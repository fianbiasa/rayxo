<?php

namespace App\Http\Controllers;

use App\Models\User;
use Illuminate\Contracts\Support\Responsable;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Password;
use Laravel\Fortify\Actions\CompletePasswordReset;
use Laravel\Fortify\Contracts\FailedPasswordResetResponse;
use Laravel\Fortify\Contracts\PasswordResetResponse;
use Laravel\Fortify\Contracts\ResetsUserPasswords;
use Laravel\Fortify\Http\Controllers\NewPasswordController as FortifyNewPasswordController;

/**
 * Fortify's password reset, by the username the link was sent for rather
 * than the email address: hosting accounts may share an address, and the
 * reset token belongs to one account (User::getEmailForPasswordReset()).
 */
class NewPasswordController extends FortifyNewPasswordController
{
    public function store(Request $request): Responsable
    {
        $request->validate([
            'token' => ['required', 'string'],
            'username' => ['required', 'string'],
            'password' => ['required'],
        ]);

        $status = $this->broker()->reset(
            $request->only('username', 'password', 'password_confirmation', 'token'),
            function (User $user) use ($request): void {
                app(ResetsUserPasswords::class)->reset($user, $request->all());

                app(CompletePasswordReset::class)($this->guard, $user);
            },
        );

        return $status === Password::PASSWORD_RESET
            ? app(PasswordResetResponse::class, ['status' => $status])
            : app(FailedPasswordResetResponse::class, ['status' => $status]);
    }
}
