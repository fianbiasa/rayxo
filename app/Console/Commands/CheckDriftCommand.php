<?php

namespace App\Console\Commands;

use App\Models\Setting;
use App\Services\DriftService;
use App\Services\NotificationService;
use App\Support\NotificationEvents;
use Illuminate\Console\Command;

/**
 * Alert administrators when agent-managed config has drifted from what RAYXO
 * would write, then queue a fresh scan. Drift that is already known is not
 * alerted again until it changes.
 */
class CheckDriftCommand extends Command
{
    protected $signature = 'rayxo:check-drift';

    protected $description = 'Check agent-managed config for drift and alert administrators.';

    /** The signature of the drift admins were last told about (dedupe). */
    public const LAST_NOTIFIED = 'notifications.drift_notified_signature';

    public function handle(DriftService $drift, NotificationService $notifications): int
    {
        $drifted = $drift->drifted();

        if ($drifted->isNotEmpty()) {
            $signature = md5($drifted->pluck('path')->sort()->implode("\n"));

            if (Setting::query()->where('key', self::LAST_NOTIFIED)->value('value') !== $signature) {
                $count = $drifted->count();

                $notifications->toAdmins(
                    NotificationEvents::CONFIG_DRIFTED,
                    __('Server config has drifted'),
                    __(':count config file(s) differ from what RAYXO would write. Review them on the Services page.', ['count' => $count]),
                    'config-drift:'.$signature,
                );

                Setting::query()->updateOrCreate(['key' => self::LAST_NOTIFIED], ['value' => $signature]);
            }
        } else {
            // Nothing drifts now: forget the signature so new drift alerts again.
            Setting::query()->where('key', self::LAST_NOTIFIED)->delete();
        }

        // Queue a fresh check so the next run sees the current state.
        $drift->scan();

        return self::SUCCESS;
    }
}
