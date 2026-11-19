<?php

namespace App\Services;

use App\Enums\AccountStatus;
use App\Models\RedisInstance;
use App\Models\User;
use Illuminate\Support\Facades\DB;
use Illuminate\Validation\ValidationException;

/**
 * Turns the per-account Redis cache on and off for hosting accounts. The
 * panel record is written right away and the change on the server is queued
 * for the agent (redis.provision / redis.deprovision), which runs one
 * redis-server per account as the account's own user.
 *
 * The password is generated here, stored encrypted so it can be shown to the
 * account, and sent to the agent inside the (encrypted) task payload to put
 * in the instance's config.
 */
class RedisService
{
    public function __construct(
        private TaskDispatcher $tasks,
        private AuditLogger $audit,
    ) {}

    /**
     * Turn on, or update, the account's Redis cache and have the agent
     * provision it. Calling it again is safe.
     *
     * @throws ValidationException
     */
    public function enable(User $account, User $actor, ?int $maxMemoryMB = null): RedisInstance
    {
        $this->ensureAccountIsActive($account);

        return DB::transaction(function () use ($account, $actor, $maxMemoryMB): RedisInstance {
            $instance = $account->redisInstance()->firstOrNew([]);

            if (! $instance->exists) {
                $instance->password = RedisInstance::newPassword();
            }

            if ($maxMemoryMB !== null) {
                $instance->max_memory_mb = $maxMemoryMB;
            }

            $instance->enabled = true;
            $instance->save();

            $this->dispatchProvision($instance, $account, $actor);
            $this->audit->record('redis.enabled', $account, ['max_memory_mb' => $instance->max_memory_mb], $actor);

            return $instance;
        });
    }

    /**
     * Turn off the account's Redis cache: the agent stops and removes the
     * instance; the panel record is kept, marked disabled.
     */
    public function disable(User $account, User $actor): void
    {
        $instance = $account->redisInstance;

        if ($instance === null) {
            return;
        }

        DB::transaction(function () use ($instance, $account, $actor): void {
            $instance->forceFill(['enabled' => false])->save();
            $this->tasks->dispatch('redis.deprovision', ['username' => $account->username], $account, $actor);
            $this->audit->record('redis.disabled', $account, [], $actor);
        });
    }

    /**
     * Give the cache a new password and re-provision it.
     *
     * @throws ValidationException
     */
    public function rotatePassword(User $account, User $actor): RedisInstance
    {
        $instance = $account->redisInstance;

        if ($instance === null || ! $instance->enabled) {
            throw ValidationException::withMessages(['redis' => __('Enable Redis before changing its password.')]);
        }

        return DB::transaction(function () use ($instance, $account, $actor): RedisInstance {
            $instance->forceFill(['password' => RedisInstance::newPassword()])->save();
            $this->dispatchProvision($instance, $account, $actor);
            $this->audit->record('redis.password_rotated', $account, [], $actor);

            return $instance;
        });
    }

    /**
     * Remove the account's Redis instance when the account itself is being
     * deleted. Runs before account.delete so the instance is stopped before
     * the Linux user goes.
     */
    public function cleanup(User $account, User $actor): void
    {
        if ($account->redisInstance()->exists()) {
            $this->tasks->dispatch('redis.deprovision', ['username' => $account->username], $account, $actor);
        }
    }

    private function dispatchProvision(RedisInstance $instance, User $account, ?User $actor): void
    {
        $this->tasks->dispatch('redis.provision', [
            'username' => $account->username,
            'password' => $instance->password,
            'max_memory_mb' => $instance->max_memory_mb,
        ], $account, $actor);
    }

    /**
     * @throws ValidationException
     */
    private function ensureAccountIsActive(User $account): void
    {
        if ($account->status !== AccountStatus::Active) {
            throw ValidationException::withMessages(['redis' => __('The account must be active to use Redis.')]);
        }
    }
}
