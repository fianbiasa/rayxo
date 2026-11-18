<?php

namespace App\Http\Middleware;

use App\Services\ImpersonationService;
use App\Services\ServerSettingsService;
use Closure;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;

/**
 * Sends a signed-in administrator or reseller whose role must have two-factor
 * authentication (set in Server Settings) to the security page to turn it on,
 * until they have. The security page, the two-factor and password-confirmation
 * endpoints, and signing out stay reachable, so a required user can always
 * either enable 2FA or leave — nobody is locked out. A session that is
 * impersonating an account ("login as") is left alone: the administrator who
 * started it already passed their own 2FA, and the account is not really
 * signing in.
 */
class RequireTwoFactor
{
    public function __construct(private ServerSettingsService $settings) {}

    public function handle(Request $request, Closure $next): Response
    {
        $user = $request->user();

        if ($user === null
            || $request->session()->has(ImpersonationService::SESSION_KEY)
            || ! $this->settings->requiresTwoFactor($user->role)
            || $user->hasEnabledTwoFactorAuthentication()
            || $this->isExempt($request)
        ) {
            return $next($request);
        }

        return redirect()->route('security.edit')
            ->with('status', __('Set up two-factor authentication to continue.'));
    }

    /**
     * The routes a required user must still reach to turn two-factor on
     * (the security page, the two-factor and password-confirmation endpoints)
     * or to sign out.
     */
    private function isExempt(Request $request): bool
    {
        $name = (string) $request->route()?->getName();

        return $name === 'security.edit'
            || $name === 'logout'
            || str_starts_with($name, 'two-factor.')
            || str_starts_with($name, 'password.confirm');
    }
}
