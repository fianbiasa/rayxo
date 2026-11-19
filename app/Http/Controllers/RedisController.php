<?php

namespace App\Http\Controllers;

use App\Services\RedisService;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Inertia\Inertia;
use Inertia\Response;

class RedisController extends Controller
{
    public function __construct(private RedisService $redis) {}

    /**
     * Show the signed-in account's Redis cache: whether it is on and, if so,
     * how its app connects to it.
     */
    public function index(Request $request): Response
    {
        $account = $request->user();
        $instance = $account->redisInstance;
        $on = (bool) $instance?->enabled;

        return Inertia::render('redis/index', [
            'enabled' => $on,
            'connection' => $on ? [
                'host' => $instance->socket(),
                'port' => 0,
                'password' => $instance->password,
                'prefix' => $account->username.':',
                'client' => 'phpredis',
                'max_memory_mb' => $instance->max_memory_mb,
            ] : null,
        ]);
    }

    /**
     * Turn the account's Redis cache on (or update its memory cap).
     */
    public function store(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'max_memory_mb' => ['nullable', 'integer', 'min:16', 'max:8192'],
        ]);

        $this->redis->enable($request->user(), $request->user(), $validated['max_memory_mb'] ?? null);

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Redis is being set up.')]);

        return to_route('redis.index');
    }

    /**
     * Turn the account's Redis cache off.
     */
    public function destroy(Request $request): RedirectResponse
    {
        $this->redis->disable($request->user(), $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Redis is being turned off.')]);

        return back();
    }

    /**
     * Give the account's Redis a new password.
     */
    public function rotate(Request $request): RedirectResponse
    {
        $this->redis->rotatePassword($request->user(), $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('A new Redis password is being applied.')]);

        return back();
    }
}
