<?php

namespace App\Http\Middleware;

use App\Services\LoginKeyService;
use Closure;
use Illuminate\Auth\AuthenticationException;
use Illuminate\Contracts\Auth\Middleware\AuthenticatesRequests;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Auth;
use Symfony\Component\HttpFoundation\Response;

/**
 * Authenticates every API request by its login key, and only by it: the
 * key is looked up on each request, never taken from an earlier one, and a
 * panel session counts for nothing here.
 *
 * It implements AuthenticatesRequests so the middleware priority runs it
 * before route-model binding: without that, a request with no valid key could
 * tell an existing account (rejected after binding) from a missing one
 * (a 404 at binding), enumerating usernames.
 */
class AuthenticateWithLoginKey implements AuthenticatesRequests
{
    public function handle(Request $request, Closure $next): Response
    {
        $user = LoginKeyService::authenticate($request);

        if ($user === null) {
            throw new AuthenticationException('Unauthenticated.', ['api']);
        }

        Auth::shouldUse('api');
        Auth::guard('api')->setUser($user);

        return $next($request);
    }
}
