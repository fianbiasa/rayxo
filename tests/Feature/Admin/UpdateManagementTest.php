<?php

namespace Tests\Feature\Admin;

use App\Agent\AgentClient;
use App\Agent\FakeAgentClient;
use App\Models\AuditLog;
use App\Models\Setting;
use App\Models\User;
use App\Services\UpdateService;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Inertia\Testing\AssertableInertia as Assert;
use Tests\TestCase;

class UpdateManagementTest extends TestCase
{
    use RefreshDatabase;

    protected function setUp(): void
    {
        parent::setUp();

        Cache::flush();
    }

    /**
     * @param  list<array{version: string, prerelease?: bool}>  $releases
     */
    private function fakeReleases(array $releases): void
    {
        $body = array_map(fn (array $release): array => [
            'tag_name' => 'v'.$release['version'],
            'name' => 'RAYXO '.$release['version'],
            'html_url' => 'https://github.com/fianbiasa/rayxo/releases/tag/v'.$release['version'],
            'published_at' => '2026-11-20T00:00:00Z',
            'draft' => false,
            'prerelease' => $release['prerelease'] ?? false,
            'assets' => [],
        ], $releases);

        Http::fake(['api.github.com/*' => Http::response($body, 200)]);
    }

    public function test_hosting_accounts_cannot_start_an_update_or_change_the_channel()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $this->fakeReleases([['version' => '0.9.0']]);

        $user = User::factory()->create();

        $this->actingAs($user)->post(route('admin.server-settings.update'))->assertForbidden();
        $this->actingAs($user)->put(route('admin.server-settings.update-channel'), ['channel' => 'beta'])->assertForbidden();

        $this->assertSame([], array_filter($agent->calls(), fn (array $call): bool => $call['method'] === 'system.update'));
    }

    public function test_the_page_shows_the_installed_version_and_an_available_update()
    {
        $this->app->instance(AgentClient::class, new FakeAgentClient);
        $this->fakeReleases([['version' => '0.9.0'], ['version' => '1.0.0-rc.1', 'prerelease' => true]]);

        $this->actingAs(User::factory()->admin()->create())
            ->get(route('admin.server-settings.index'))
            ->assertInertia(fn (Assert $page) => $page
                ->component('admin/server-settings/index')
                ->where('update.installed', 'dev')
                ->where('update.channel', 'stable')
                ->where('update.stable.version', '0.9.0')
                ->where('update.beta.version', '1.0.0-rc.1')
                ->where('update.available.version', '0.9.0'));
    }

    public function test_the_beta_channel_offers_the_newest_pre_release()
    {
        $this->app->instance(AgentClient::class, new FakeAgentClient);
        $this->fakeReleases([['version' => '0.9.0'], ['version' => '1.0.0-rc.1', 'prerelease' => true]]);

        $admin = User::factory()->admin()->create();
        Setting::query()->create(['key' => UpdateService::CHANNEL, 'value' => 'beta']);

        $this->actingAs($admin)
            ->get(route('admin.server-settings.index'))
            ->assertInertia(fn (Assert $page) => $page
                ->where('update.channel', 'beta')
                ->where('update.available.version', '1.0.0-rc.1'));
    }

    public function test_setting_the_channel_is_saved_and_audited()
    {
        $this->app->instance(AgentClient::class, new FakeAgentClient);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->put(route('admin.server-settings.update-channel'), ['channel' => 'beta'])
            ->assertSessionHasNoErrors();

        $this->assertDatabaseHas(Setting::class, ['key' => UpdateService::CHANNEL, 'value' => json_encode('beta')]);
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'server.update_channel_changed', 'actor_id' => $admin->id]);
    }

    public function test_an_invalid_channel_is_rejected()
    {
        $this->app->instance(AgentClient::class, new FakeAgentClient);

        $this->actingAs(User::factory()->admin()->create())
            ->put(route('admin.server-settings.update-channel'), ['channel' => 'nightly'])
            ->assertSessionHasErrors('channel');
    }

    public function test_starting_an_update_asks_the_agent_for_the_target_release()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $this->fakeReleases([['version' => '0.9.0']]);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->post(route('admin.server-settings.update'))
            ->assertSessionHasNoErrors();

        $update = array_values(array_filter($agent->calls(), fn (array $call): bool => $call['method'] === 'system.update'));

        $this->assertCount(1, $update);
        $this->assertSame('0.9.0', $update[0]['params']['version']);
        $this->assertSame('https://github.com/fianbiasa/rayxo/releases/download/v0.9.0/setup.sh', $update[0]['params']['setup_url']);
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'server.update_started', 'actor_id' => $admin->id]);
    }

    public function test_an_update_is_refused_while_a_backup_job_is_queued()
    {
        $agent = $this->app->instance(AgentClient::class, new FakeAgentClient);
        $this->fakeReleases([['version' => '0.9.0']]);

        DB::table('jobs')->insert([
            'queue' => 'backups',
            'payload' => '{}',
            'attempts' => 0,
            'reserved_at' => null,
            'available_at' => time(),
            'created_at' => time(),
        ]);

        $this->actingAs(User::factory()->admin()->create())
            ->post(route('admin.server-settings.update'))
            ->assertSessionHasErrors('update');

        $this->assertSame([], array_filter($agent->calls(), fn (array $call): bool => $call['method'] === 'system.update'));
    }

    public function test_no_update_is_offered_when_already_current()
    {
        $this->app->instance(AgentClient::class, new FakeAgentClient);
        $this->fakeReleases([]);
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->get(route('admin.server-settings.index'))
            ->assertInertia(fn (Assert $page) => $page->where('update.available', null));

        $this->actingAs($admin)
            ->post(route('admin.server-settings.update'))
            ->assertSessionHasErrors('update');
    }
}
