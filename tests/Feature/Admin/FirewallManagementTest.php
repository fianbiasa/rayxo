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

class FirewallManagementTest extends TestCase
{
    use RefreshDatabase;

    /**
     * @param  array<string, mixed>  $overrides
     * @return array<string, mixed>
     */
    private function config(array $overrides = []): array
    {
        return array_merge([
            'enabled' => true,
            'tcp_ports' => [],
            'udp_ports' => [],
            'allow_ips' => [],
            'deny_ips' => [],
        ], $overrides);
    }

    public function test_hosting_accounts_cannot_reach_the_firewall()
    {
        $this->actingAs(User::factory()->create())
            ->get(route('admin.firewall.index'))
            ->assertForbidden();
    }

    public function test_the_page_shows_what_the_agent_reports()
    {
        $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->get(route('admin.firewall.index'))
            ->assertOk()
            ->assertInertia(fn (AssertableInertia $page) => $page
                ->component('admin/firewall/index')
                ->where('error', null)
                ->where('status.active.enabled', false)
                ->where('status.pending', null)
                ->where('status.ftp_passive', '35000-35999'));
    }

    public function test_an_agent_that_does_not_answer_is_explained()
    {
        $this->mock(AgentClient::class, fn (MockInterface $mock) => $mock
            ->shouldReceive('call')->once()->andThrow(new AgentException('agent is not running')));

        $this->actingAs(User::factory()->admin()->create())
            ->get(route('admin.firewall.index'))
            ->assertOk()
            ->assertInertia(fn (AssertableInertia $page) => $page
                ->where('status', null)
                ->where('error', 'The agent did not answer. Check that rayxod is running: systemctl status rayxod'));
    }

    public function test_enabling_the_firewall_asks_the_agent_and_is_audited()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->put(route('admin.firewall.apply'), $this->config(['tcp_ports' => [8080], 'allow_ips' => ['203.0.113.0/24']]))
            ->assertSessionHasNoErrors();

        $this->assertSame(
            [['method' => 'firewall.apply', 'params' => [
                'enabled' => true,
                'tcp_ports' => [8080],
                'udp_ports' => [],
                'allow_ips' => ['203.0.113.0/24'],
                'deny_ips' => [],
            ]]],
            $agent->calls(),
        );
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'firewall.applied', 'actor_id' => $admin->id]);
    }

    public function test_apply_rejects_a_bad_port_without_touching_the_agent()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->put(route('admin.firewall.apply'), $this->config(['tcp_ports' => [70000]]))
            ->assertSessionHasErrors(['tcp_ports.0']);

        $this->assertSame([], $agent->calls());
    }

    public function test_apply_rejects_a_bad_address_without_touching_the_agent()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->put(route('admin.firewall.apply'), $this->config(['deny_ips' => ['evil; rm -rf']]))
            ->assertSessionHasErrors(['deny_ips.0']);

        $this->assertSame([], $agent->calls());
    }

    public function test_confirm_asks_the_agent_and_is_audited()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->post(route('admin.firewall.confirm'))
            ->assertSessionHasNoErrors();

        $this->assertSame([['method' => 'firewall.confirm', 'params' => []]], $agent->calls());
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'firewall.confirmed', 'actor_id' => $admin->id]);
    }

    public function test_cancel_asks_the_agent_and_is_audited()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->post(route('admin.firewall.cancel'))
            ->assertSessionHasNoErrors();

        $this->assertSame([['method' => 'firewall.cancel', 'params' => []]], $agent->calls());
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'firewall.cancelled', 'actor_id' => $admin->id]);
    }

    public function test_hosting_accounts_cannot_change_the_firewall()
    {
        $this->actingAs(User::factory()->create())
            ->put(route('admin.firewall.apply'), $this->config())
            ->assertForbidden();
    }
}
