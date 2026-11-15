<?php

namespace App\Jobs;

use App\Models\AccountImport;
use App\Services\AccountImportService;
use App\Services\ServerBackupService;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Foundation\Queue\Queueable;
use Throwable;

/**
 * The import of one hosting account from a cPanel backup, on the backups
 * queue: reading a backup of many gigabytes through, then writing it again
 * as a RAYXO backup, can take hours.
 */
class ImportAccount implements ShouldQueue
{
    use Queueable;

    public int $tries = 1;

    public int $timeout = 2 * 6 * 3600 + 300;

    public bool $failOnTimeout = true;

    public bool $deleteWhenMissingModels = true;

    public function __construct(public AccountImport $import)
    {
        $this->onQueue(ServerBackupService::QUEUE);
    }

    public function handle(AccountImportService $imports): void
    {
        $imports->run($this->import);
    }

    public function failed(?Throwable $exception): void
    {
        app(AccountImportService::class)->fail($this->import, __('The import did not finish.'));
    }
}
