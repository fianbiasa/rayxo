<?php

namespace Tests\Feature\Admin;

use App\Agent\AgentClient;
use App\Agent\FakeAgentClient;
use App\Models\AuditLog;
use App\Models\Setting;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Tests\TestCase;

class ClamavManagementTest extends TestCase
{
    use RefreshDatabase;

    public function test_hosting_accounts_cannot_reach_server_settings()
    {
        $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->create())
            ->get(route('admin.server-settings.index'))
            ->assertForbidden();
    }

    public function test_enabling_clamav_asks_the_agent_and_is_saved()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->put(route('admin.server-settings.clamav'), [
                'enabled' => true, 'email' => true, 'on_access' => false, 'scheduled' => true,
            ])
            ->assertSessionHasNoErrors();

        $this->assertSame('clamav.apply', $agent->calls()[0]['method']);
        $this->assertSame(
            ['enabled' => true, 'email' => true, 'on_access' => false, 'scheduled' => true],
            $agent->calls()[0]['params'],
        );
        $this->assertDatabaseHas(Setting::class, ['key' => 'clamav']);
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'server.clamav_changed', 'actor_id' => $admin->id]);
    }

    public function test_sub_modes_are_forced_off_when_clamav_is_off()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->put(route('admin.server-settings.clamav'), [
                'enabled' => false, 'email' => true, 'on_access' => true, 'scheduled' => true,
            ])
            ->assertSessionHasNoErrors();

        $this->assertSame(
            ['enabled' => false, 'email' => false, 'on_access' => false, 'scheduled' => false],
            $agent->calls()[0]['params'],
        );
    }

    public function test_scan_asks_the_agent_and_is_audited()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->post(route('admin.server-settings.clamav-scan'), ['path' => '/home/budi'])
            ->assertSessionHasNoErrors();

        $this->assertSame('clamav.scan', $agent->calls()[0]['method']);
        $this->assertSame(['path' => '/home/budi'], $agent->calls()[0]['params']);
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'server.clamav_scanned']);
    }

    public function test_scan_rejects_a_path_outside_home()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->post(route('admin.server-settings.clamav-scan'), ['path' => '/etc/passwd'])
            ->assertSessionHasErrors(['path']);

        $this->assertSame([], $agent->calls());
    }

    public function test_scan_rejects_path_traversal()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->post(route('admin.server-settings.clamav-scan'), ['path' => '/home/../etc'])
            ->assertSessionHasErrors(['path']);

        $this->assertSame([], $agent->calls());
    }
}
