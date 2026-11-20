<?php

namespace Tests\Feature\Console;

use App\Enums\AccountStatus;
use App\Enums\NodeAppStatus;
use App\Models\CronJob;
use App\Models\Domain;
use App\Models\FtpAccount;
use App\Models\NodeApp;
use App\Models\Task;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Queue;
use Tests\TestCase;

class RebuildCommandTest extends TestCase
{
    use RefreshDatabase;

    public function test_it_queues_every_config_of_the_accounts_that_exist_on_the_server()
    {
        Queue::fake();

        $budi = User::factory()->create(['username' => 'budi']);
        $budiFtp = FtpAccount::factory()->for($budi, 'account')->create();
        CronJob::factory()->for($budi, 'account')->create(['schedule' => '@daily', 'command' => 'php backup.php']);
        CronJob::factory()->for($budi, 'account')->create(['enabled' => false]);
        CronJob::factory()->for($budi, 'account')->create(['schedule' => '*/5 * * * *', 'command' => 'php queue.php']);

        $sari = User::factory()->suspended()->create(['username' => 'sari']);
        $sariFtp = FtpAccount::factory()->for($sari, 'account')->create();

        $leaving = User::factory()->create(['status' => AccountStatus::Deleting]);
        FtpAccount::factory()->for($leaving, 'account')->create();
        CronJob::factory()->for($leaving, 'account')->create();
        User::factory()->admin()->create();

        $this->artisan('rayxo:rebuild')
            ->expectsOutputToContain('Queued: 2 websites, 0 Node.js apps, 2 disk quotas, 2 crontabs, 2 FTP folders, 2 DNS zones, 0 mail domains.')
            ->assertSuccessful();

        $this->assertEqualsCanonicalizing(
            ['budi', 'sari'],
            Task::query()->where('action', 'account.set_quota')->get()->pluck('payload.username')->all(),
        );
        $this->assertSame(2, Task::query()->where('action', 'web.apply_domain')->count());
        $this->assertEqualsCanonicalizing(['budi', 'sari'], Task::query()->where('action', 'dns.sync_zone')->get()->pluck('payload.username')->all());

        $crontabs = Task::query()
            ->where('action', 'cron.apply')
            ->get()
            ->mapWithKeys(fn (Task $task): array => [$task->payload['username'] => $task->payload['jobs']]);
        $this->assertSame([
            'budi' => [
                ['schedule' => '@daily', 'command' => 'php backup.php'],
                ['schedule' => '*/5 * * * *', 'command' => 'php queue.php'],
            ],
            'sari' => [],
        ], $crontabs->sortKeys()->all());

        $this->assertEqualsCanonicalizing([
            ['username' => 'budi', 'directory' => $budiFtp->directory],
            ['username' => 'sari', 'directory' => $sariFtp->directory],
        ], Task::query()->where('action', 'ftp.ensure_dir')->get()->pluck('payload')->all());
    }

    public function test_it_reprovisions_enabled_node_apps_so_their_sockets_are_recreated()
    {
        // On an upgrade the website rebuild re-points Nginx at each Node app's
        // socket, so the rebuild must re-provision the apps or the socket would
        // be missing.
        Queue::fake();

        $budi = User::factory()->create(['username' => 'budi']);
        $domain = Domain::factory()->for($budi, 'account')->create(['name' => 'app.test']);
        $app = new NodeApp;
        $app->domain()->associate($domain);
        $app->forceFill([
            'entry' => 'index.mjs',
            'port' => 40000,
            'enabled' => true,
            'status' => NodeAppStatus::Active,
        ])->save();

        $this->artisan('rayxo:rebuild')
            ->expectsOutputToContain('1 Node.js apps')
            ->assertSuccessful();

        $this->assertSame(
            ['app.test'],
            Task::query()->where('action', 'nodeapp.provision')->get()->pluck('payload.domain')->all(),
        );
    }
}
