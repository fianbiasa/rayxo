<?php

use App\Http\Middleware\EnsureAccountIsActive;
use App\Http\Middleware\EnsureEmailIsVerified;
use App\Http\Middleware\EnsureImpersonationIsValid;
use App\Http\Middleware\EnsureLoginKeyAllows;
use App\Http\Middleware\EnsureUserHasRole;
use App\Http\Middleware\HandleAppearance;
use App\Http\Middleware\HandleInertiaRequests;
use App\Http\Middleware\RequireTwoFactor;
use Illuminate\Foundation\Application;
use Illuminate\Foundation\Configuration\Exceptions;
use Illuminate\Foundation\Configuration\Middleware;
use Illuminate\Http\Middleware\AddLinkHeadersForPreloadedAssets;
use Illuminate\Http\Request;
use Illuminate\Routing\Middleware\SubstituteBindings;

return Application::configure(basePath: dirname(__DIR__))
    ->withRouting(
        web: __DIR__.'/../routes/web.php',
        api: __DIR__.'/../routes/api.php',
        commands: __DIR__.'/../routes/console.php',
        health: '/up',
    )
    ->withMiddleware(function (Middleware $middleware): void {
        // The binding cookies are read by phpMyAdmin's sign-on script and
        // Roundcube's sign-on plugin, which cannot decrypt Laravel cookies.
        $middleware->encryptCookies(except: ['appearance', 'sidebar_state', 'rayxo_pma_binding', 'rayxo_webmail_binding']);

        $middleware->web(append: [
            HandleAppearance::class,
            HandleInertiaRequests::class,
            AddLinkHeadersForPreloadedAssets::class,
            EnsureAccountIsActive::class,
            EnsureImpersonationIsValid::class,
            RequireTwoFactor::class,
        ]);

        $middleware->alias([
            'role' => EnsureUserHasRole::class,
            'login-key' => EnsureLoginKeyAllows::class,
            'verified' => EnsureEmailIsVerified::class,
        ]);

        // Run the role and login-key checks before route-model binding (but
        // after authentication, which is earlier in the priority list), so a
        // request without the required role or permission is refused before a
        // bound model is resolved. Otherwise a missing id (404) and an
        // existing id (403) would tell an authenticated caller which records
        // exist panel-wide.
        $middleware->prependToPriorityList(SubstituteBindings::class, EnsureUserHasRole::class);
        $middleware->prependToPriorityList(SubstituteBindings::class, EnsureLoginKeyAllows::class);
    })
    ->withExceptions(function (Exceptions $exceptions): void {
        // A custom certificate's private key must never reach the session
        // as old input when its form fails validation.
        // api_key is another panel's access key entered to move an account in;
        // secret_key is an S3 secret for remote backups. Neither must be kept
        // as old input in the session when a form fails validation.
        $exceptions->dontFlash(['private_key', 'certificate', 'api_key', 'secret_key']);

        $exceptions->shouldRenderJsonWhen(
            fn (Request $request) => $request->is('api/*') || $request->expectsJson(),
        );
    })->create();
