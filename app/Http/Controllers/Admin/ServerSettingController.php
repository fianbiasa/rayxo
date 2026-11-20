<?php

namespace App\Http\Controllers\Admin;

use App\Agent\AgentException;
use App\Http\Controllers\Controller;
use App\Http\Requests\Admin\TryPanelPortRequest;
use App\Http\Requests\Admin\UpdateMailSendLimitsRequest;
use App\Http\Requests\Admin\UpdateNameserversRequest;
use App\Http\Requests\Admin\UpdatePhpUploadLimitsRequest;
use App\Http\Requests\Admin\UpdateSecurityPolicyRequest;
use App\Http\Requests\Admin\UpdateServerHostnameRequest;
use App\Http\Requests\Admin\UpdateServerTimezoneRequest;
use App\Services\AuditLogger;
use App\Services\BandwidthService;
use App\Services\DatabaseToolsService;
use App\Services\DnsService;
use App\Services\DomainService;
use App\Services\MailSendLimitService;
use App\Services\ServerSettingsService;
use App\Services\TelegramBot;
use App\Services\UpdateService;
use App\Support\PhpSettings;
use DateTimeZone;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Validation\Rule;
use Illuminate\Validation\ValidationException;
use Inertia\Inertia;
use Inertia\Response;

/**
 * The server's own settings: its hostname (which can be changed) and the
 * certificate the panel, phpMyAdmin and FTP serve, its timezone, and the
 * upload sizes websites may use.
 */
class ServerSettingController extends Controller
{
    public function __construct(
        private ServerSettingsService $settings,
        private DomainService $domains,
        private DnsService $dns,
        private MailSendLimitService $sendLimits,
        private BandwidthService $bandwidth,
        private DatabaseToolsService $databaseTools,
        private AuditLogger $audit,
        private TelegramBot $telegram,
        private UpdateService $updates,
    ) {}

    public function index(): Response
    {
        $server = null;
        $error = null;

        try {
            $server = $this->settings->serverState();
        } catch (AgentException $exception) {
            report($exception);
            $error = __('The agent did not answer. Check that rayxod is running: systemctl status rayxod');
        }

        return Inertia::render('admin/server-settings/index', [
            'server' => $server,
            'error' => $error,
            'certificate_request' => $this->settings->hostnameCertificateRequest(),
            'timezones' => DateTimeZone::listIdentifiers(),
            'php_uploads' => [
                'limit' => $this->settings->phpUploadLimit(),
                'default' => $this->settings->phpUploadDefault(),
                'sizes' => PhpSettings::SIZES,
            ],
            'nameservers' => $this->dns->nameservers(),
            'public_ip' => config('hosting.public_ip') ?: null,
            'bandwidth' => [
                'suspend_over_limit' => $this->bandwidth->suspendsOverLimit(),
            ],
            'database_remote_access' => $this->databaseTools->remoteAccessEnabled(),
            'ioncube' => $this->settings->ionCube(),
            'clamav' => $this->settings->clamav(),
            'waf' => $this->settings->waf(),
            'update' => $this->updates->info(is_array($server) && is_string($server['version'] ?? null) ? $server['version'] : ''),
            'telegram_bot' => $this->telegram->username(),
            'mail_limits' => [
                ...$this->settings->mailSendLimits(),
                'default_account' => ServerSettingsService::DEFAULT_MAIL_SEND_LIMIT_ACCOUNT,
            ],
            'security_policy' => [
                ...$this->settings->securityPolicy(),
                'default_min' => ServerSettingsService::DEFAULT_PASSWORD_MIN_LENGTH,
                'max_min' => ServerSettingsService::MAX_PASSWORD_MIN_LENGTH,
            ],
        ]);
    }

    /**
     * Set which roles must have two-factor authentication and the shortest
     * password each role may set.
     */
    public function updateSecurityPolicy(UpdateSecurityPolicyRequest $request): RedirectResponse
    {
        $this->settings->setSecurityPolicy(
            $request->array('require_2fa'),
            $request->array('password_min_length'),
            $request->user(),
        );

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Security policy saved.')]);

        return back();
    }

