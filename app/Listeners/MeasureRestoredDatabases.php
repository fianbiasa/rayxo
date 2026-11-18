<?php

namespace App\Listeners;

use App\Agent\AgentException;
use App\Enums\TaskStatus;
use App\Events\AgentTaskFinished;
use App\Services\DatabaseService;

/**
 * Measure database sizes as soon as a restore has loaded their data, so an
 * imported, restored or transferred account shows real database sizes at
 * once instead of zero until the next scheduled collection. A failure here
 * is swallowed: the scheduled collection measures them within the hour.
 */
class MeasureRestoredDatabases
{
    public function __construct(private DatabaseService $databases) {}

    public function handle(AgentTaskFinished $event): void
    {
        $task = $event->task;

        if ($task->action !== 'backup.server_restore' || $task->status !== TaskStatus::Success) {
            return;
        }

        try {
            $this->databases->collectSizes();
        } catch (AgentException) {
            // The scheduled collection will measure the sizes shortly.
        }
    }
}
