<?php

namespace App\Listeners;

use App\Enums\TaskStatus;
use App\Events\AgentTaskFinished;
use App\Services\DriftService;

/**
 * Store the result of a config-drift check task: which agent-managed files
 * differ from what RAYXO would write. Only "check" tasks are handled, and only
 * when they succeeded.
 */
class RecordConfigDrift
{
    public function __construct(private DriftService $drift) {}

    public function handle(AgentTaskFinished $event): void
    {
        $task = $event->task;

        if (! isset(DriftService::APPLY_ACTIONS[$task->action]) || $task->status !== TaskStatus::Success) {
            return;
        }

        $this->drift->recordResult($task);
    }
}
