<?php

namespace App\Http\Controllers\Admin;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Http\Controllers\Controller;
use App\Services\AuditLogger;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Validation\ValidationException;
use Inertia\Inertia;
use Inertia\Response;

/**
 * The brute-force monitor: fail2ban's jails and the addresses each has
 * banned, unbanning an address, and the whitelist of addresses fail2ban must
 * never ban. What is banned and jailed is decided by the agent.
 */
class BruteForceController extends Controller
{
    public function __construct(
        private AgentClient $agent,
        private AuditLogger $audit,
    ) {}

    /**
     * Every jail with its failed and banned counts, the banned addresses, and
     * the agent's whitelist.
     */
    public function index(): Response
    {
        $report = null;
        $error = null;

        try {
            $report = $this->agent->call('bfm.status', [], 20);
        } catch (AgentException $exception) {
            report($exception);
            $error = __('The agent did not answer. Check that rayxod is running: systemctl status rayxod');
        }

        return Inertia::render('admin/brute-force/index', [
            'report' => $report,
            'error' => $error,
        ]);
    }

    /**
     * Remove a ban on one address across every jail.
     *
     * @throws ValidationException
     */
    public function unban(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'ip' => ['required', 'string', 'ip'],
        ]);

        try {
            $this->agent->call('bfm.unban', ['ip' => $validated['ip']], 30);
        } catch (AgentException $exception) {
            report($exception);

            throw ValidationException::withMessages([
                'ip' => __(':ip could not be unbanned. Try again in a moment.', ['ip' => $validated['ip']]),
            ]);
        }

        $this->audit->record('bfm.unbanned', null, ['ip' => $validated['ip']]);

        Inertia::flash('toast', ['type' => 'success', 'message' => __(':ip is no longer banned.', ['ip' => $validated['ip']])]);

        return back();
    }

    /**
     * Replace the whitelist of addresses fail2ban must never ban. Each entry
     * is a plain IP address or a CIDR range; the loopback addresses are kept
     * by the agent whether or not they are listed.
     *
     * @throws ValidationException
     */
    public function updateWhitelist(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'ips' => ['present', 'array', 'max:200'],
            'ips.*' => ['string', 'max:43', function (string $attribute, mixed $value, callable $fail): void {
                if (! is_string($value) || ! $this->isIpOrCidr($value)) {
                    $fail(__(':value is not an IP address or CIDR range.', ['value' => (string) $value]));
                }
            }],
        ]);

        try {
            $this->agent->call('bfm.whitelist', ['ips' => array_values($validated['ips'])], 30);
        } catch (AgentException $exception) {
            report($exception);

            throw ValidationException::withMessages([
                'ips' => __('The whitelist could not be saved. Try again in a moment.'),
            ]);
        }

        $this->audit->record('bfm.whitelist_changed', null, ['ips' => array_values($validated['ips'])]);

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The whitelist has been saved.')]);

        return back();
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
