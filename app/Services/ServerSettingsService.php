<?php

namespace App\Services;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Enums\Role;
use App\Enums\TaskStatus;
use App\Models\Setting;
use App\Models\Task;
use App\Models\User;
use App\Support\PhpSettings;
use Illuminate\Validation\ValidationException;

/**
 * The server-wide settings the admin changes in the panel: the timezone
 * and the hostname's certificate, which live on the server (the agent), and
 * the upload sizes websites may use, which live in the panel's database.
 */
class ServerSettingsService
{
    /**
     * The largest upload and POST size a website may choose.
     */
    public const PHP_UPLOAD_LIMIT = 'php.upload_limit';

    /**
     * The upload and POST size of websites that did not choose their own.
     */
    public const PHP_UPLOAD_DEFAULT = 'php.upload_default';

    /**
     * Whether the ionCube Loader is on, as the agent last reported it.
     */
    public const IONCUBE = 'php.ioncube';

    /**
     * How many recipients each hosting account may send mail to per 24
     * hours; null is no limit.
     */
    public const MAIL_SEND_LIMIT_ACCOUNT = 'mail.send_limit_account';

    /**
     * How many recipients all hosting accounts together may send mail to
     * per 24 hours; null is no limit.
     */
    public const MAIL_SEND_LIMIT_SERVER = 'mail.send_limit_server';

    /**
     * The limit of each account until the admin changes it.
     */
    public const DEFAULT_MAIL_SEND_LIMIT_ACCOUNT = 300;

    /**
     * The largest limit the agent takes.
     */
    public const MAX_MAIL_SEND_LIMIT = 10_000_000;

    /**
     * Which roles must have two-factor authentication to use the panel (a
     * JSON list of role values; only admins and resellers can be required).
     */
    public const REQUIRE_2FA_ROLES = 'security.require_2fa_roles';

    /**
     * The shortest password each role may set (a JSON object keyed by role
     * value), on top of the complexity rules every password already follows.
     */
    public const PASSWORD_MIN_LENGTH = 'security.password_min_length';

    /**
     * The shortest password allowed — the production default, which no role
     * may go below — and the longest minimum the admin may require.
     */
    public const DEFAULT_PASSWORD_MIN_LENGTH = 12;

    public const MAX_PASSWORD_MIN_LENGTH = 128;

    public function __construct(
        private AgentClient $agent,
        private AuditLogger $audit,
        private TaskDispatcher $tasks,
    ) {}

    /**
     * The largest upload and POST size websites may choose, such as "1024M".
     */
    public function phpUploadLimit(): string
    {
        $limit = $this->get(self::PHP_UPLOAD_LIMIT);

        return is_string($limit) && in_array($limit, PhpSettings::SIZES, true) ? $limit : PhpSettings::SIZES[array_key_last(PhpSettings::SIZES)];
    }

    /**
     * The upload and POST size of websites that did not choose their own.
     */
    public function phpUploadDefault(): string
    {
        $default = $this->get(self::PHP_UPLOAD_DEFAULT);

        return is_string($default) && in_array($default, PhpSettings::SIZES, true) ? $default : PhpSettings::DEFAULTS['upload_max_filesize'];
    }

    /**
     * The defaults a website's PHP settings start from on this server.
     *
     * @return array<string, string>
     */
    public function phpDefaults(): array
    {
        return PhpSettings::defaults($this->phpUploadDefault());
    }

    /**
     * Whether the ionCube Loader is on, for which PHP versions, and which
     * loader it is, as the agent last reported.
     *
     * @return array{enabled: bool, versions: list<string>, missing: list<string>, loader: string|null}
     */
    public function ionCube(): array
    {
        $saved = $this->get(self::IONCUBE);

        return $this->ionCubeState(is_array($saved) ? $saved : []);
    }

    /**
     * Turn the ionCube Loader on, which the agent downloads from ionCube,
     * or off, for every PHP version of the server.
     *
     * @return array{enabled: bool, versions: list<string>, missing: list<string>, loader: string|null}
     *
     * @throws ValidationException
     */
    public function setIonCube(bool $enabled, User $actor): array
    {
        try {
            $state = $this->ionCubeState($this->agent->call('php.ioncube', ['enabled' => $enabled], 900));
        } catch (AgentException $exception) {
            throw ValidationException::withMessages(['enabled' => __('The ionCube Loader could not be changed: :message', ['message' => $exception->getMessage()])]);
        }

        $this->put(self::IONCUBE, $state);
        $this->audit->record('server.ioncube_changed', null, ['enabled' => $state['enabled'], 'versions' => $state['versions'], 'loader' => $state['loader']], $actor);

        return $state;
    }

