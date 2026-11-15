<?php

namespace App\Services;

use App\Models\LoginLink;
use App\Models\User;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;

/**
 * One-time links that sign a hosting account in to the panel, for single
 * sign-on from a billing system such as WHMCS: its administrator's or
 * reseller's login key asks for one through the API and sends the
 * customer to it. A link works once, within a minute; only the hash of
 * its token is kept.
 */
class LoginLinkService
{
    /**
     * How long a link works.
     */
    public const SECONDS = 60;

    public function __construct(private AuditLogger $audit) {}

    /**
     * A new link that signs account in, at the address the API was called
     * on.
     *
     * @return array{url: string, expires_at: string}
     */
    public function create(User $account, User $actor): array
    {
        $token = Str::random(64);
        $expires = now()->addSeconds(self::SECONDS);

        $link = new LoginLink;
        $link->forceFill([
            'user_id' => $account->id,
            'created_by_id' => $actor->id,
            'token_hash' => hash('sha256', $token),
            'expires_at' => $expires,
        ])->save();

        LoginLink::query()->where('expires_at', '<', now()->subDay())->delete();

        $this->audit->record('account.login_link_created', $account, ['username' => $account->username], $actor);

        return ['url' => route('login-link', ['token' => $token]), 'expires_at' => $expires->toIso8601String()];
    }

    /**
     * The account a link signs in, once: null when the token is unknown,
     * used or expired.
     */
    public function consume(string $token): ?User
    {
        return DB::transaction(function () use ($token): ?User {
            $link = LoginLink::query()->where('token_hash', hash('sha256', $token))->lockForUpdate()->first();

            if ($link === null || $link->used_at !== null || $link->expires_at->isPast()) {
                return null;
            }

            $link->forceFill(['used_at' => now()])->save();

            return $link->account;
        });
    }
}
