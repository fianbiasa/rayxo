<?php

namespace App\Listeners;

use App\Models\User;
use App\Services\AuditLogger;
use Illuminate\Auth\Events\Failed;
use Illuminate\Support\Facades\Log;
use Illuminate\Support\Str;
use Laravel\Fortify\Fortify;

class RecordFailedLogin
{
    public function __construct(private AuditLogger $audit) {}

    /**
     * Record the attempted login name only; the password is never stored.
     * Also write a one-line entry to the auth log with the client address,
     * which the fail2ban rayxo-panel jail watches to block brute force.
     */
    public function handle(Failed $event): void
    {
        $attempted = $event->credentials[Fortify::username()] ?? $event->credentials['email'] ?? '';
        $username = Str::limit((string) $attempted, 255, '');

        $this->audit->record(
            'auth.failed',
            $event->user instanceof User ? $event->user : null,
            ['username' => $username],
        );

        // JSON-encode the username so it cannot contain a line break or the
        // trailing " from <ip>" the jail matches on; the address is from the
        // request, which the jail reads as <HOST> anchored at the line's end.
        $quoted = json_encode($username, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
        if ($quoted === false) {
            $quoted = '"?"';
        }

        Log::channel('auth')->warning('Failed panel login for '.$quoted.' from '.(request()->ip() ?? '-'));
    }
}