    /**
     * Change the nameservers every DNS zone names, and write every zone
     * again.
     */
    public function updateNameservers(UpdateNameserversRequest $request): RedirectResponse
    {
        $nameservers = array_values(array_filter([
            $request->string('ns1')->toString(),
            $request->filled('ns2') ? $request->string('ns2')->toString() : null,
        ]));

        $queued = $this->dns->setNameservers($nameservers, $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The nameservers are saved; :count DNS zones are being rewritten.', ['count' => $queued])]);

        return back();
    }

    /**
     * Change the server's timezone.
     *
     * @throws ValidationException
     */
    public function updateTimezone(UpdateServerTimezoneRequest $request): RedirectResponse
    {
        $timezone = $request->string('timezone')->toString();

        try {
            $this->settings->setTimezone($timezone, $request->user());
        } catch (AgentException $exception) {
            report($exception);

            throw ValidationException::withMessages(['timezone' => __('The server could not change its timezone: :detail', ['detail' => $exception->getMessage()])]);
        }

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The server now runs on :timezone time.', ['timezone' => $timezone])]);

        return back();
    }

    /**
     * Give the server a new hostname, in the background.
     *
     * @throws ValidationException
     */
    public function updateHostname(UpdateServerHostnameRequest $request): RedirectResponse
    {
        $hostname = $request->string('hostname')->toString();

        try {
            $this->settings->changeHostname($hostname, $request->user());
        } catch (AgentException $exception) {
            if ($exception->reason === null) {
                report($exception);
            }

            throw ValidationException::withMessages(['hostname' => $exception->reason === 'busy'
                ? __('The hostname is already being changed.')
                : __('The hostname cannot be changed: :detail', ['detail' => $exception->getMessage()])]);
        }

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Changing the hostname to :hostname. This takes a few minutes; follow it on this page.', ['hostname' => $hostname])]);

        return back();
    }

    /**
     * Open a port for the panel next to its own, to be confirmed through it.
     *
     * @throws ValidationException
     */
    public function tryPort(TryPanelPortRequest $request): RedirectResponse
    {
        $port = $request->integer('port');

        try {
            $this->settings->tryPanelPort($port, $request->user());
        } catch (AgentException $exception) {
            throw $this->portError($exception);
        }

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Port :port is open for 5 minutes. Open the panel through it to confirm.', ['port' => $port])]);

        return back();
    }

    /**
     * Make the port being tried the panel's. Only a request that came in
     * through that port may, since it proves the port can be reached.
     *
     * @throws ValidationException
     */
    public function confirmPort(Request $request): RedirectResponse
    {
        $trial = $this->portTrial();

        if ($trial === null) {
            throw ValidationException::withMessages(['port' => __('No port is being tried.')]);
        }

        if ($request->getPort() !== $trial['port']) {
            throw ValidationException::withMessages(['port' => __('Open the panel through port :port to confirm it: that shows the port can be reached.', ['port' => $trial['port']])]);
        }

        try {
            $this->settings->confirmPanelPort($trial['port'], $request->user());
        } catch (AgentException $exception) {
            throw $this->portError($exception);
        }

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Moving the panel to port :port. This takes a minute; follow it on this page.', ['port' => $trial['port']])]);

        return back();
    }

    /**
     * Close the port being tried.
     *
     * @throws ValidationException
     */
    public function cancelPort(Request $request): RedirectResponse
    {
        try {
            $this->settings->cancelPanelPort($request->user());
        } catch (AgentException $exception) {
            throw $this->portError($exception);
        }

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The port tried is closed again.')]);

        return back();
    }

    /**
     * Get the hostname's Let's Encrypt certificate again, in the background.
     *
     * @throws ValidationException
     */
    public function issueHostnameCertificate(Request $request): RedirectResponse
    {
        if ($this->settings->hostnameCertificateRequest()['pending']) {
            throw ValidationException::withMessages(['certificate' => __('The certificate is already being requested.')]);
        }

        $this->settings->requestHostnameCertificate($request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The hostname certificate is being requested. This takes up to a minute.')]);

        return back();
    }

    /**
     * Change the daily send limits of hosting accounts (empty: no limit);
     * the mail server gets them at once.
     */
    public function updateMailSendLimits(UpdateMailSendLimitsRequest $request): RedirectResponse
    {
        $changed = $this->settings->setMailSendLimits(
            $request->filled('account') ? $request->integer('account') : null,
            $request->filled('server') ? $request->integer('server') : null,
            $request->user(),
        );

        if ($changed) {
            $this->sendLimits->sync($request->user());
        }

        Inertia::flash('toast', ['type' => 'success', 'message' => $changed ? __('Send limits saved.') : __('Nothing changed.')]);

        return back();
    }

    /**
     * Turn suspending hosting accounts that go over their package's
     * monthly bandwidth on or off.
     */
    public function updateBandwidth(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'suspend_over_limit' => ['required', 'boolean'],
        ]);

        $this->bandwidth->setSuspendOverLimit((bool) $validated['suspend_over_limit'], $request->user(), $this->audit);

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Bandwidth setting saved.')]);

        return back();
    }