    /**
     * @param  array<mixed>  $state
     * @return array{enabled: bool, versions: list<string>, missing: list<string>, loader: string|null}
     */
    private function ionCubeState(array $state): array
    {
        $versions = fn (mixed $list): array => array_values(array_filter((array) $list, fn (mixed $version): bool => is_string($version) && preg_match('/^\d+\.\d+$/D', $version) === 1));

        return [
            'enabled' => ($state['enabled'] ?? false) === true,
            'versions' => $versions($state['versions'] ?? []),
            'missing' => $versions($state['missing'] ?? []),
            'loader' => is_string($state['loader'] ?? null) && $state['loader'] !== '' ? $state['loader'] : null,
        ];
    }

    /**
     * Store the upload sizes, both from {@see PhpSettings::SIZES}, the
     * default no larger than the limit (checked by the request).
     *
     * @return bool Whether anything changed, so websites must be applied again.
     */
    public function setPhpUploads(string $limit, string $default, User $actor): bool
    {
        $before = ['limit' => $this->phpUploadLimit(), 'default' => $this->phpUploadDefault()];
        $after = ['limit' => $limit, 'default' => $default];

        if ($before === $after) {
            return false;
        }

        $this->put(self::PHP_UPLOAD_LIMIT, $limit);
        $this->put(self::PHP_UPLOAD_DEFAULT, $default);

        $this->audit->record('server.php_uploads_changed', null, ['from' => $before, 'to' => $after], $actor);

        return true;
    }

    /**
     * The daily send limits, in recipients per 24 hours (null: no limit).
     * Each account has DEFAULT_MAIL_SEND_LIMIT_ACCOUNT until the admin sets
     * another limit or none; the server has none until the admin sets one.
     *
     * @return array{account: int|null, server: int|null}
     */
    public function mailSendLimits(): array
    {
        $account = Setting::query()->where('key', self::MAIL_SEND_LIMIT_ACCOUNT)->first();

        return [
            'account' => $account === null ? self::DEFAULT_MAIL_SEND_LIMIT_ACCOUNT : $this->limitOrNull($account->value),
            'server' => $this->limitOrNull($this->get(self::MAIL_SEND_LIMIT_SERVER)),
        ];
    }

    /**
     * Store the daily send limits (null: no limit).
     *
     * @return bool Whether anything changed, so the server must get them.
     */
    public function setMailSendLimits(?int $account, ?int $server, User $actor): bool
    {
        $before = $this->mailSendLimits();
        $after = ['account' => $account, 'server' => $server];

        if ($before === $after) {
            return false;
        }

        // No limit is stored as 0: a setting's value cannot be empty.
        $this->put(self::MAIL_SEND_LIMIT_ACCOUNT, $account ?? 0);
        $this->put(self::MAIL_SEND_LIMIT_SERVER, $server ?? 0);

        $this->audit->record('server.mail_send_limits_changed', null, ['from' => $before, 'to' => $after], $actor);

        return true;
    }

    /**
     * What the server reports: its hostname, timezone and the certificate
     * the panel serves.
     *
     * @return array<string, mixed>
     */
    public function serverState(): array
    {
        return $this->agent->call('system.settings', [], 20);
    }

    /**
     * Make timezone the server's (cron and logs follow it).
     */
    public function setTimezone(string $timezone, User $actor): void
    {
        $result = $this->agent->call('system.set_timezone', ['timezone' => $timezone], 60);

        $this->audit->record('server.timezone_changed', null, ['timezone' => $result['timezone'] ?? $timezone], $actor);
    }

    /**
     * Have the server take a new hostname: the agent runs setup.sh again in
     * the background, which first checks the name's A record and PTR and
     * changes nothing when they are wrong.
     */
    public function changeHostname(string $hostname, User $actor): void
    {
        $this->agent->call('system.change_hostname', ['hostname' => $hostname], 30);

        $this->audit->record('server.hostname_change_started', null, ['hostname' => $hostname], $actor);
    }

    /**
     * Open port for the panel as well, for a few minutes, so the admin can
     * check it is reachable before it becomes the panel's port.
     *
     * @return array<string, mixed> The trial: port, previous port and when it closes again.
     */
    public function tryPanelPort(int $port, User $actor): array
    {
        $trial = $this->agent->call('panel.port_trial', ['port' => $port], 30);

        $this->audit->record('server.panel_port_tried', null, ['port' => $port], $actor);

        return $trial;
    }

