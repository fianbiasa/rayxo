<?php

namespace App\Http\Controllers;

use App\Services\LoginLinkService;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Auth;

/**
 * Signs a hosting account in through a one-time link from the API (see
 * LoginLinkService), such as WHMCS's "Login to Control Panel".
 */
class LoginLinkController extends Controller
{
    public function __invoke(Request $request, string $token, LoginLinkService $links): RedirectResponse
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
