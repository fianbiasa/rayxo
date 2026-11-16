<?php

namespace App\Listeners;

use App\Models\User;
use App\Services\AuditLogger;
use App\Services\ImpersonationService;
use Illuminate\Auth\Events\Login;

class RecordSuccessfulLogin
{
    public function __construct(private AuditLogger $audit) {}

    public function handle(Login $event): void
    {
        // Signing in as another account ("login as") or back is not a login
        // of that account.
        if (! $event->user instanceof User || request()->attributes->get(ImpersonationService::SWITCHING) === true) {
            return;
        }

        // A real sign-in is never inside a "login as", so drop any marker left
        // over from an earlier one (e.g. after the impersonated account was
        // deleted); otherwise it could later switch into the impersonator.
        if (request()->hasSession()) {
            request()->session()->forget(ImpersonationService::SESSION_KEY);
        }

        $event->user->forceFill(['last_login_at' => now()])->saveQuietly();

        $this->audit->record('auth.login', $event->user, actor: $event->user);
    }
}
