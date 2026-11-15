<?php

namespace App\Http\Controllers;

use App\Http\Responses\PasswordResetLinkResponse;
use App\Models\User;
use Illuminate\Contracts\Support\Responsable;
use Illuminate\Support\Facades\RateLimiter;
use Illuminate\Support\Str;
use Laravel\Fortify\Http\Controllers\PasswordResetLinkController as FortifyPasswordResetLinkController;
use Laravel\Fortify\Http\Requests\SendPasswordResetLinkRequest;

/**
 * Fortify's password reset link request, limited per address and per
 * email, so nobody can flood an inbox with reset mail or try many emails
 * quickly. Over the limit, the answer is the same as always and no mail
 * is sent.
 *
 * Hosting accounts may share an email address, so every account of the
 * address (up to a few) gets a link of its own, which names it and resets
 * only it.
 */
class PasswordResetLinkController extends FortifyPasswordResetLinkController
{
    /**
     * Requests per IP address, and per email, in ten minutes.
     */
    public const PER_IP = 10;

    public const PER_EMAIL = 3;

    /**
     * Accounts of one email address that get a link.
     */
    public const ACCOUNTS = 10;

    public function store(SendPasswordResetLinkRequest $request): Responsable
    {
        $keys = [
            'password-reset-ip:'.$request->ip() => self::PER_IP,
            'password-reset-email:'.Str::lower((string) $request->input('email')) => self::PER_EMAIL,
        ];

        foreach ($keys as $key => $limit) {
            if (RateLimiter::tooManyAttempts($key, $limit)) {
                return new PasswordResetLinkResponse;
            }
        }

        foreach (array_keys($keys) as $key) {
            RateLimiter::hit($key, 600);
        }

        $accounts = User::query()
            ->where('email', Str::lower(trim((string) $request->input('email'))))
            ->orderBy('id')
            ->limit(self::ACCOUNTS)
            ->get();

        foreach ($accounts as $account) {
            $this->broker()->sendResetLink(['username' => $account->username]);
        }

        return new PasswordResetLinkResponse;
    }
}
