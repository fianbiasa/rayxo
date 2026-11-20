<?php

namespace App\Models;

use App\Enums\DatabaseStatus;
use Database\Factories\PostgresUserFactory;
use Illuminate\Database\Eloquent\Attributes\Fillable;
use Illuminate\Database\Eloquent\Attributes\Hidden;
use Illuminate\Database\Eloquent\Factories\HasFactory;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Illuminate\Database\Eloquent\Relations\BelongsToMany;
use Illuminate\Support\Carbon;
use Illuminate\Support\Str;

/**
 * A PostgreSQL role of a hosting account, named <username>_<name>, that may
 * reach the account's databases from this server. The password is kept
 * encrypted at rest (not hashed): PostgreSQL computes its own SCRAM verifier
 * from the plaintext, and the panel shows and rotates it.
 *
 * `user_id`, `status` and the password are deliberately not mass assignable.
 *
 * @property int $id
 * @property int $user_id
 * @property string $name
 * @property string $password
 * @property DatabaseStatus $status
 * @property Carbon|null $created_at
 * @property Carbon|null $updated_at
 * @property-read User $account
 */
#[Fillable(['name'])]
#[Hidden(['password'])]
class PostgresUser extends Model
{
    /** @use HasFactory<PostgresUserFactory> */
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
            'password' => 'encrypted',
        ];
    }

    /**
     * A fresh password: 24 letters and digits, which the agent's validation
     * accepts and which is safe inside an SQL string literal.
     */
    public static function newPassword(): string
    {
        return Str::password(24, letters: true, numbers: true, symbols: false, spaces: false);
    }

    /**
     * The hosting account the role belongs to.
     *
     * @return BelongsTo<User, $this>
     */
    public function account(): BelongsTo
    {
        return $this->belongsTo(User::class, 'user_id');
    }

    /**
     * The databases the role owns.
     *
     * @return BelongsToMany<PostgresDatabase, $this>
     */
    public function databases(): BelongsToMany
    {
        return $this->belongsToMany(PostgresDatabase::class);
    }
}
