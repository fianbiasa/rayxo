<?php

namespace App\Models;

use App\Enums\DatabaseStatus;
use Database\Factories\PostgresDatabaseFactory;
use Illuminate\Database\Eloquent\Attributes\Fillable;
use Illuminate\Database\Eloquent\Factories\HasFactory;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Illuminate\Database\Eloquent\Relations\BelongsToMany;
use Illuminate\Support\Carbon;

/**
 * A PostgreSQL database of a hosting account, named <username>_<name>. Its
 * size is measured every fifteen minutes with the disk usage.
 *
 * `user_id`, `status` and the size are deliberately not mass assignable.
 *
 * @property int $id
 * @property int $user_id
 * @property string $name
 * @property DatabaseStatus $status
 * @property int|null $size_bytes
 * @property Carbon|null $measured_at
 * @property string|null $import_status How the last import went: running, done or failed.
 * @property string|null $import_error What PostgreSQL said when the last import failed.
 * @property Carbon|null $imported_at When the last import finished.
 * @property Carbon|null $created_at
 * @property Carbon|null $updated_at
 * @property-read User $account
 */
#[Fillable(['name'])]
class PostgresDatabase extends Model
{
    /** @use HasFactory<PostgresDatabaseFactory> */
    use HasFactory;

    /**
     * Get the attributes that should be cast.
     *
     * @return array<string, string>
     */
    protected function casts(): array
    {
        return [
            'status' => DatabaseStatus::class,
            'size_bytes' => 'integer',
            'measured_at' => 'datetime',
            'imported_at' => 'datetime',
        ];
    }

    /**
     * The hosting account the database belongs to.
     *
     * @return BelongsTo<User, $this>
     */
    public function account(): BelongsTo
    {
        return $this->belongsTo(User::class, 'user_id');
    }

    /**
     * The PostgreSQL roles with access to it.
     *
     * @return BelongsToMany<PostgresUser, $this>
     */
    public function users(): BelongsToMany
    {
        return $this->belongsToMany(PostgresUser::class);
    }
}