    /**
     * Make the port being tried the panel's: setup.sh writes every config
     * for it in the background.
     */
    public function confirmPanelPort(int $port, User $actor): void
    {
        $this->agent->call('panel.port_confirm', [], 30);

        $this->audit->record('server.panel_port_changed', null, ['port' => $port], $actor);
    }

    /**
     * Close the port being tried; the panel keeps its port.
     */
    public function cancelPanelPort(User $actor): void
    {
        $this->agent->call('panel.port_cancel', [], 30);

        $this->audit->record('server.panel_port_cancelled', null, [], $actor);
    }

    /**
     * Queue getting the hostname's Let's Encrypt certificate again for the
     * panel, phpMyAdmin and FTP.
     */
    public function requestHostnameCertificate(User $actor): Task
    {
        $task = $this->tasks->dispatch('ssl.issue_hostname', [], null, $actor);

        $this->audit->record('server.hostname_ssl_requested', null, [], $actor);

        return $task;
    }

    /**
     * Whether the latest hostname certificate request is still running, and
     * why it failed when it did.
     *
     * @return array{pending: bool, error: string|null}
     */
    public function hostnameCertificateRequest(): array
    {
        $task = Task::query()
            ->where('action', 'ssl.issue_hostname')
            ->latest('created_at')
            ->latest('id')
            ->first();

        return [
            'pending' => in_array($task?->status, [TaskStatus::Queued, TaskStatus::Running], true),
            'error' => $task?->status === TaskStatus::Failed ? ($task->error ?? '') : null,
        ];
    }

    /**
     * The security policy: which roles must have two-factor authentication,
     * and the shortest password each role may set.
     *
     * @return array{require_2fa: list<string>, password_min_length: array<string, int>}
     */
    public function securityPolicy(): array
    {
        return [
            'require_2fa' => $this->require2faRoles(),
            'password_min_length' => [
                Role::Admin->value => $this->passwordMinLength(Role::Admin),
                Role::Reseller->value => $this->passwordMinLength(Role::Reseller),
                Role::User->value => $this->passwordMinLength(Role::User),
            ],
        ];
    }

    /**
     * The roles that must have two-factor authentication. Only admins and
     * resellers can be required; anything else stored is ignored.
     *
     * @return list<string>
     */
    public function require2faRoles(): array
    {
        $stored = $this->get(self::REQUIRE_2FA_ROLES);
        $allowed = [Role::Admin->value, Role::Reseller->value];

        return is_array($stored) ? array_values(array_intersect($allowed, $stored)) : [];
    }

    public function requiresTwoFactor(Role $role): bool
    {
        return in_array($role->value, $this->require2faRoles(), true);
    }

    /**
     * The shortest password role may set — never below the production default,
     * so the policy can only make passwords stronger.
     */
    public function passwordMinLength(Role $role): int
    {
        $stored = $this->get(self::PASSWORD_MIN_LENGTH);
        $value = is_array($stored) ? ($stored[$role->value] ?? null) : null;

        return is_int($value)
            ? max(self::DEFAULT_PASSWORD_MIN_LENGTH, min(self::MAX_PASSWORD_MIN_LENGTH, $value))
            : self::DEFAULT_PASSWORD_MIN_LENGTH;
    }

    /**
     * Save which roles must have two-factor authentication and the shortest
     * password each role may set. Minimums are clamped so no role ever goes
     * below the production default.
     *
     * @param  list<string>  $require2fa
     * @param  array<string, int>  $minLengths
     */
    public function setSecurityPolicy(array $require2fa, array $minLengths, User $actor): void
    {
        $allowed = [Role::Admin->value, Role::Reseller->value];
        $require = array_values(array_intersect($allowed, array_map('strval', $require2fa)));

        $mins = [];
        foreach ([Role::Admin, Role::Reseller, Role::User] as $role) {
            $value = (int) ($minLengths[$role->value] ?? self::DEFAULT_PASSWORD_MIN_LENGTH);
            $mins[$role->value] = max(self::DEFAULT_PASSWORD_MIN_LENGTH, min(self::MAX_PASSWORD_MIN_LENGTH, $value));
        }

        $before = $this->securityPolicy();
        $this->put(self::REQUIRE_2FA_ROLES, $require);
        $this->put(self::PASSWORD_MIN_LENGTH, $mins);

        $this->audit->record('server.security_policy_changed', null, [
            'from' => $before,
            'to' => ['require_2fa' => $require, 'password_min_length' => $mins],
        ], $actor);
    }

    private function limitOrNull(mixed $value): ?int
    {
        return is_int($value) && $value >= 1 && $value <= self::MAX_MAIL_SEND_LIMIT ? $value : null;
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
