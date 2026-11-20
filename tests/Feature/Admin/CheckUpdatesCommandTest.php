<?php

namespace Tests\Feature\Admin;

use App\Agent\AgentClient;
use App\Agent\FakeAgentClient;
use App\Jobs\DeliverAlert;
use App\Models\NotificationChannel;
use App\Models\Setting;
use App\Models\User;
use App\Services\UpdateService;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Queue;
use Tests\TestCase;

class CheckUpdatesCommandTest extends TestCase
{
    use RefreshDatabase;

    protected function setUp(): void
    {
        parent::setUp();

        Cache::flush();
        $this->app->instance(AgentClient::class, new FakeAgentClient);
    }

    /**
     * @param  list<string>  $versions
     */
    private function fakeReleases(array $versions): void
    {
        $body = array_map(fn (string $version): array => [
            'tag_name' => 'v'.$version,
            'name' => 'RAYXO '.$version,
            'html_url' => 'https://github.com/fianbiasa/rayxo/releases/tag/v'.$version,
            'published_at' => '2026-11-20T00:00:00Z',
            'draft' => false,
            'prerelease' => false,
            'assets' => [],
        ], $versions);

        Http::fake(['api.github.com/*' => Http::response($body, 200)]);
    }

    public function test_it_notifies_admins_about_a_newer_release_once()
    {
        Queue::fake();
        $this->fakeReleases(['0.9.0']);

        $admin = User::factory()->admin()->create();
        NotificationChannel::factory()->for($admin)->create(['target' => 'admin@example.test']);

        $this->artisan('rayxo:check-updates')->assertSuccessful();

        Queue::assertPushed(DeliverAlert::class, 1);
        $this->assertDatabaseHas(Setting::class, ['key' => UpdateService::LAST_NOTIFIED, 'value' => json_encode('0.9.0')]);

        // Running it again announces nothing new for the same version.
        $this->artisan('rayxo:check-updates')->assertSuccessful();

        Queue::assertPushed(DeliverAlert::class, 1);
    }

    public function test_it_is_quiet_when_there_is_no_newer_release()
    {
        Queue::fake();
        $this->fakeReleases([]);

        $admin = User::factory()->admin()->create();
        NotificationChannel::factory()->for($admin)->create(['target' => 'admin@example.test']);

        $this->artisan('rayxo:check-updates')->assertSuccessful();

        Queue::assertNotPushed(DeliverAlert::class);
        $this->assertDatabaseMissing(Setting::class, ['key' => UpdateService::LAST_NOTIFIED]);
    }
}
