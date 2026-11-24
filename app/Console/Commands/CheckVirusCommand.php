<?php

namespace App\Console\Commands;

use App\Models\Setting;
use App\Services\NotificationService;
use App\Services\ServerSettingsService;
use App\Support\NotificationEvents;
use Illuminate\Console\Command;
use Throwable;

/**
 * Alert administrators when ClamAV quarantines a new infected file. Files that
 * were already reported are not alerted again, and restoring or deleting a file
 * does not raise a new alert.
 */
class CheckVirusCommand extends Command
{
    protected $signature = 'rayxo:check-virus';

    protected $description = 'Alert administrators when ClamAV quarantines a new infected file.';

    /** The quarantine ids administrators have already been told about. */
    public const KNOWN = 'notifications.virus_known_ids';

    public function handle(ServerSettingsService $settings, NotificationService $notifications): int
    {
        // Only meaningful while ClamAV is on; forget the state otherwise.
        if (! $settings->clamav()['enabled']) {
            Setting::query()->where('key', self::KNOWN)->delete();

            return self::SUCCESS;
        }

        try {
            $entries = $settings->quarantineList();
        } catch (Throwable) {
            // The agent is unreachable or busy; try again on the next run.
            return self::SUCCESS;
        }

        $currentIds = array_map(fn (array $entry): string => $entry['id'], $entries);

        $row = Setting::query()->firstWhere('key', self::KNOWN);
        $known = is_array($row?->value) ? $row->value : [];

        $newIds = array_values(array_diff($currentIds, $known));

        if ($newIds !== []) {
            $count = count($newIds);

            $notifications->toAdmins(
                NotificationEvents::VIRUS_FOUND,
                __('ClamAV quarantined an infected file'),
                __(':count file(s) were found infected and moved to quarantine. Review them on the Antivirus page.', ['count' => $count]),
                'virus:'.md5(implode("\n", $newIds)),
            );
        }

        Setting::query()->updateOrCreate(['key' => self::KNOWN], ['value' => $currentIds]);

        return self::SUCCESS;
    }
}
