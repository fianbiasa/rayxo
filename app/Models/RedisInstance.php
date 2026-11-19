<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Attributes\Fillable;
use Illuminate\Database\Eloquent\Attributes\Hidden;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Illuminate\Support\Carbon;
use Illuminate\Support\Str;

/**
 * A per-account Redis cache: one redis-server the agent runs as the account's
 * own user, reachable only over the account's unix socket. The password is
 * stored encrypted so it can be shown to the account; the agent writes it
 * into the instance's config (requirepass).
 *
 * `user_id`, `password` and `enabled` are deliberately not mass assignable.
 *
 * @property int $id
 * @property int $user_id
 * @property string $password
 * @property int $max_memory_mb
 * @property bool $enabled
 * @property Carbon|null $created_at
 * @property Carbon|null $updated_at
 * @property-read User $account
 */
#[Fillable(['max_memory_mb'])]
#[Hidden(['password'])]
class RedisInstance extends Model
{
    /**
     * The unix socket path the account's app connects to.
     */
    public function socket(): string
    {
        return '/run/rayxo-redis/'.$this->account->username.'/redis.sock';
    }

    /**
     * A fresh alphanumeric password, safe for the redis config's requirepass
     * and accepted by the agent's validation.
     */
    public static function newPassword(): string
    {
        return Str::password(32, symbols: false);
    }

    /**
     * Get the attributes that should be cast.
     *
     * @return array<string, string>
     */
    protected function casts(): array
    {
        return [
            'password' => 'encrypted',
            'max_memory_mb' => 'integer',
            'enabled' => 'boolean',
        ];
    }

    /**
     * The hosting account this cache belongs to.
     *
     * @return BelongsTo<User, $this>
     */
    public function account(): BelongsTo
    {
        return $this->belongsTo(User::class, 'user_id');
    }
}
