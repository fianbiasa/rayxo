<?php

namespace App\Services;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Enums\BackupRunStatus;
use App\Enums\TaskStatus;
use App\Models\BackupRun;
use App\Models\Setting;
use App\Models\Task;
use App\Models\User;
use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Illuminate\Validation\ValidationException;

/**
 * One-click panel update: reads the installed version from the agent, checks
 * GitHub for newer releases on a stable or beta channel, and has the agent
 * run the chosen release's setup.sh. The update is refused while a backup or
 * other task is running, so an upgrade never restarts the agent mid-work.
 */
class UpdateService
{
    /** The update channel the admin chose: "stable" or "beta". */
    public const CHANNEL = 'update.channel';

    /** The newest version an admin was already told about (dedupe). */
    public const LAST_NOTIFIED = 'notifications.update_notified_version';

    /** The channels the admin may choose. */
    public const CHANNELS = ['stable', 'beta'];

    /** How long a GitHub release lookup is cached. */
    public const CACHE_SECONDS = 3600;

    private const CACHE_KEY = 'update:releases';

    public function __construct(
        private AgentClient $agent,
        private AuditLogger $audit,
    ) {}

    /**
     * The published releases of the configured repository, newest first, each
     * as {version, tag, prerelease, setup_url, notes_url}. Cached for an hour;
     * a GitHub outage returns an empty list rather than an error.
     *
     * @return list<array{version: string, tag: string, prerelease: bool, setup_url: string, notes_url: string}>
     */
    public function releases(bool $fresh = false): array
    {
        if ($fresh) {
            Cache::forget(self::CACHE_KEY);
        }

        /** @var list<array{version: string, tag: string, prerelease: bool, setup_url: string, notes_url: string}> */
        return Cache::remember(self::CACHE_KEY, self::CACHE_SECONDS, fn (): array => $this->fetchReleases());
    }

    /**
     * The channel the admin chose, or the configured default.
     */
    public function channel(): string
    {
        $stored = $this->get(self::CHANNEL);
        $default = is_string(config('hosting.release.channel')) ? config('hosting.release.channel') : 'stable';

        return in_array($stored, self::CHANNELS, true) ? $stored : (in_array($default, self::CHANNELS, true) ? $default : 'stable');
    }

    /**
     * Store the update channel.
     *
     * @throws ValidationException
     */
    public function setChannel(string $channel, User $actor): void
    {
        if (! in_array($channel, self::CHANNELS, true)) {
            throw ValidationException::withMessages(['channel' => __('Choose a valid update channel.')]);
        }

        $this->put(self::CHANNEL, $channel);
        $this->audit->record('server.update_channel_changed', null, ['channel' => $channel], $actor);
    }

    /**
     * The newest release on channel: the newest full release for "stable", or
     * the newest release including pre-releases for "beta".
     *
     * @return array{version: string, tag: string, prerelease: bool, setup_url: string, notes_url: string}|null
     */
    public function target(string $channel): ?array
    {
        $best = null;

        foreach ($this->releases() as $release) {
            if ($channel !== 'beta' && $release['prerelease']) {
                continue;
            }

            if ($best === null || $this->isNewer($release['version'], $best['version'])) {
                $best = $release;
            }
        }

        return $best;
    }

    /**
     * The release the panel offers to update to: the channel's newest, only
     * when it is newer than the installed version.
     *
     * @return array{version: string, tag: string, prerelease: bool, setup_url: string, notes_url: string}|null
     */
    public function available(string $installed, string $channel): ?array
    {
        $target = $this->target($channel);

        if ($target === null || ! $this->isNewer($target['version'], $installed)) {
            return null;
        }

        return $target;
    }

    /**
     * Everything the Server Settings page shows about updates: the installed
     * version, the chosen channel, the newest release per channel, and the
     * release an update would install (if any).
     *
     * @return array{installed: string, channel: string, stable: array<string, mixed>|null, beta: array<string, mixed>|null, available: array<string, mixed>|null}
     */
    public function info(string $installed): array
    {
        $channel = $this->channel();

        return [
            'installed' => $installed,
            'channel' => $channel,
            'stable' => $this->target('stable'),
            'beta' => $this->target('beta'),
            'available' => $this->available($installed, $channel),
        ];
    }

