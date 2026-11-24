<?php

namespace Tests\Feature\Admin;

use App\Console\Commands\CheckVirusCommand;
use App\Jobs\DeliverAlert;
use App\Models\NotificationChannel;
use App\Models\Setting;
use App\Models\User;
use App\Services\ServerSettingsService;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Queue;
use Mockery\MockInterface;
use Tests\TestCase;

class CheckVirusCommandTest extends TestCase
{
    use RefreshDatabase;

    /**
     * @return array{id: string, original_path: string, owner: string, virus: string, quarantined_at: string, source: string}
     */
    private static function entry(string $id): array
    {
        return [
            'id' => $id,
            'original_path' => '/home/budi/x.php',
            'owner' => 'budi',
            'virus' => 'Eicar-Test-Signature',
            'quarantined_at' => '2026-11-24T10:00:00Z',
            'source' => 'on-demand',
        ];
    }

    public function test_it_notifies_admins_about_a_new_quarantined_file_once(): void
    {
        Queue::fake();

        $admin = User::factory()->admin()->create();
        NotificationChannel::factory()->for($admin)->create(['target' => 'admin@example.test']);

        $this->mock(ServerSettingsService::class, function (MockInterface $mock) {
            $mock->shouldReceive('clamav')->andReturn(['enabled' => true, 'email' => false, 'on_access' => false, 'scheduled' => false]);
            $mock->shouldReceive('quarantineList')->andReturn([self::entry('abc123')]);
        });

        $this->artisan('rayxo:check-virus')->assertSuccessful();

        Queue::assertPushed(DeliverAlert::class, 1);
        $this->assertDatabaseHas(Setting::class, ['key' => CheckVirusCommand::KNOWN]);

        // The same file on the next run raises nothing new.
        $this->artisan('rayxo:check-virus')->assertSuccessful();

        Queue::assertPushed(DeliverAlert::class, 1);
    }

    public function test_it_is_quiet_when_clamav_is_off(): void
    {
        Queue::fake();

        $admin = User::factory()->admin()->create();
        NotificationChannel::factory()->for($admin)->create(['target' => 'admin@example.test']);

        $this->mock(ServerSettingsService::class, function (MockInterface $mock) {
            $mock->shouldReceive('clamav')->andReturn(['enabled' => false, 'email' => false, 'on_access' => false, 'scheduled' => false]);
        });

        $this->artisan('rayxo:check-virus')->assertSuccessful();

        Queue::assertNotPushed(DeliverAlert::class);
    }
}
