<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Attributes\Fillable;
use Illuminate\Database\Eloquent\Attributes\Hidden;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Illuminate\Support\Carbon;

/**
 * One config file (or other target) that currently differs from what RAYXO
 * would write. Rows exist only for targets that are drifting: a scan deletes a
 * row when its target matches again. The payload is what the agent's apply
 * action needs to rewrite the target, so it is encrypted and hidden.
 *
 * @property int $id
 * @property int|null $user_id
 * @property string $kind
 * @property string $path
 * @property string $apply_action
 * @property array<string, mixed> $payload
 * @property bool $missing
 * @property string|null $diff
 * @property Carbon $checked_at
 */
#[Hidden(['payload'])]
#[Fillable(['user_id', 'kind', 'path', 'apply_action', 'payload', 'missing', 'diff', 'checked_at'])]
class ConfigDrift extends Model
{
    protected function casts(): array
    {
        return [
            'payload' => 'encrypted:array',
            'missing' => 'boolean',
            'checked_at' => 'datetime',
        ];
    }

    /**
     * @return BelongsTo<User, $this>
     */
    public function account(): BelongsTo
    {
        return $this->belongsTo(User::class, 'user_id');
    }
}