    /**
     * Let MariaDB answer from outside, for the hosts accounts allow, or only
     * on the server. MariaDB restarts.
     */
    /**
     * Use a Telegram bot for alerts; Telegram has to accept its token.
     */
    public function updateTelegram(Request $request): RedirectResponse
    {
        $validated = $request->validate(['token' => ['required', 'string', 'max:100']]);

        $username = $this->telegram->setToken(trim($validated['token']), $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Alerts are sent by @:bot.', ['bot' => (string) $username])]);

        return back();
    }

    /**
     * Stop sending alerts by Telegram.
     */
    public function destroyTelegram(Request $request): RedirectResponse
    {
        $this->telegram->setToken(null, $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The Telegram bot is removed; alerts go by email only.')]);

        return back();
    }

    public function updateDatabaseRemoteAccess(Request $request): RedirectResponse
    {
        $validated = $request->validate(['enabled' => ['required', 'boolean']]);

        $this->databaseTools->setRemoteAccess((bool) $validated['enabled'], $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => $validated['enabled']
            ? __('MariaDB now answers from outside, for the hosts accounts allow.')
            : __('MariaDB only answers on this server again.')]);

        return back();
    }

    /**
     * Turn the ionCube Loader on or off for every PHP version.
     */
    public function updateIonCube(Request $request): RedirectResponse
    {
        $validated = $request->validate(['enabled' => ['required', 'boolean']]);
        $state = $this->settings->setIonCube((bool) $validated['enabled'], $request->user());

        $message = match (true) {
            $state['enabled'] => __('The ionCube Loader :loader is on for PHP :versions.', ['loader' => (string) $state['loader'], 'versions' => implode(', ', $state['versions'])]),
            (bool) $validated['enabled'] => __('ionCube makes no loader for the PHP versions of this server.'),
            default => __('The ionCube Loader is off.'),
        };

        Inertia::flash('toast', ['type' => $state['enabled'] || ! $validated['enabled'] ? 'success' : 'warning', 'message' => $message]);

        return back();
    }

    /**
     * Turn ClamAV and its scan modes (email, on-access, scheduled) on or off.
     * The sub-modes only apply while ClamAV itself is on.
     */
    public function updateClamav(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'enabled' => ['required', 'boolean'],
            'email' => ['required', 'boolean'],
            'on_access' => ['required', 'boolean'],
            'scheduled' => ['required', 'boolean'],
        ]);

        $enabled = (bool) $validated['enabled'];
        $this->settings->setClamav([
            'enabled' => $enabled,
            'email' => $enabled && (bool) $validated['email'],
            'on_access' => $enabled && (bool) $validated['on_access'],
            'scheduled' => $enabled && (bool) $validated['scheduled'],
        ], $request->user());

