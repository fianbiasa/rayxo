<?php

namespace App\Services;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Enums\RemoteCopyStatus;
use App\Enums\ServerBackupStatus;
use App\Jobs\CopyBackupToRemote;
use App\Jobs\FetchBackupFromRemote;
use App\Models\ServerBackup;
use App\Models\Setting;
use App\Models\SystemBackup;
use App\Models\User;
use App\Rules\ValidUsername;
use App\Support\NotificationEvents;
use Illuminate\Contracts\Encryption\DecryptException;
use Illuminate\Support\Collection;
use Illuminate\Support\Facades\Crypt;
use Illuminate\Validation\ValidationException;

/**
 * The remote destination backups are copied to, so a lost server does not
 * take them along: an FTP or FTPS server, an SFTP server, or an S3 bucket,
 * reached by the agent through rclone. Each automatic backup of an
 * account and each backup of the panel is copied there once it is done;
 * the destination keeps the newest few of each account and of the panel,
 * and the server may keep none of them. Its password and keys are stored
 * encrypted and never go back to the browser.
 *
 * On the destination, this server's backups are in a folder named after
 * it, so several servers may share one destination, and a new server can
 * take back what an old one left there.
 */
class RemoteBackupService
{
    public const SETTING = 'backups.remote';

    public const TYPES = ['ftp', 'sftp', 's3'];

    public const MAX_KEEP = 90;

    /**
     * Settings that say where the copies are: changing one moves the
     * destination elsewhere.
     */
    public const LOCATION = ['type', 'host', 'port', 'user', 'endpoint', 'bucket', 'path'];

    /**
     * Settings that are stored encrypted, and never shown again.
     */
    public const SECRETS = ['password', 'secret_key'];

    /**
     * @var array{enabled: bool, type: string, host: string, port: int|null, user: string, password: string, tls: bool, host_key: string, endpoint: string, region: string, bucket: string, access_key: string, secret_key: string, path: string, keep: int, keep_local: bool}
     */
    public const DEFAULTS = [
        'enabled' => false,
        'type' => 'sftp',
        'host' => '',
        'port' => null,
        'user' => '',
        'password' => '',
        'tls' => true,
        'host_key' => '',
        'endpoint' => '',
        'region' => '',
        'bucket' => '',
        'access_key' => '',
        'secret_key' => '',
        'path' => '',
        'keep' => 7,
        'keep_local' => true,
    ];

    public function __construct(
        private AgentClient $agent,
        private AuditLogger $audit,
        private NotificationService $notifications,
    ) {}

    /**
     * The settings as stored, secrets still encrypted.
     *
     * @return array{enabled: bool, type: string, host: string, port: int|null, user: string, password: string, tls: bool, host_key: string, endpoint: string, region: string, bucket: string, access_key: string, secret_key: string, path: string, keep: int, keep_local: bool}
     */
    private function saved(): array
    {
        $saved = Setting::query()->where('key', self::SETTING)->value('value');

        /** @var array{enabled: bool, type: string, host: string, port: int|null, user: string, password: string, tls: bool, host_key: string, endpoint: string, region: string, bucket: string, access_key: string, secret_key: string, path: string, keep: int, keep_local: bool} */
        return array_replace(self::DEFAULTS, array_intersect_key(is_array($saved) ? $saved : [], self::DEFAULTS));
    }

    /**
     * The settings for the page: no secrets, only whether each is set.
     *
     * @return array<string, mixed>
     */
    public function settings(): array
    {
        $saved = $this->saved();

        return [
            ...array_diff_key($saved, array_flip(self::SECRETS)),
            'has_password' => $saved['password'] !== '',
            'has_secret_key' => $saved['secret_key'] !== '',
            'host_key_fingerprint' => $saved['host_key'] !== '' ? self::fingerprint($saved['host_key']) : null,
            'server' => $this->server(),
        ];
    }

    /**
     * Whether backups are copied to the destination.
     */
    public function enabled(): bool
    {
        return $this->saved()['enabled'];
    }

    /**
     * This server's folder on the destination: its hostname.
     */
    public function server(): string
    {
        return app(SystemBackupService::class)->hostname();
    }

