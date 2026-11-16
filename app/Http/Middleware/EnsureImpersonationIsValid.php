<?php

namespace App\Http\Middleware;

use App\Services\ImpersonationService;
use Closure;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Auth;
use Symfony\Component\HttpFoundation\Response;

/**
 * End a "login as" session once the account that started it may no longer
 * act as this one: it was deleted, suspended, or the account was moved to
 * another owner. Without this the impersonator would keep control of the
 * account until it signed out by hand.
 */
class EnsureImpersonationIsValid
{
    public function __construct(private ImpersonationService $impersonation) {}

    /**
     * @param  Closure(Request): (Response)  $next
     */
    public function handle(Request $request, Closure $next): Response
    {
        // Only while actually signed in as an account: a leftover marker on a
        // guest session (e.g. after that account was deleted) must not block a
        // fresh sign-in; the login listener clears it instead.
        if ($request->user() !== null
            && $this->impersonation->impersonating($request)
            && ! $this->impersonation->impersonatorMayContinue($request)) {
            Auth::guard('web')->logout();

            $request->session()->invalidate();
            $request->session()->regenerateToken();

            return redirect()->route('login')->withErrors([
                'username' => __('The account you signed in from can no longer manage this one, so the session ended.'),
            ]);
        }

        return $next($request);
    }
}
