<?php

namespace App\Listeners;

use App\Enums\NodeAppStatus;
use App\Enums\TaskStatus;
use App\Events\AgentTaskFinished;
use App\Models\Domain;

/**
 * Bring a domain's Node.js app record in line with what the agent did on the
 * server: active once the app started, failed when it could not. The task's
 * error is shown in the task list.
 */
class ApplyNodeAppTaskResult
{
    public function handle(AgentTaskFinished $event): void
    {
        $task = $event->task;

        if ($task->action !== 'nodeapp.provision') {
            return;
        }

        $domain = Domain::findForTask($task);
        $app = $domain?->nodeApp;

        if ($app === null || ! $app->enabled) {
            return;
        }

        if ($task->status === TaskStatus::Success && $app->status !== NodeAppStatus::Active) {
            $app->forceFill(['status' => NodeAppStatus::Active])->save();
        } elseif ($task->status === TaskStatus::Failed) {
            $app->forceFill(['status' => NodeAppStatus::Failed])->save();
        }
    }
}
