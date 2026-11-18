<?php

namespace Tests\Feature\Admin;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Agent\FakeAgentClient;
use App\Models\AuditLog;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Inertia\Testing\AssertableInertia;
use Mockery\MockInterface;
use Tests\TestCase;

class BruteForceManagementTest extends TestCase
{
    use RefreshDatabase;

    public function test_hosting_accounts_cannot_reach_the_brute_force_monitor()
    {
        $this->actingAs(User::factory()->create())
            ->get(route('admin.brute-force.index'))
            ->assertForbidden();
    }

    public function test_the_page_shows_what_the_agent_reports()
    {
        $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->get(route('admin.brute-force.index'))
            ->assertOk()
            ->assertInertia(fn (AssertableInertia $page) => $page
                ->component('admin/brute-force/index')
                ->where('error', null)
                ->where('report.jails.0.name', 'sshd')
                ->where('report.jails.0.banned_ips.0', '203.0.113.7')
                ->where('report.whitelist.0', '198.51.100.4'));
    }

    public function test_an_agent_that_does_not_answer_is_explained()
    {
        $this->mock(AgentClient::class, fn (MockInterface $mock) => $mock
            ->shouldReceive('call')->once()->andThrow(new AgentException('agent is not running')));

        $this->actingAs(User::factory()->admin()->create())
            ->get(route('admin.brute-force.index'))
            ->assertOk()
            ->assertInertia(fn (AssertableInertia $page) => $page
                ->where('report', null)
                ->where('error', 'The agent did not answer. Check that rayxod is running: systemctl status rayxod'));
    }

    public function test_unbanning_asks_the_agent_and_is_audited()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->post(route('admin.brute-force.unban'), ['ip' => '203.0.113.7'])
            ->assertSessionHasNoErrors()
            ->assertInertiaFlash('toast.message', '203.0.113.7 is no longer banned.');

        $this->assertSame(
            [['method' => 'bfm.unban', 'params' => ['ip' => '203.0.113.7']]],
            $agent->calls(),
        );
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'bfm.unbanned', 'actor_id' => $admin->id]);
    }

    public function test_unban_rejects_a_bad_address_without_touching_the_agent()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->post(route('admin.brute-force.unban'), ['ip' => 'not-an-ip'])
            ->assertSessionHasErrors(['ip']);

        $this->assertSame([], $agent->calls());
        $this->assertDatabaseMissing(AuditLog::class, ['action' => 'bfm.unbanned']);
    }

    public function test_the_whitelist_is_saved_and_audited()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->put(route('admin.brute-force.whitelist'), ['ips' => ['198.51.100.4', '203.0.113.0/24']])
            ->assertSessionHasNoErrors()
            ->assertInertiaFlash('toast.message', 'The whitelist has been saved.');

        $this->assertSame(
            [['method' => 'bfm.whitelist', 'params' => ['ips' => ['198.51.100.4', '203.0.113.0/24']]]],
            $agent->calls(),
        );
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'bfm.whitelist_changed', 'actor_id' => $admin->id]);
    }

    public function test_the_whitelist_may_be_emptied()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->put(route('admin.brute-force.whitelist'), ['ips' => []])
            ->assertSessionHasNoErrors();

        $this->assertSame(
            [['method' => 'bfm.whitelist', 'params' => ['ips' => []]]],
            $agent->calls(),
        );
    }

    public function test_the_whitelist_rejects_a_bad_entry_without_touching_the_agent()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->put(route('admin.brute-force.whitelist'), ['ips' => ['198.51.100.4', 'evil; rm -rf']])
            ->assertSessionHasErrors(['ips.1']);

        $this->assertSame([], $agent->calls());
        $this->assertDatabaseMissing(AuditLog::class, ['action' => 'bfm.whitelist_changed']);
    }

    public function test_hosting_accounts_cannot_change_the_whitelist()
    {
        $this->actingAs(User::factory()->create())
            ->put(route('admin.brute-force.whitelist'), ['ips' => ['198.51.100.4']])
            ->assertForbidden();
    }
}