        Inertia::flash('toast', [
            'type' => 'success',
            'message' => $enabled ? __('ClamAV is being turned on; downloading signatures can take a few minutes.') : __('ClamAV is off.'),
        ]);

        return back();
    }

    /**
     * Set the server-wide ModSecurity (WAF) mode: off, detection-only or on.
     * It is the default for every website that does not override it.
     */
    public function updateWaf(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'mode' => ['required', Rule::in(ServerSettingsService::WAF_MODES)],
        ]);

        $state = $this->settings->setWaf((string) $validated['mode'], $request->user());

        $message = match ($state['mode']) {
            'on' => __('ModSecurity is on: the OWASP rules now block offending requests.'),
            'detection' => __('ModSecurity is in detection-only mode: offending requests are logged, not blocked.'),
            default => __('ModSecurity is off.'),
        };

        Inertia::flash('toast', ['type' => 'success', 'message' => $message]);

        return back();
    }

    /**
     * Start updating the panel to the newest release on the chosen channel.
     */
    public function startUpdate(Request $request): RedirectResponse
    {
        $installed = '';

        try {
            $server = $this->settings->serverState();
            $installed = is_string($server['version'] ?? null) ? $server['version'] : '';
        } catch (AgentException $exception) {
            throw ValidationException::withMessages(['update' => __('The agent did not answer. Check that rayxod is running: systemctl status rayxod')]);
        }

        $result = $this->updates->start($installed, $request->user());

        Inertia::flash('toast', [
            'type' => 'success',
            'message' => __('Updating to v:version has started; follow it on this page.', ['version' => $result['version']]),
        ]);

        return back();
    }

    /**
     * Choose the update channel (stable or beta).
     */
    public function setUpdateChannel(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'channel' => ['required', Rule::in(UpdateService::CHANNELS)],
        ]);

        $this->updates->setChannel((string) $validated['channel'], $request->user());

        return back();
    }

    /**
     * Scan a path under /home now and flash the result.
     */
    public function scanClamav(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'path' => ['required', 'string', 'max:255', 'starts_with:/home', 'not_regex:/\.\./'],
        ]);

        $result = $this->settings->scanClamav($validated['path'], $request->user());

        $count = count($result['infected']);
        Inertia::flash('toast', [
            'type' => $result['clean'] ? 'success' : 'warning',
            'message' => $result['clean']
                ? __('Scan clean: no viruses in :path.', ['path' => $validated['path']])
                : __(':count infected file(s) in :path.', ['count' => $count, 'path' => $validated['path']]),
        ]);

        return back();
    }

    /**
     * Change the upload sizes websites may use; every website is applied
     * again when they change.
     */
    public function updatePhpUploads(UpdatePhpUploadLimitsRequest $request): RedirectResponse
    {
        $changed = $this->settings->setPhpUploads(
            $request->string('limit')->toString(),
            $request->string('default')->toString(),
            $request->user(),
        );

        $message = __('Nothing changed.');

        if ($changed) {
            $message = __('Upload sizes saved; :count websites are being updated.', ['count' => $this->domains->reapplyAll($request->user())]);
        }

        Inertia::flash('toast', ['type' => 'success', 'message' => $message]);

        return back();
    }

    /**
     * The port being tried, as the agent reports it.
     *
     * @return array{port: int, confirmed: bool}|null
     */
    private function portTrial(): ?array
    {
        try {
            $trial = $this->settings->serverState()['port_trial'] ?? null;
        } catch (AgentException $exception) {
            throw $this->portError($exception);
        }

        if (! is_array($trial) || ! is_int($trial['port'] ?? null)) {
            return null;
        }

        return ['port' => $trial['port'], 'confirmed' => (bool) ($trial['confirmed'] ?? false)];
    }

    private function portError(AgentException $exception): ValidationException
    {
        if ($exception->reason === null && $exception->getCode() !== -32000 && $exception->getCode() !== -32602) {
            report($exception);
        }

        return ValidationException::withMessages(['port' => __('The panel port cannot be changed: :detail', ['detail' => $exception->getMessage()])]);
    }
}