    /**
     * Save the destination. A password or secret key left empty keeps the
     * one saved before; an SFTP server needs the host key the administrator
     * accepted.
     *
     * @param  array<string, mixed>  $values
     *
     * @throws ValidationException
     */
    public function save(array $values, User $actor): void
    {
        $before = $this->saved();
        $after = array_replace($before, array_intersect_key($values, self::DEFAULTS));

        foreach (self::SECRETS as $secret) {
            $after[$secret] = ($values[$secret] ?? '') !== ''
                ? Crypt::encryptString((string) $values[$secret])
                : $before[$secret];
        }

        if ($after['type'] !== 'sftp') {
            $after['host_key'] = '';
        }

        $missing = $after['type'] === 's3' ? 'secret_key' : 'password';

        if ($after[$missing] === '') {
            throw ValidationException::withMessages([$missing => $missing === 'secret_key'
                ? __('Enter the secret key of the bucket.')
                : __('Enter the password of the destination.')]);
        }

        Setting::query()->updateOrCreate(['key' => self::SETTING], ['value' => $after]);

        $changed = array_values(array_filter(array_keys(self::DEFAULTS), fn (string $key): bool => $before[$key] !== $after[$key]));

        if (array_intersect($changed, self::LOCATION) !== []) {
            $this->forgetCopies();
        }

        if ($changed !== []) {
            $this->audit->record('backup.remote_changed', null, [
                'changed' => $changed,
                'enabled' => $after['enabled'],
                'type' => $after['type'],
            ], $actor);
        }
    }

    /**
     * The key an SFTP server presents, for the administrator to accept.
     *
     * @return array{key: string, fingerprint: string}
     *
     * @throws ValidationException
     */
    public function hostKey(string $host, ?int $port): array
    {
        try {
            $key = $this->agent->call('backup.remote_hostkey', ['host' => $host, 'port' => $port ?? 0], 60);
        } catch (AgentException $exception) {
            throw ValidationException::withMessages(['host' => $exception->getMessage()]);
        }

        return ['key' => (string) ($key['key'] ?? ''), 'fingerprint' => (string) ($key['fingerprint'] ?? '')];
    }

    /**
     * Write and delete a file on the destination, as the saved settings
     * reach it.
     *
     * @throws ValidationException
     */
    public function test(): void
    {
        try {
            $this->agent->call('backup.remote_test', ['destination' => $this->destination(), 'server' => $this->server()], 300);
        } catch (AgentException $exception) {
            throw ValidationException::withMessages(['remote' => $exception->getMessage()]);
        }
    }

    /**
     * Queue the copy of a finished backup, when copies are on.
     */
    public function queueCopy(ServerBackup|SystemBackup $backup): void
    {
        if ($this->enabled() && $backup->status === ServerBackupStatus::Completed) {
            CopyBackupToRemote::dispatch($backup);
        }
    }

    /**
     * Copy a backup to the destination: the job queued once it was made.
     * Then the server's copy goes when it keeps none, and the oldest copies
     * on the destination beyond what it keeps. A failure tells the
     * administrators, and the server's copy stays.
     */
    public function copy(ServerBackup|SystemBackup $backup): void
    {
        $backup->refresh();

        if (! $this->enabled() || $backup->status !== ServerBackupStatus::Completed || ! $backup->on_server || $backup->remote_status === RemoteCopyStatus::Uploaded) {
            return;
        }

        $settings = $this->saved();
        $backup->forceFill(['remote_status' => RemoteCopyStatus::Uploading, 'remote_error' => null])->save();

        try {
            $this->agent->call('backup.remote_upload', $this->target($backup), ServerBackupService::BACKUP_SECONDS);
        } catch (AgentException $exception) {
            $this->failCopy($backup, $exception->getMessage());

            return;
        }

        $backup->forceFill(['remote_status' => RemoteCopyStatus::Uploaded, 'remote_error' => null, 'remote_at' => now()])->save();

        if (! $settings['keep_local']) {
            try {
                $this->agent->call($backup instanceof SystemBackup ? 'backup.system_delete' : 'backup.server_delete', ['names' => [$backup->name]], 60);
                $backup->forceFill(['on_server' => false])->save();
            } catch (AgentException $exception) {
                report($exception);
            }
        }

        $this->rotate($backup);
    }

    /**
     * Record a copy that failed, and tell the administrators.
     */
    public function failCopy(ServerBackup|SystemBackup $backup, string $message): void
    {
        $backup->forceFill(['remote_status' => RemoteCopyStatus::Failed, 'remote_error' => $message])->save();

        $this->notifications->toAdmins(
            NotificationEvents::BACKUP,
            __('A backup could not be copied to the remote destination'),
            $backup->name.': '.$message,
            'remote-copy-failed',
        );
    }

