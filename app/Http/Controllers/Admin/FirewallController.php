<?php

namespace App\Http\Controllers\Admin;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Http\Controllers\Controller;
use App\Services\AuditLogger;
use Closure;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Validation\ValidationException;
use Inertia\Inertia;
use Inertia\Response;

/**
 * The server firewall (nftables), managed by the agent. It is off until the
 * admin turns it on; enabling or changing it opens a trial that reverts
 * itself unless confirmed, so a wrong rule can never lock the admin out.
 */
class FirewallController extends Controller
{
    public function __construct(
        private AgentClient $agent,
        private AuditLogger $audit,
    ) {}

    /**
     * The firewall's state: the confirmed config, any pending change, and the
     * service ports that are always open.
     */
    public function index(): Response
    {
        $status = null;
        $error = null;

        try {
            $status = $this->agent->call('firewall.status', [], 20);
        } catch (AgentException $exception) {
            report($exception);
            $error = __('The agent did not answer. Check that rayxod is running: systemctl status rayxod');
        }

        return Inertia::render('admin/firewall/index', [
            'status' => $status,
            'error' => $error,
        ]);
    }

    /**
     * Turn the firewall on or change it (behind a trial), or turn it off.
     *
     * @throws ValidationException
     */
    public function apply(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'enabled' => ['required', 'boolean'],
            'tcp_ports' => ['present', 'array', 'max:200'],
            'tcp_ports.*' => ['integer', 'between:1,65535'],
            'udp_ports' => ['present', 'array', 'max:200'],
            'udp_ports.*' => ['integer', 'between:1,65535'],
            'allow_ips' => ['present', 'array', 'max:200'],
            'allow_ips.*' => ['string', 'max:43', $this->ipOrCidr()],
            'deny_ips' => ['present', 'array', 'max:200'],
            'deny_ips.*' => ['string', 'max:43', $this->ipOrCidr()],
        ]);

        $config = [
            'enabled' => $validated['enabled'],
            'tcp_ports' => array_values(array_map('intval', $validated['tcp_ports'])),
            'udp_ports' => array_values(array_map('intval', $validated['udp_ports'])),
            'allow_ips' => array_values($validated['allow_ips']),
            'deny_ips' => array_values($validated['deny_ips']),
        ];

        try {
            $this->agent->call('firewall.apply', $config, 30);
        } catch (AgentException $exception) {
            throw $this->explain($exception);
        }

        $this->audit->record('firewall.applied', null, $config);

        Inertia::flash('toast', ['type' => 'success', 'message' => $validated['enabled']
            ? __('The firewall is on for a trial. Confirm it while you still have access, or it reverts on its own.')
            : __('The firewall is off.')]);

        return back();
    }

    /**
     * Keep a pending firewall change for good.
     *
     * @throws ValidationException
     */
    public function confirm(): RedirectResponse
    {
        try {
            $this->agent->call('firewall.confirm', [], 20);
        } catch (AgentException $exception) {
            throw $this->explain($exception);
        }

        $this->audit->record('firewall.confirmed', null, []);

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The firewall change is confirmed.')]);

        return back();
    }

    /**
     * Revert a pending firewall change at once.
     *
     * @throws ValidationException
     */
    public function cancel(): RedirectResponse
    {
        try {
            $this->agent->call('firewall.cancel', [], 20);
        } catch (AgentException $exception) {
            throw $this->explain($exception);
        }

        $this->audit->record('firewall.cancelled', null, []);

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The firewall change was reverted.')]);

        return back();
    }

    /**
     * Turn an agent refusal into a field error the panel can show.
     */
    private function explain(AgentException $exception): ValidationException
    {
        if ($exception->reason === null) {
            report($exception);
        }

        return ValidationException::withMessages(['firewall' => match ($exception->reason) {
            'trial_running' => __('A firewall change is already waiting to be confirmed. Confirm or cancel it first.'),
            'no_trial' => __('There is no firewall change waiting to be confirmed; it may have already reverted.'),
            default => __('The firewall could not be changed. Try again in a moment.'),
        }]);
    }

    /**
     * A validation rule that accepts a plain IP address or a CIDR range.
     */
    private function ipOrCidr(): Closure
    {
        return function (string $attribute, mixed $value, callable $fail): void {
            if (! is_string($value) || ! $this->isIpOrCidr($value)) {
                $fail(__(':value is not an IP address or CIDR range.', ['value' => (string) $value]));
            }
        };
    }

    /**
     * Whether value is a plain IP address or a CIDR range (IP/bits), with the
     * prefix length in range for the address family.
     */
    private function isIpOrCidr(string $value): bool
    {
        if (filter_var($value, FILTER_VALIDATE_IP) !== false) {
            return true;
        }

        if (substr_count($value, '/') !== 1) {
            return false;
        }

        [$address, $bits] = explode('/', $value, 2);

        if (filter_var($address, FILTER_VALIDATE_IP) === false || ! ctype_digit($bits)) {
            return false;
        }

        $maximum = str_contains($address, ':') ? 128 : 32;

        return (int) $bits <= $maximum;
    }
}
