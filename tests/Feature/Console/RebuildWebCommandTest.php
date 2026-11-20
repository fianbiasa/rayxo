<?php

namespace Tests\Feature\Console;

use App\Enums\AccountStatus;
use App\Enums\NodeAppStatus;
use App\Models\Domain;
use App\Models\NodeApp;
use App\Models\Task;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Queue;
use Tests\TestCase;

class RebuildWebCommandTest extends TestCase
{
    use RefreshDatabase;

    public function test_it_queues_every_active_website_with_its_current_state()
    {
        Queue::fake();

        $budi = User::factory()->create(['username' => 'budi']);
        Domain::factory()->for($budi, 'account')->create(['name' => 'plain.example']);
        Domain::factory()->for($budi, 'account')->sslActive(forceHttps: true)->create(['name' => 'secure.example']);
        Domain::factory()->for($budi, 'account')->pending()->create(['name' => 'pending.example']);
        Domain::factory()->for($budi, 'account')->failed()->create(['name' => 'failed.example']);

        $suspended = User::factory()->suspended()->create(['username' => 'sari']);
        Domain::factory()->for($suspended, 'account')->create(['name' => 'sari.example']);

        $leaving = User::factory()->create(['username' => 'leaving', 'status' => AccountStatus::Deleting]);
        Domain::factory()->for($leaving, 'account')->create(['name' => 'leaving.example']);

        $this->artisan('rayxo:rebuild-web')
            ->expectsOutputToContain('3 websites queued')
            ->assertSuccessful();

        $payloads = Task::query()
            ->where('action', 'web.apply_domain')
            ->get()
            ->mapWithKeys(fn (Task $task): array => [$task->payload['domain'] => $task->payload]);

        $this->assertSame(['plain.example', 'sari.example', 'secure.example'], $payloads->keys()->sort()->values()->all());
        $this->assertSame([
            'username' => 'budi',
            'domain' => 'plain.example',
            'php_version' => '8.4',
            'ssl' => false,
            'force_https' => false,
        ], $payloads['plain.example']);
        $this->assertTrue($payloads['secure.example']['ssl']);
        $this->assertTrue($payloads['secure.example']['force_https']);
    }

    public function test_it_reprovisions_enabled_node_apps_so_the_socket_exists_after_the_rebuild()
    {
        // setup.sh runs this on every upgrade; it re-points Nginx at each Node
        // app's socket, so it must re-provision the apps or the socket would be
        // missing after an upgrade that changed how apps are served.
        Queue::fake();

        $budi = User::factory()->create(['username' => 'budi']);
        $domain = Domain::factory()->for($budi, 'account')->create(['name' => 'app.example']);
        $app = new NodeApp;
        $app->domain()->associate($domain);
        $app->forceFill([
            'entry' => 'index.mjs',
            'port' => 40000,
            'enabled' => true,
            'status' => NodeAppStatus::Active,
        ])->save();

        $this->artisan('rayxo:rebuild-web')
            ->expectsOutputToContain('1 Node.js app queued for re-provisioning.')
            ->assertSuccessful();

        $this->assertSame(
            ['app.example'],
            Task::query()->where('action', 'nodeapp.provision')->get()->pluck('payload.domain')->all(),
        );
    }
}
