<?php

namespace App\Http\Controllers;

use App\Services\LoginLinkService;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Auth;
use Inertia\Inertia;
use Inertia\Response;

/**
 * Signs a hosting account in through a one-time link from the API (see
 * LoginLinkService), such as WHMCS's "Login to Control Panel".
 */
class LoginLinkController extends Controller
{
    /**
     * Follow a login link. When nobody is signed in on this browser (the
     * usual single sign-on from a billing system), sign the account in at
     * once. When a session is already open, swapping it on a GET would be a
     * login CSRF, so ask to confirm first; the confirmation is a POST that
     * carries the session's CSRF token, which an attacker cannot forge.
     */
    public function show(Request $request, string $token, LoginLinkService $links): Response|RedirectResponse
    {
        if (Auth::guard('web')->check()) {
            return Inertia::render('auth/login-link', ['token' => $token]);
        }

        return $this->signIn($request, $token, $links);
    }

    /**
     * Confirm following a login link while already signed in.
     */
    public function confirm(Request $request, string $token, LoginLinkService $links): RedirectResponse
    {
        return $this->signIn($request, $token, $links);
    }

    private function signIn(Request $request, string $token, LoginLinkService $links): RedirectResponse
    {
        $account = $links->consume($token);

        if ($account === null) {
            return redirect()->route('login')->with('status', __('This login link has expired or was used already. Sign in with your password.'));
        }

        if (Auth::guard('web')->check()) {
            Auth::guard('web')->logout();
        }

        $request->session()->invalidate();
        Auth::guard('web')->login($account);
        $request->session()->regenerate();

        return redirect()->route('dashboard');
    }
}
