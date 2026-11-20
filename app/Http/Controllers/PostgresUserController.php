<?php

namespace App\Http\Controllers;

use App\Http\Requests\StorePostgresUserRequest;
use App\Models\PostgresUser;
use App\Services\PostgresService;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Gate;
use Inertia\Inertia;

/**
 * The PostgreSQL roles of the signed-in account.
 */
class PostgresUserController extends Controller
{
    public function __construct(private PostgresService $postgres) {}

    /**
     * Add a role with a generated password that owns the chosen databases.
     */
    public function store(StorePostgresUserRequest $request): RedirectResponse
    {
        $account = $request->user();

        $user = $this->postgres->createUser(
            $account,
            $request->string('name')->toString(),
            PostgresUser::newPassword(),
            $request->input('databases', []),
            $account,
        );

        Inertia::flash('toast', [
            'type' => 'success',
            'message' => __('Role :name is being created.', ['name' => $user->name]),
        ]);

        return to_route('postgres.index');
    }

    /**
     * Give a role a fresh password.
     */
    public function rotate(Request $request, PostgresUser $postgresUser): RedirectResponse
    {
        Gate::authorize('update', $postgresUser);

        $this->postgres->rotatePassword($postgresUser, $request->user());

        Inertia::flash('toast', [
            'type' => 'success',
            'message' => __('A new password for :name is being applied.', ['name' => $postgresUser->name]),
        ]);

        return back();
    }

    /**
     * Drop a role.
     */
    public function destroy(Request $request, PostgresUser $postgresUser): RedirectResponse
    {
        Gate::authorize('delete', $postgresUser);

        $this->postgres->deleteUser($postgresUser, $request->user());

        Inertia::flash('toast', [
            'type' => 'success',
            'message' => __('Role :name is being deleted.', ['name' => $postgresUser->name]),
        ]);

        return back();
    }
}
