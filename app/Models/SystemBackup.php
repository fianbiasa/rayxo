<?php

namespace App\Models;

use App\Enums\RemoteCopyStatus;
use App\Enums\ServerBackupStatus;
use Database\Factories\SystemBackupFactory;
use Illuminate\Database\Eloquent\Attributes\Fillable;
use Illuminate\Database\Eloquent\Factories\HasFactory;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Support\Carbon;

/**
 * A backup of the panel itself: its database, its configuration, the DKIM
 * keys and the files people uploaded. The agent keeps it for root alone;
 * setup.sh brings a panel back from it on a new server.
 *
 * @property int $id
 * @property string $name
 * @property ServerBackupStatus $status
 * @property bool $on_server Whether the file is on this server; false once only the remote copy is left.
 * @property RemoteCopyStatus|null $remote_status Its copy on the remote destination, if one was made.
 * @property string|null $remote_error
 * @property Carbon|null $remote_at When the copy reached the remote destination.
 * @property int|null $size_bytes
 * @property string|null $error
 * @property Carbon|null $finished_at
 * @property Carbon|null $created_at
 * @property Carbon|null $updated_at
 */
#[Fillable(['name', 'status'])]
class SystemBackup extends Model
{
    /** @use HasFactory<SystemBackupFactory> */
    use HasFactory;

    /**
     * A new backup is on this server, and has no copy elsewhere yet.
     *
     * @var array<string, mixed>
     */
    protected $attributes = [
        'on_server' => true,
    ];

    /**
     * Get the attributes that should be cast.
     *
     * @return array<string, string>
     */
    protected function casts(): array
    {
        return [
            'status' => ServerBackupStatus::class,
            'on_server' => 'boolean',
            'remote_status' => RemoteCopyStatus::class,
            'remote_at' => 'datetime',
            'size_bytes' => 'integer',
            'finished_at' => 'datetime',
        ];
    }
}
