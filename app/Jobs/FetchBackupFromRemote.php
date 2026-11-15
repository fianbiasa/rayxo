<?php

namespace App\Jobs;

use App\Enums\RemoteCopyStatus;
use App\Models\ServerBackup;
use App\Models\SystemBackup;
use App\Models\User;
use App\Services\RemoteBackupService;
use App\Services\ServerBackupService;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Foundation\Queue\Queueable;
use Throwable;

/**
 * A backup brought back from the remote destination, on the backups queue:
 * one the panel knows whose copy is only there, or one it does not know
 * yet from a server's folder there.
 */
class FetchBackupFromRemote implements ShouldQueue
{
    use Queueable;

    public int $tries = 1;

    public int $timeout = ServerBackupService::BACKUP_SECONDS + 300;

    public bool $failOnTimeout = true;

    public bool $deleteWhenMissingModels = true;

    /**
     * @param  array{server: string, kind: string, username: string, name: string}|null  $file
     */
    public function __construct(public ServerBackup|SystemBackup|null $backup, public ?array $file, public User $actor)
    {
        $this->onQueue(ServerBackupService::QUEUE);
    }

    public function handle(RemoteBackupService $remote): void
    {
        if ($this->backup !== null) {
            $remote->fetch($this->backup);
        } elseif ($this->file !== null) {
            $remote->take($this->file, $this->actor);
        }
    }

    public function failed(?Throwable $exception): void
    {
        if ($this->backup !== null && $this->backup->remote_status === RemoteCopyStatus::Fetching) {
            $this->backup->forceFill(['remote_status' => RemoteCopyStatus::Uploaded, 'remote_error' => __('Fetching did not finish.')])->save();
        }
    }
}
