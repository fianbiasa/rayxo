<?php

namespace App\Http\Controllers;

use App\Http\Requests\StorePostgresDatabaseRequest;
use App\Models\PostgresDatabase;
use App\Models\PostgresUser;
use App\Services\FileManagerService;
use App\Services\PostgresService;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Gate;
use Inertia\Inertia;
use Inertia\Response;

/**
 * The signed-in account's PostgreSQL databases and roles.
 */
class PostgresController extends Controller
{
    /**
     * The longest PostgreSQL name, including the prefix.
     */
    private const MAX_NAME = 63;

    public function __construct(private PostgresService $postgres) {}

    /**
     * List the account's PostgreSQL databases and roles, with how to connect.
     */
    public function index(Request $request): Response
    {
        $account = $request->user();

        $databases = $account->postgresDatabases()
            ->with(['users' => fn ($query) => $query->orderBy('name')])
            ->orderBy('name')
            ->get()
            ->map(fn (PostgresDatabase $database): array => [
                'id' => $database->id,
                'name' => $database->name,
                'status' => $database->status->value,
                'size_bytes' => $database->size_bytes,
                'measured_at' => $database->measured_at?->toIso8601String(),
                'users' => $database->users->pluck('name')->all(),
                'import_status' => $database->import_status,
                'import_error' => $database->import_error,
                'imported_at' => $database->imported_at?->toIso8601String(),
                'created_at' => $database->created_at?->toIso8601String(),
            ]);

        $users = $account->postgresUsers()
            ->with(['databases' => fn ($query) => $query->orderBy('name')])
            ->orderBy('name')
            ->get()
            ->map(fn (PostgresUser $user): array => [
                'id' => $user->id,
                'name' => $user->name,
                'status' => $user->status->value,
                // The account owns these roles and needs the password to
                // connect its apps, so it is shown (stored encrypted at rest).
                'password' => $user->password,
                'database_ids' => $user->databases->pluck('id')->all(),
                'created_at' => $user->created_at?->toIso8601String(),
            ]);

        $prefixLength = self::MAX_NAME - strlen((string) $account->username) - 1;

        return Inertia::render('postgres/index', [
            'databases' => $databases,
            'postgres_users' => $users,
            'prefix' => $account->username.'_',
            'max_lengths' => ['database' => $prefixLength, 'user' => $prefixLength],
            'connection' => ['host' => '127.0.0.1', 'port' => 5432],
            'upload_chunk_bytes' => FileManagerService::chunkBytes(),
        ]);
    }

    /**
     * Add a database, optionally with a new role; the agent creates them in
     * the background.
     */
    public function store(StorePostgresDatabaseRequest $request): RedirectResponse
    {
        $account = $request->user();

        $newUser = null;
        if ($request->boolean('create_user')) {
            $newUser = [
                'name' => $request->string('user_name')->toString(),
                'password' => PostgresUser::newPassword(),
            ];
        }

        $database = $this->postgres->createDatabase(
            $account,
            $request->string('name')->toString(),
            $account,
            $newUser,
        );

        Inertia::flash('toast', [
            'type' => 'success',
            'message' => __('Database :name is being created.', ['name' => $database->name]),
        ]);

        return to_route('postgres.index');
    }

    /**
     * Drop a database with everything in it.
     */
    public function destroy(Request $request, PostgresDatabase $postgresDatabase): RedirectResponse
    {
        Gate::authorize('delete', $postgresDatabase);

        $this->postgres->deleteDatabase($postgresDatabase, $request->user());

        Inertia::flash('toast', [
            'type' => 'success',
            'message' => __('Database :name is being deleted.', ['name' => $postgresDatabase->name]),
        ]);

        return back();
    }
}
