<?php

namespace App\Console\Commands;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Models\Setting;
use App\Services\NotificationService;
use App\Services\UpdateService;
use App\Support\NotificationEvents;
use Illuminate\Console\Command;

/**
 * Tells administrators when a newer RAYXO release is out on the chosen update
 * channel, so they can update from Server Settings. The scheduler runs it
 * daily; each new version is announced once.
 */
class CheckUpdatesCommand extends Command
{
    /**
     * @var string
     */
    protected $signature = 'rayxo:check-updates';

    /**
     * @var string
     */
    protected $description = 'Notify administrators when a newer RAYXO release is available';

    public function handle(AgentClient $agent, NotificationService $notifications, UpdateService $updates): int
    {
        try {
            $installed = (string) ($agent->call('system.info', [], 30)['agent_version'] ?? '');
        } catch (AgentException $exception) {
            report($exception);
            $this->components->error('The server agent did not answer: '.$exception->getMessage());

            return self::SUCCESS;
        }

        if ($installed === '') {
            return self::SUCCESS;
        }

        $target = $updates->available($installed, $updates->channel());

        if ($target === null) {
            return self::SUCCESS;
        }

        // Announce each version once, even though the check runs daily.
        if (Setting::query()->where('key', UpdateService::LAST_NOTIFIED)->value('value') === $target['version']) {
            return self::SUCCESS;
        }

        $notifications->toAdmins(
            NotificationEvents::UPDATE_AVAILABLE,
            __('RAYXO :version is available', ['version' => $target['version']]),
            __('You are on :installed. Update to :version from Server Settings in the panel.', [
                'installed' => $installed,
                'version' => $target['version'],
            ]),
            'update-available:'.$target['version'],
        );

        Setting::query()->updateOrCreate(['key' => UpdateService::LAST_NOTIFIED], ['value' => $target['version']]);

        return self::SUCCESS;
    }
}
