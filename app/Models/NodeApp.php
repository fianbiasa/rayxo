<?php

namespace App\Models;

use App\Enums\NodeAppStatus;
use Illuminate\Database\Eloquent\Attributes\Fillable;
use Illuminate\Database\Eloquent\Attributes\Hidden;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Illuminate\Support\Carbon;

/**
 * A Node.js application a domain runs instead of PHP: the agent runs the
 * account's own built app as a systemd service (as the account's user, in its
 * slice) on a localhost port, and Nginx reverse-proxies the domain to it. The
 * account builds the app over SSH; the panel only runs the built output.
 *
 * The environment is stored encrypted (it holds the app's secrets) and sent to
 * the agent inside the (encrypted) task payload.
 *
 * `domain_id`, `port`, `enabled` and `status` are deliberately not mass
 * assignable.
 *
 * @property int $id
 * @property int $domain_id
 * @property string $entry
 * @property array<string, string>|null $env
 * @property int $port
 * @property bool $enabled
 * @property NodeAppStatus $status
 * @property Carbon|null $created_at
 * @property Carbon|null $updated_at
 * @property-read Domain $domain
 */
#[Fillable(['entry', 'env'])]
#[Hidden(['env'])]
class NodeApp extends Model
{
    /**
     * The first and last localhost port handed to a Node.js app. The agent
     * keeps the panel's own port out of this range (panelport.Reserved).
     */
    public const int PORT_MIN = 40000;

    public const int PORT_MAX = 49999;

    /**
     * Pick a port no other Node.js app holds. Call this inside a transaction
     * that locked the table (see NodeAppService), so two accounts enabled at
     * once never get the same port.
     *
     * @param  list<int>  $taken  Ports already in use.
     */
    public static function firstFreePort(array $taken): int
    {
        $used = array_flip($taken);

        for ($port = self::PORT_MIN; $port <= self::PORT_MAX; $port++) {
            if (! isset($used[$port])) {
                return $port;
            }
        }

        throw new \RuntimeException('No free Node.js app port is left.');
    }

    /**
     * Get the attributes that should be cast.
     *
     * @return array<string, string>
     */
    protected function casts(): array
    {
        return [
            'env' => 'encrypted:array',
            'port' => 'integer',
            'enabled' => 'boolean',
            'status' => NodeAppStatus::class,
        ];
    }

    /**
     * The domain this app serves.
     *
     * @return BelongsTo<Domain, $this>
     */
    public function domain(): BelongsTo
    {
        return $this->belongsTo(Domain::class);
    }
}
