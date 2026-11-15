<?php

namespace App\Jobs;

use App\Models\ServerBackup;
use App\Models\SystemBackup;
use App\Services\RemoteBackupService;
use App\Services\ServerBackupService;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Foundation\Queue\Queueable;
use Throwable;

/**
 * The copy of a finished backup to the remote destination, on the backups
 * queue.
 */
class CopyBackupToRemote implements ShouldQueue
{
    use Queueable;

    public int $tries = 1;

    public int $timeout = ServerBackupService::BACKUP_SECONDS + 300;

    public bool $failOnTimeout = true;

    public bool $deleteWhenMissingModels = true;

    public function __construct(public ServerBackup|SystemBackup $backup)
    {
        $this->onQueue(ServerBackupService::QUEUE);
    }

    public function handle(RemoteBackupService $remote): void
    {
        $remote->copy($this->backup);
    }

    public function failed(?Throwable $exception): void
    {
        app(RemoteBackupService::class)->failCopy($this->backup, __('The copy did not finish.'));
    }
}