    /**
     * Delete the copies on the destination beyond the newest it keeps, of
     * the backup's account (its automatic backups) or of the panel. A
     * backup no longer on the server either is forgotten.
     */
    public function rotate(ServerBackup|SystemBackup $backup): void
    {
        $copies = fn () => ($backup instanceof SystemBackup
            ? SystemBackup::query()
            : ServerBackup::query()->whereNotNull('backup_run_id')->where('username', $backup->username))
            ->where('remote_status', RemoteCopyStatus::Uploaded);

        $kept = $copies()->orderByDesc('id')->limit($this->saved()['keep'])->pluck('id');
        $old = $copies()->whereNotIn('id', $kept)->get();

        foreach ($old as $copy) {
            try {
                $this->agent->call('backup.remote_delete', $this->target($copy), 300);
            } catch (AgentException $exception) {
                report($exception);

                continue;
            }

            $this->forgetRemote($copy);
        }
    }

    /**
     * Delete a backup's copy on the destination, when it has one: before
     * the backup itself is deleted. With copies off, the copy is only
     * forgotten.
     *
     * @throws ValidationException
     */
    public function deleteCopy(ServerBackup|SystemBackup $backup): void
    {
        if (! in_array($backup->remote_status, [RemoteCopyStatus::Uploaded, RemoteCopyStatus::Fetching], true) || ! $this->enabled()) {
            return;
        }

        try {
            $this->agent->call('backup.remote_delete', $this->target($backup), 300);
        } catch (AgentException $exception) {
            throw ValidationException::withMessages(['backup' => __('The copy on the remote destination could not be deleted: :message', ['message' => $exception->getMessage()])]);
        }
    }

    /**
     * Forget backups whose files the server deleted: those with a copy on
     * the destination stay, as copies there only.
     *
     * @param  Collection<int, ServerBackup>|Collection<int, SystemBackup>  $backups
     */
    public static function leftOnServer(Collection $backups): void
    {
        foreach ($backups as $backup) {
            if ($backup->remote_status === RemoteCopyStatus::Uploaded) {
                $backup->forceFill(['on_server' => false])->save();
            } else {
                $backup->delete();
            }
        }
    }

    /**
     * Queue bringing a backup that is only on the destination back to the
     * server, to restore or download it.
     *
     * @throws ValidationException
     */
    public function queueFetch(ServerBackup|SystemBackup $backup, User $actor): void
    {
        if ($backup->on_server) {
            throw ValidationException::withMessages(['backup' => __('This backup is on the server already.')]);
        }

        if ($backup->remote_status !== RemoteCopyStatus::Uploaded) {
            throw ValidationException::withMessages(['backup' => __('This backup has no copy on the remote destination.')]);
        }

        if (! $this->enabled()) {
            throw ValidationException::withMessages(['backup' => __('Turn the remote destination on first.')]);
        }

        $backup->forceFill(['remote_status' => RemoteCopyStatus::Fetching, 'remote_error' => null])->save();

        FetchBackupFromRemote::dispatch($backup, null, $actor);

        $this->audit->record('backup.remote_fetched', $backup instanceof ServerBackup ? $backup->account : null, ['name' => $backup->name], $actor);
    }

    /**
     * Take the newest backup of every active account back from the
     * destination, where it is there only: after the panel was brought back
     * on a new server, before restoring every account. Returns how many
     * were queued.
     *
     * @throws ValidationException
     */
    public function fetchNewest(User $actor): int
    {
        if (! $this->enabled()) {
            throw ValidationException::withMessages(['backup' => __('Turn the remote destination on first.')]);
        }

        $queued = 0;

        foreach (app(ServerBackupService::class)->accounts() as $account) {
            $newest = ServerBackup::query()
                ->where('user_id', $account->id)
                ->where('status', ServerBackupStatus::Completed)
                ->latest('id')
                ->first();

            if ($newest !== null && ! $newest->on_server && $newest->remote_status === RemoteCopyStatus::Uploaded) {
                $this->queueFetch($newest, $actor);
                $queued++;
            }
        }

        return $queued;
    }

