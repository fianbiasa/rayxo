<?php

namespace App\Services;

use App\Models\ConfigDrift;
use App\Models\Setting;
use App\Models\Task;
use App\Models\User;
use Illuminate\Support\Carbon;
use Illuminate\Support\Collection;

/**
 * Config drift detection. A scan queues a "check" of every agent-managed
 * config (render + compare on the server, no write); the agent reports which
 * files differ, and RecordConfigDrift stores the drifting ones in
 * config_drifts. The admin fixes one with "rewrite", which re-applies the
 * same config. Nothing is changed by a scan.
 */
class DriftService
{
    /** When the last scan was started. */
    public const LAST_SCAN = 'drift.last_scan_at';

    /** Maps each check action to the apply action that rewrites its target. */
    public const APPLY_ACTIONS = [
        'web.check_domain' => 'web.apply_domain',
        'dns.check_zone' => 'dns.sync_zone',
        'mail.check_sieve' => 'mail.sieve_apply',
        'waf.check' => 'waf.apply',
    ];

    public function __construct(
        private TaskDispatcher $tasks,
        private DomainService $domains,
        private DnsService $dns,
        private MailRulesService $mailRules,
        private ServerSettingsService $settings,
        private AuditLogger $audit,
    ) {}

    /**
     * Queue a drift check of every agent-managed config. Each check renders
     * the expected content and compares it to disk without writing; results
     * come back as tasks that RecordConfigDrift stores.
     *
     * @return int The number of checks queued.
     */
    public function scan(?User $actor = null): int
    {
        $queued = 0;

        // Server-wide ModSecurity engine.
        $this->tasks->dispatch('waf.check', $this->settings->waf(), null, $actor);
        $queued++;

        $queued += $this->domains->checkAll($actor);
        $queued += $this->dns->checkAll($actor);
        $queued += $this->mailRules->checkAll($actor);

        Setting::query()->updateOrCreate(['key' => self::LAST_SCAN], ['value' => now()->toIso8601ZuluString('microsecond')]);
        $this->audit->record('server.drift_scanned', null, ['checks' => $queued], $actor);

        return $queued;
    }

    /**
     * Record a finished check task's result: keep a row for every target that
     * drifted, and clear the row of any target that matches again.
     */
    public function recordResult(Task $task): void
    {
        $applyAction = self::APPLY_ACTIONS[$task->action] ?? null;
        if ($applyAction === null) {
            return;
        }

        $targets = is_array($task->output['targets'] ?? null) ? $task->output['targets'] : [];

        foreach ($targets as $target) {
            if (! is_array($target) || ! isset($target['path'])) {
                continue;
            }

            $path = (string) $target['path'];

            if (($target['drifted'] ?? false) !== true) {
                ConfigDrift::query()->where('path', $path)->delete();

                continue;
            }

            ConfigDrift::query()->updateOrCreate(['path' => $path], [
                'user_id' => $task->user_id,
                'kind' => (string) ($target['kind'] ?? 'config'),
                'apply_action' => $applyAction,
                'payload' => $task->payload ?? [],
                'missing' => ($target['missing'] ?? false) === true,
                'diff' => isset($target['diff']) ? (string) $target['diff'] : null,
                'checked_at' => now(),
            ]);
        }
    }

    /**
     * Every target currently drifting, newest check first.
     *
     * @return Collection<int, ConfigDrift>
     */
    public function drifted(): Collection
    {
        return ConfigDrift::query()->orderBy('kind')->orderBy('path')->get();
    }

    /**
     * When the last scan was started, or null if none has run.
     */
    public function lastScan(): ?Carbon
    {
        $value = Setting::query()->where('key', self::LAST_SCAN)->value('value');

        return is_string($value) ? Carbon::parse($value) : null;
    }

    /**
     * Re-apply a drifted target: dispatch the apply action with the payload
     * the check used, so the file is written back to what RAYXO wants. The row
     * clears when the next scan sees the target matching again.
     */
    public function rewrite(ConfigDrift $drift, User $actor): void
    {
        $account = $drift->user_id !== null ? User::query()->find($drift->user_id) : null;

        $this->tasks->dispatch($drift->apply_action, $drift->payload, $account, $actor);

        $this->audit->record('server.drift_rewritten', null, ['kind' => $drift->kind, 'path' => $drift->path], $actor);
    }
}
