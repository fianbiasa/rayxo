<?php

namespace App\Http\Controllers\Api\V1;

use App\Enums\Role;
use App\Http\Controllers\Controller;
use App\Models\BandwidthUsage;
use App\Models\User;
use Illuminate\Database\Eloquent\Builder;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;

/**
 * What every hosting account the key may see uses of its package, in one
 * answer: disk now and bandwidth this calendar month, with their limits
 * (null is no limit). A billing system such as WHMCS reads it to show
 * usage and bill overage.
 */
class UsageController extends Controller
{
    public function __invoke(Request $request): JsonResponse
    {
        /** @var User $actor */
        $actor = $request->user();

        $accounts = User::query()
            ->with(['package', 'diskUsage'])
            ->where('role', Role::User)
            ->when(! $actor->isAdmin(), fn (Builder $query) => $query->where('owner_id', $actor->id))
            ->orderBy('username')
            ->get();

        $bandwidth = BandwidthUsage::query()
            ->where('date', '>=', now()->startOfMonth()->toDateString())
            ->whereIn('user_id', $accounts->pluck('id'))
            ->groupBy('user_id')
            ->selectRaw('user_id, sum(web_bytes + mail_bytes + ftp_bytes) as total')
            ->pluck('total', 'user_id');

        return response()->json(['data' => $accounts->map(fn (User $account): array => [
            'username' => $account->username,
            'status' => $account->status->value,
            'disk_mb' => intdiv((int) ($account->diskUsage->disk_kb ?? 0), 1024),
            'disk_limit_mb' => $account->package?->disk_mb,
            'bandwidth_mb' => intdiv((int) ($bandwidth[$account->id] ?? 0), 1048576),
            'bandwidth_limit_mb' => $account->package?->bandwidth_mb,
        ])->values()]);
    }
}