    /**
     * Start updating the panel to the newest release on the chosen channel.
     * Refuses while a backup or other task is running, or when no newer
     * release is available.
     *
     * @return array{version: string}
     *
     * @throws ValidationException
     */
    public function start(string $installed, User $actor): array
    {
        if ($this->busy()) {
            throw ValidationException::withMessages(['update' => __('A backup or task is running. Wait for it to finish before updating.')]);
        }

        $target = $this->available($installed, $this->channel());

        if ($target === null) {
            throw ValidationException::withMessages(['update' => __('There is no newer release to update to.')]);
        }

        try {
            $this->agent->call('system.update', ['version' => $target['version'], 'setup_url' => $target['setup_url']], 60);
        } catch (AgentException $exception) {
            throw ValidationException::withMessages(['update' => __('The update could not be started: :message', ['message' => $exception->getMessage()])]);
        }

        $this->audit->record('server.update_started', null, ['version' => $target['version']], $actor);

        return ['version' => $target['version']];
    }

    /**
     * Whether a newer version than installed exists, using PHP's version
     * comparison (which orders pre-releases below the matching full release).
     */
    public function isNewer(string $candidate, string $installed): bool
    {
        return version_compare($this->normalize($candidate), $this->normalize($installed), '>');
    }

    /**
     * Whether a backup round, a queued backup job, or any agent task is
     * running — an update restarts the agent and its workers, so it must wait.
     */
    private function busy(): bool
    {
        if (Task::query()->whereIn('status', [TaskStatus::Queued, TaskStatus::Running])->exists()) {
            return true;
        }

        if (BackupRun::query()->where('status', BackupRunStatus::Running)->exists()) {
            return true;
        }

        return DB::table('jobs')->where('queue', ServerBackupService::QUEUE)->exists();
    }

    /**
     * Fetch and shape the repository's releases from the GitHub API.
     *
     * @return list<array{version: string, tag: string, prerelease: bool, setup_url: string, notes_url: string}>
     */
    private function fetchReleases(): array
    {
        $repo = is_string(config('hosting.release.repo')) ? config('hosting.release.repo') : 'fianbiasa/rayxo';

        try {
            $response = Http::timeout(15)
                ->withHeaders(['Accept' => 'application/vnd.github+json', 'X-GitHub-Api-Version' => '2022-11-28'])
                ->withUserAgent('rayxo-panel')
                ->get("https://api.github.com/repos/{$repo}/releases", ['per_page' => 100]);
        } catch (ConnectionException) {
            return [];
        }

        if (! $response->successful() || ! is_array($response->json())) {
            return [];
        }

        $releases = [];

        foreach ($response->json() as $release) {
            if (! is_array($release) || ($release['draft'] ?? false) === true || empty($release['published_at']) || empty($release['tag_name'])) {
                continue;
            }

            $tag = (string) $release['tag_name'];

            $releases[] = [
                'version' => ltrim($tag, 'v'),
                'tag' => $tag,
                'prerelease' => ($release['prerelease'] ?? false) === true,
                'setup_url' => "https://github.com/{$repo}/releases/download/{$tag}/setup.sh",
                'notes_url' => is_string($release['html_url'] ?? null) ? $release['html_url'] : '',
            ];
        }

        return $releases;
    }

    /**
     * Normalise a version for comparison: drop a leading "v".
     */
    private function normalize(string $version): string
    {
        return ltrim(trim($version), 'v');
    }

    private function get(string $key): mixed
    {
        return Setting::query()->where('key', $key)->value('value');
    }

    private function put(string $key, mixed $value): void
    {
        Setting::query()->updateOrCreate(['key' => $key], ['value' => $value]);
    }
}
