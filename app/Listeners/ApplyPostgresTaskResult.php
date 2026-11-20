<?php

namespace App\Listeners;

use App\Enums\DatabaseStatus;
use App\Enums\TaskStatus;
use App\Events\AgentTaskFinished;
use App\Models\PostgresDatabase;
use App\Models\PostgresUser;
use App\Models\Task;

/**
 * Bring a PostgreSQL database or role's panel record in line with what the
 * agent did on the server. The task's error is shown in the task list.
 * Results that arrive after the record has moved on are ignored.
 */
class ApplyPostgresTaskResult
{
    public function handle(AgentTaskFinished $event): void
    {
        $task = $event->task;

        $record = match ($task->action) {
            'pg.create_database', 'pg.delete_database' => $this->find(PostgresDatabase::class, $task, 'database'),
            'pg.apply_user', 'pg.delete_user' => $this->find(PostgresUser::class, $task, 'user'),
            default => null,
        };

        if ($record === null) {
            return;
        }

        $removal = in_array($task->action, ['pg.delete_database', 'pg.delete_user'], true);
        $expected = $removal ? DatabaseStatus::Deleting : DatabaseStatus::Pending;

        if ($record->status !== $expected) {
            return;
        }

        if ($task->status === TaskStatus::Success) {
            $removal ? $record->delete() : $record->forceFill(['status' => DatabaseStatus::Active])->save();
        } elseif ($task->status === TaskStatus::Failed) {
            $record->forceFill(['status' => DatabaseStatus::Failed])->save();
        }
    }

    /**
     * The record of the task's account named in its payload.
     *
     * @param  class-string<PostgresDatabase>|class-string<PostgresUser>  $model
     */
    private function find(string $model, Task $task, string $key): PostgresDatabase|PostgresUser|null
    {
        $name = $task->payload[$key] ?? null;

        if ($task->user_id === null || ! is_string($name)) {
            return null;
        }

        return $model::query()->where('user_id', $task->user_id)->where('name', $name)->first();
    }
}
