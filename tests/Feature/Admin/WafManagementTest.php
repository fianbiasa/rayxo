<?php

namespace Tests\Feature\Admin;

use App\Agent\AgentClient;
use App\Agent\FakeAgentClient;
use App\Models\AuditLog;
use App\Models\Setting;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Tests\TestCase;

class WafManagementTest extends TestCase
{
    use RefreshDatabase;

    public function test_hosting_accounts_cannot_change_the_waf_mode()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->create())
            ->put(route('admin.server-settings.waf'), ['mode' => 'on'])
            ->assertForbidden();

        $this->assertSame([], $agent->calls());
    }

    public function test_setting_the_waf_mode_asks_the_agent_and_is_saved()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->put(route('admin.server-settings.waf'), ['mode' => 'detection'])
            ->assertSessionHasNoErrors();

        $this->assertSame('waf.apply', $agent->calls()[0]['method']);
        $this->assertSame(['mode' => 'detection'], $agent->calls()[0]['params']);
        $this->assertDatabaseHas(Setting::class, ['key' => 'waf']);
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'server.waf_changed', 'actor_id' => $admin->id]);
    }

    public function test_an_unknown_waf_mode_is_rejected()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->put(route('admin.server-settings.waf'), ['mode' => 'block-everything'])
            ->assertSessionHasErrors(['mode']);

        $this->assertSame([], $agent->calls());
    }
}
