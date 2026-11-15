<?php

namespace App\Providers;

use App\Actions\Fortify\ResetUserPassword;
use App\Http\Controllers\NewPasswordController;
use App\Http\Controllers\PasswordResetLinkController;
use App\Http\Responses\PasswordResetLinkResponse;
use App\Models\User;
use Illuminate\Auth\Notifications\ResetPassword;
use Illuminate\Cache\RateLimiting\Limit;
use Illuminate\Http\Request;
use Illuminate\Notifications\Messages\MailMessage;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Hash;
use Illuminate\Support\Facades\RateLimiter;
use Illuminate\Support\ServiceProvider;
use Illuminate\Support\Str;
use Illuminate\Validation\Rules\Password;
use Illuminate\Validation\ValidationException;
use Inertia\Inertia;
use Laravel\Fortify\Contracts\FailedPasswordResetLinkRequestResponse;
use Laravel\Fortify\Contracts\SuccessfulPasswordResetLinkRequestResponse;
use Laravel\Fortify\Features;
use Laravel\Fortify\Fortify;
use Laravel\Fortify\Http\Controllers\NewPasswordController as FortifyNewPasswordController;
use Laravel\Fortify\Http\Controllers\PasswordResetLinkController as FortifyPasswordResetLinkController;

class FortifyServiceProvider extends ServiceProvider
{
    /**
     * How many accounts of one email address a sign-in tries.
     */
    public const ACCOUNTS_PER_EMAIL = 10;

    /**
     * Register any application services.
     */
    public function register(): void
    {
        // A password reset request answers the same for every email, and
        // is limited (see the two classes); it sends a link for each account
        // of the address, and a link resets the account it was sent for.
        $this->app->bind(FortifyPasswordResetLinkController::class, PasswordResetLinkController::class);
        $this->app->bind(FortifyNewPasswordController::class, NewPasswordController::class);
        $this->app->bind(FailedPasswordResetLinkRequestResponse::class, PasswordResetLinkResponse::class);
        $this->app->bind(SuccessfulPasswordResetLinkRequestResponse::class, PasswordResetLinkResponse::class);
    }

    /**
     * Bootstrap any application services.
     */
    public function boot(): void
    {
        $this->configureActions();
        $this->configureViews();
        $this->configureRateLimiting();
    }

    /**
     * Configure Fortify actions.
     */
    private function configureActions(): void
    {
        Fortify::resetUserPasswordsUsing(ResetUserPassword::class);
        Fortify::authenticateUsing(fn (Request $request): ?User => $this->authenticate($request));
        ResetPassword::toMailUsing(fn (User $user, string $token): MailMessage => $this->passwordResetMail($user, $token));
    }

    /**
     * Resolve the account for a login attempt by username or email address.
     *
     * Suspended or not-yet-provisioned accounts are rejected only after the
     * password is verified, so the message never leaks account state to
     * someone who does not know the password.
     *
     * @throws ValidationException
     */
    private function authenticate(Request $request): ?User
    {
        $login = Str::lower(trim((string) $request->input(Fortify::username())));

        $user = User::query()->where('username', $login)->first()
            ?? $this->accountOfEmail($login, (string) $request->input('password'));

        // Without an account the password is still checked, against a hash
        // of nothing, so the answer takes as long and the time does not tell
        // which usernames exist.
        $hash = $user->password ?? Cache::rememberForever('auth.unknown-user-hash', fn (): string => Hash::make(Str::random(40)));

        if (! Hash::check((string) $request->input('password'), $hash) || $user === null) {
            return null;
        }

        if (! $user->status->canLogIn()) {
            throw ValidationException::withMessages([
                Fortify::username() => __('This account is suspended or not active yet.'),
            ]);
        }

        return $user;
    }

    /**
     * The account an email address signs in to. Hosting accounts may share
     * an address (several hosting services of one WHMCS client); then the
     * password tells which account is meant, and when it fits more than one,
     * the username has to be given. Only the first few accounts of an
     * address are tried, so one attempt cannot take long.
     *
     * @throws ValidationException
     */
    private function accountOfEmail(string $email, string $password): ?User
    {
        $accounts = User::query()->where('email', $email)->orderBy('id')->limit(self::ACCOUNTS_PER_EMAIL)->get();

        if ($accounts->count() < 2) {
            return $accounts->first();
        }

        $matching = $accounts->filter(fn (User $account): bool => Hash::check($password, (string) $account->password));

        if ($matching->count() > 1) {
            throw ValidationException::withMessages([
                Fortify::username() => __('Several accounts use this email address and password; sign in with the username instead.'),
            ]);
        }

        return $matching->first() ?? $accounts->first();
    }

    /**
     * A password reset mail names the account it resets, since hosting
     * accounts may share an address, and its link carries the username the
     * reset token belongs to (see User::getEmailForPasswordReset()).
     */
    private function passwordResetMail(User $user, string $token): MailMessage
    {
        $url = url(route('password.reset', ['token' => $token, 'email' => $user->email, 'username' => $user->username], false));
        $broker = config('auth.defaults.passwords');

        return (new MailMessage)
            ->subject(__('Reset Password Notification'))
            ->line(__('You are receiving this email because we received a password reset request for your account.'))
            ->line(__('Account: :username', ['username' => $user->username]))
            ->action(__('Reset Password'), $url)
            ->line(__('This password reset link will expire in :count minutes.', ['count' => config("auth.passwords.{$broker}.expire")]))
            ->line(__('If you did not request a password reset, no further action is required.'));
    }

    /**
     * Configure Fortify views.
     */
    private function configureViews(): void
    {
        Fortify::loginView(fn (Request $request) => Inertia::render('auth/login', [
            'canResetPassword' => Features::enabled(Features::resetPasswords()),
            'status' => $request->session()->get('status'),
        ]));

        Fortify::resetPasswordView(fn (Request $request) => Inertia::render('auth/reset-password', [
            'email' => $request->email,
            'username' => (string) $request->query('username', ''),
            'token' => $request->route('token'),
            'passwordRules' => Password::defaults()->toPasswordRulesString(),
        ]));

        Fortify::requestPasswordResetLinkView(fn (Request $request) => Inertia::render('auth/forgot-password', [
            'status' => $request->session()->get('status'),
        ]));

        Fortify::verifyEmailView(fn (Request $request) => Inertia::render('auth/verify-email', [
            'status' => $request->session()->get('status'),
        ]));

        Fortify::twoFactorChallengeView(fn () => Inertia::render('auth/two-factor-challenge'));

        Fortify::confirmPasswordView(fn () => Inertia::render('auth/confirm-password'));
    }

    /**
     * Configure rate limiting.
     */
    private function configureRateLimiting(): void
    {
        RateLimiter::for('two-factor', function (Request $request) {
            return Limit::perMinute(5)->by($request->session()->get('login.id'));
        });

        RateLimiter::for('login', function (Request $request) {
            $throttleKey = Str::transliterate(Str::lower($request->input(Fortify::username())).'|'.$request->ip());

            return Limit::perMinute(5)->by($throttleKey);
        });

        RateLimiter::for('passkeys', function (Request $request) {
            return Limit::perMinute(10)->by(
                ($request->input('credential.id') ?: $request->session()->getId()).'|'.$request->ip(),
            );
        });
    }
}
