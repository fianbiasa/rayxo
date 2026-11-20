<?php

namespace Tests\Feature\Admin;

use App\Console\Commands\CheckDriftCommand;
use App\Enums\TaskStatus;
use App\Models\AuditLog;
use App\Models\ConfigDrift;
use App\Models\Setting;
use App\Models\Task;
use App\Models\User;
use App\Services\DriftService;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Tests\TestCase;

class ConfigDriftTest extends TestCase
{
    use RefreshDatabase;

    private function checkTask(User $account, bool $drifted): Task
    {
        $task = new Task;
        $task->forceFill([
            'action' => 'web.check_domain',
            'user_id' => $account->id,
            'status' => TaskStatus::Success,
            'payload' => ['username' => $account->username, 'domain' => 'example.com', 'php_version' => '8.4'],
            'output' => ['targets' => [[
                'kind' => 'nginx-vhost',
                'path' => '/etc/nginx/rayxo/'.$account->username.'/example.com.conf',
                'drifted' => $drifted,
                'missing' => false,
                'diff' => $drifted ? "-old\n+new\n" : '',
            ]]],
        ])->save();

        return $task;
    }

    public function test_recording_a_check_result_stores_only_drifting_targets()
    {
        $account = User::factory()->create();
        $service = app(DriftService::class);

        $service->recordResult($this->checkTask($account, true));

        $this->assertDatabaseHas(ConfigDrift::class, [
            'kind' => 'nginx-vhost',
            'apply_action' => 'web.apply_domain',
            'user_id' => $account->id,
        ]);

        // A later check that matches clears the row.
        $service->recordResult($this->checkTask($account, false));

        $this->assertDatabaseCount(ConfigDrift::class, 0);
    }

    public function test_recording_ignores_tasks_that_are_not_checks()
    {
        $account = User::factory()->create();

        $task = new Task;
        $task->forceFill([
            'action' => 'web.apply_domain',
            'user_id' => $account->id,
            'status' => TaskStatus::Success,
            'output' => ['targets' => [['kind' => 'nginx-vhost', 'path' => '/x', 'drifted' => true]]],
        ])->save();

        app(DriftService::class)->recordResult($task);

        $this->assertDatabaseCount(ConfigDrift::class, 0);
    }

    public function test_rewriting_a_drift_dispatches_the_apply_action()
    {
        $account = User::factory()->create();
        $admin = User::factory()->admin()->create();

        $drift = ConfigDrift::query()->create([
            'user_id' => $account->id,
            'kind' => 'nginx-vhost',
            'path' => '/etc/nginx/rayxo/x/example.com.conf',
            'apply_action' => 'web.apply_domain',
            'payload' => ['username' => $account->username, 'domain' => 'example.com', 'php_version' => '8.4'],
            'missing' => false,
            'diff' => "-a\n+b\n",
            'checked_at' => now(),
        ]);

        app(DriftService::class)->rewrite($drift, $admin);

        $this->assertDatabaseHas(Task::class, ['action' => 'web.apply_domain', 'user_id' => $account->id]);
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'server.drift_rewritten', 'actor_id' => $admin->id]);
    }

    public function test_hosting_accounts_cannot_scan_or_see_drift()
    {
        $this->actingAs(User::factory()->create())
            ->post(route('admin.services.drift.scan'))
            ->assertForbidden();
    }

    public function test_admin_can_start_a_scan()
    {
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->post(route('admin.services.drift.scan'))
            ->assertSessionHasNoErrors();

        $this->assertDatabaseHas(Setting::class, ['key' => DriftService::LAST_SCAN]);
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'server.drift_scanned', 'actor_id' => $admin->id]);
    }

    public function test_the_drift_check_command_announces_drift_once()
    {
        $account = User::factory()->create();

        ConfigDrift::query()->create([
            'user_id' => $account->id,
            'kind' => 'nginx-vhost',
            'path' => '/etc/nginx/rayxo/x/example.com.conf',
            'apply_action' => 'web.apply_domain',
            'payload' => [],
            'missing' => false,
            'diff' => null,
            'checked_at' => now(),
        ]);

        $this->artisan('rayxo:check-drift')->assertSuccessful();

        $this->assertDatabaseHas(Setting::class, ['key' => CheckDriftCommand::LAST_NOTIFIED]);
    }
}