    /**
     * Bring a backup back from the destination: the job queueFetch()
     * queued.
     */
    public function fetch(ServerBackup|SystemBackup $backup): void
    {
        $backup->refresh();

        if ($backup->on_server || $backup->remote_status !== RemoteCopyStatus::Fetching) {
            return;
        }

        try {
            $this->agent->call('backup.remote_download', $this->target($backup), ServerBackupService::BACKUP_SECONDS);
        } catch (AgentException $exception) {
            $backup->forceFill(['remote_status' => RemoteCopyStatus::Uploaded, 'remote_error' => $this->reason($exception)])->save();

            return;
        }

        $backup->forceFill(['on_server' => true, 'remote_status' => RemoteCopyStatus::Uploaded, 'remote_error' => null])->save();
    }

    /**
     * The servers' folders on the destination: this one's, and those of
     * other servers or of an old name of this one.
     *
     * @return list<string>
     *
     * @throws ValidationException
     */
    public function servers(): array
    {
        try {
            $servers = (array) ($this->agent->call('backup.remote_list', ['destination' => $this->destination(), 'server' => ''], 300)['servers'] ?? []);
        } catch (AgentException $exception) {
            throw ValidationException::withMessages(['remote' => $exception->getMessage()]);
        }

        return array_values(array_filter($servers, 'is_string'));
    }

    /**
     * The backups in a server's folder on the destination, and whether
     * each is known here already.
     *
     * @return array{server: string, accounts: list<array{username: string, name: string, size: int, modified: int, known: bool}>, system: list<array{name: string, size: int, modified: int, known: bool}>}
     *
     * @throws ValidationException
     */
    public function listing(string $server): array
    {
        try {
            $listed = $this->agent->call('backup.remote_list', ['destination' => $this->destination(), 'server' => $server], 300);
        } catch (AgentException $exception) {
            throw ValidationException::withMessages(['remote' => $exception->getMessage()]);
        }

        $accounts = collect((array) ($listed['accounts'] ?? []))->filter(fn (mixed $file): bool => is_array($file) && is_string($file['name'] ?? null) && is_string($file['username'] ?? null));
        $system = collect((array) ($listed['system'] ?? []))->filter(fn (mixed $file): bool => is_array($file) && is_string($file['name'] ?? null));
        $knownAccounts = ServerBackup::query()->whereIn('name', $accounts->pluck('name'))->pluck('name')->all();
        $knownSystem = SystemBackup::query()->whereIn('name', $system->pluck('name'))->pluck('name')->all();

        return [
            'server' => $server,
            'accounts' => array_values($accounts->map(fn (array $file): array => [
                'username' => (string) $file['username'],
                'name' => (string) $file['name'],
                'size' => (int) ($file['size'] ?? 0),
                'modified' => (int) ($file['modified'] ?? 0),
                'known' => in_array($file['name'], $knownAccounts, true),
            ])->sortByDesc('modified')->all()),
            'system' => array_values($system->map(fn (array $file): array => [
                'name' => (string) $file['name'],
                'size' => (int) ($file['size'] ?? 0),
                'modified' => (int) ($file['modified'] ?? 0),
                'known' => in_array($file['name'], $knownSystem, true),
            ])->sortByDesc('modified')->all()),
        ];
    }

    /**
     * Take a backup this panel does not know from a server's folder on the
     * destination: such as after the panel was brought back on a new
     * server. It is downloaded on the backups queue, then known like one
     * made here. Only a backup in this server's own folder counts as having
     * its copy there.
     *
     * @throws ValidationException
     */
    public function queueTake(string $server, string $kind, ?string $username, string $name, User $actor): void
    {
        if (! $this->enabled()) {
            throw ValidationException::withMessages(['backup' => __('Turn the remote destination on first.')]);
        }

        $known = $kind === 'system'
            ? SystemBackup::query()->where('name', $name)->exists()
            : ServerBackup::query()->where('name', $name)->exists();

        if ($known) {
            throw ValidationException::withMessages(['backup' => __('This backup is known here already.')]);
        }

        if ($kind === 'account' && ($username === null || preg_match(ValidUsername::PATTERN, $username) !== 1)) {
            throw ValidationException::withMessages(['backup' => __('This is not a backup of an account.')]);
        }

        FetchBackupFromRemote::dispatch(null, ['server' => $server, 'kind' => $kind, 'username' => $kind === 'account' ? (string) $username : '', 'name' => $name], $actor);

        $this->audit->record('backup.remote_fetched', null, ['name' => $name, 'server' => $server], $actor);
    }

