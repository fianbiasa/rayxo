<?php

namespace Tests\Feature\Databases;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Enums\TaskStatus;
use App\Events\AgentTaskFinished;
use App\Models\Database;
use App\Models\Task;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Mockery\MockInterface;
use Tests\TestCase;

/**
 * When a restore has loaded a database's data, the sizes are measured at
 * once instead of staying zero until the next scheduled collection.
 */
class MeasureRestoredDatabasesTest extends TestCase
{
    use RefreshDatabase;

    public function test_a_successful_server_restore_measures_the_databases_now()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $database = Database::factory()->create([
            'user_id' => $account->id,
            'name' => 'budi_blog',
            'size_bytes' => 0,
            'measured_at' => null,
        ]);

        $this->mock(AgentClient::class, function (MockInterface $mock) {
            $mock->shouldReceive('call')->once()
                ->with('db.report', [], config('agent.timeout'))
                ->andReturn(['databases' => [['name' => 'budi_blog', 'size_bytes' => 5_242_880]]]);
        });

        event(new AgentTaskFinished($this->restoreTask($account, TaskStatus::Success)));

        $database->refresh();

        $this->assertSame(5_242_880, $database->size_bytes);
        $this->assertNotNull($database->measured_at);
    }

    public function test_a_failed_server_restore_does_not_measure()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $database = Database::factory()->create(['user_id' => $account->id, 'name' => 'budi_blog', 'size_bytes' => 0]);

        $this->mock(AgentClient::class, function (MockInterface $mock) {
            $mock->shouldReceive('call')->never();
        });

        event(new AgentTaskFinished($this->restoreTask($account, TaskStatus::Failed)));

        $this->assertSame(0, $database->fresh()->size_bytes);
    }

    public function test_a_task_other_than_a_server_restore_does_not_measure()
    {
        $account = User::factory()->create(['username' => 'budi']);

        $this->mock(AgentClient::class, function (MockInterface $mock) {
            $mock->shouldReceive('call')->never();
        });

        $task = $this->restoreTask($account, TaskStatus::Success);
        $task->forceFill(['action' => 'backup.create'])->save();

        event(new AgentTaskFinished($task));
    }

    public function test_an_unreachable_agent_leaves_the_sizes_for_the_scheduled_collection()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $database = Database::factory()->create(['user_id' => $account->id, 'name' => 'budi_blog', 'size_bytes' => 0]);

        $this->mock(AgentClient::class, function (MockInterface $mock) {
            $mock->shouldReceive('call')->once()
                ->with('db.report', [], config('agent.timeout'))
                ->andThrow(new AgentException('the agent is unreachable'));
        });

        // The listener swallows the failure; no exception leaves it.
        event(new AgentTaskFinished($this->restoreTask($account, TaskStatus::Success)));

        $this->assertSame(0, $database->fresh()->size_bytes);
    }

    private function restoreTask(User $account, TaskStatus $status): Task
    {
        return Task::create([
            'user_id' => $account->id,
            'actor_id' => $account->id,
            'action' => 'backup.server_restore',
            'payload' => ['username' => $account->username],
            'status' => $status,
        ]);
    }
}