    /**
     * Download a backup this panel does not know, and remember it: the job
     * queueTake() queued.
     *
     * @param  array{server: string, kind: string, username: string, name: string}  $file
     */
    public function take(array $file, User $actor): void
    {
        $ours = $file['server'] === $this->server();

        try {
            $result = $this->agent->call('backup.remote_download', [
                'destination' => $this->destination(),
                'server' => $file['server'],
                'kind' => $file['kind'],
                'username' => $file['username'],
                'name' => $file['name'],
            ], ServerBackupService::BACKUP_SECONDS);
        } catch (AgentException $exception) {
            $this->notifications->toAdmins(NotificationEvents::BACKUP, __('A backup could not be taken from the remote destination'), $file['name'].': '.$this->reason($exception), 'remote-take-failed:'.$file['name']);

            return;
        }

        $remote = $ours ? ['remote_status' => RemoteCopyStatus::Uploaded, 'remote_at' => now()] : [];

        if ($file['kind'] === 'system') {
            $backup = new SystemBackup;
            $backup->forceFill(['name' => $file['name'], 'status' => ServerBackupStatus::Completed, 'size_bytes' => (int) ($result['size'] ?? 0), 'finished_at' => now(), ...$remote])->save();

            return;
        }

        try {
            $backup = app(ServerBackupService::class)->register($file['name'], (int) ($result['size'] ?? 0), $result, $actor);
            $backup->forceFill($remote)->save();
        } catch (ValidationException $exception) {
            report($exception);
        }
    }

    /**
     * What the agent needs to reach the destination, secrets decrypted.
     *
     * @return array<string, mixed>
     */
    public function destination(): array
    {
        $saved = $this->saved();
        $secret = function (string $value): string {
            try {
                return $value !== '' ? Crypt::decryptString($value) : '';
            } catch (DecryptException) {
                return '';
            }
        };

        return [
            'type' => $saved['type'],
            'host' => $saved['host'],
            'port' => (int) ($saved['port'] ?? 0),
            'user' => $saved['user'],
            'password' => $secret($saved['password']),
            'tls' => $saved['tls'],
            'host_key' => $saved['host_key'],
            'endpoint' => $saved['endpoint'],
            'region' => $saved['region'],
            'bucket' => $saved['bucket'],
            'access_key' => $saved['access_key'],
            'secret_key' => $secret($saved['secret_key']),
            'path' => $saved['path'],
        ];
    }

    /**
     * The agent's params naming a backup on the destination.
     *
     * @return array<string, mixed>
     */
    private function target(ServerBackup|SystemBackup $backup): array
    {
        return [
            'destination' => $this->destination(),
            'server' => $this->server(),
            'kind' => $backup instanceof SystemBackup ? 'system' : 'account',
            'username' => $backup instanceof SystemBackup ? '' : $backup->username,
            'name' => $backup->name,
        ];
    }

    /**
     * Forget every copy on the old destination once it moved elsewhere: the
     * panel no longer reaches them, and leaves those files as they are.
     * Backups whose only copy was there are forgotten with them.
     */
    private function forgetCopies(): void
    {
        foreach ([ServerBackup::class, SystemBackup::class] as $model) {
            $model::query()->whereNotNull('remote_status')->where('on_server', false)->delete();
            $model::query()->whereNotNull('remote_status')->update(['remote_status' => null, 'remote_error' => null, 'remote_at' => null]);
        }
    }

    /**
     * Forget a backup's copy on the destination; a backup no longer on the
     * server either is forgotten itself.
     */
    private function forgetRemote(ServerBackup|SystemBackup $backup): void
    {
        if ($backup->on_server) {
            $backup->forceFill(['remote_status' => null, 'remote_at' => null, 'remote_error' => null])->save();
        } else {
            $backup->delete();
        }
    }

    /**
     * What an agent error about a remote copy means for the administrator.
     */
    private function reason(AgentException $exception): string
    {
        return $exception->reason === 'no_space'
            ? __('The server disk does not have enough free space for this backup.')
            : $exception->getMessage();
    }

    /**
     * The SHA256 fingerprint of a host key (the first of several, the
     * strongest), as ssh shows it.
     */
    public static function fingerprint(string $key): string
    {
        $blob = base64_decode(explode(' ', strtok($key, "\n") ?: '')[1] ?? '', true);

        return $blob === false ? '' : 'SHA256:'.rtrim(base64_encode(hash('sha256', $blob, true)), '=');
    }
}
