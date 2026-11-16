<?php

namespace App\Http\Controllers\Admin;

use App\Http\Controllers\Controller;
use App\Http\Requests\Admin\UpdateRemoteBackupRequest;
use App\Models\ServerBackup;
use App\Models\SystemBackup;
use App\Services\BackupService;
use App\Services\RemoteBackupService;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Gate;
use Illuminate\Validation\Rule;
use Illuminate\Validation\ValidationException;
use Inertia\Inertia;

/**
 * The remote destination backups are copied to, and bringing backups back
 * from it.
 */
class RemoteBackupController extends Controller
{
    public function __construct(private RemoteBackupService $remote) {}

    /**
     * Save the destination; when copies are on, try it at once.
     */
    public function update(UpdateRemoteBackupRequest $request): RedirectResponse
    {
        $validated = $request->validated();
        $texts = array_intersect_key($validated, array_flip(['host', 'user', 'password', 'host_key', 'endpoint', 'region', 'bucket', 'access_key', 'secret_key']));

        $this->remote->save([
            ...$validated,
            ...array_map(fn (mixed $value): string => (string) ($value ?? ''), $texts),
            'enabled' => (bool) $validated['enabled'],
            'port' => isset($validated['port']) ? (int) $validated['port'] : null,
            'keep' => (int) $validated['keep'],
            'keep_local' => (bool) $validated['keep_local'],
            'path' => trim((string) ($validated['path'] ?? '')),
        ], $request->user());

        if (! $validated['enabled']) {
            Inertia::flash('toast', ['type' => 'success', 'message' => __('Saved. Backups are not copied to the remote destination.')]);

            return back();
        }

        try {
            $this->remote->test();

            Inertia::flash('toast', ['type' => 'success', 'message' => __('Saved. The remote destination took a test file, so backups will be copied there.')]);
        } catch (ValidationException $exception) {
            Inertia::flash('toast', ['type' => 'error', 'message' => __('Saved, but the remote destination did not take a test file: :message', ['message' => collect($exception->errors())->flatten()->first()])]);
        }

        return back();
    }

    /**
     * The host key an SFTP server presents, for the administrator to check
     * and accept.
     */
    public function hostKey(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'host' => ['required', 'string', 'max:253', 'regex:/^[A-Za-z0-9]([A-Za-z0-9.:-]{0,251}[A-Za-z0-9])?$/'],
            'port' => ['nullable', 'integer', 'between:1,65535'],
        ], ['host.regex' => __('Enter a host name or IP address.')]);

        Inertia::flash('remote_host_key', $this->remote->hostKey($validated['host'], isset($validated['port']) ? (int) $validated['port'] : null));

        return back();
    }

    /**
     * Try the saved destination.
     */
    public function test(): RedirectResponse
    {
        $this->remote->test();

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The remote destination took a test file and deleted it again.')]);

        return back();
    }

    /**
     * Bring a backup of an account that is only on the destination back to
     * the server.
     */
    public function fetch(Request $request, ServerBackup $serverBackup): RedirectResponse
    {
        Gate::authorize('use', $serverBackup);

        $this->remote->queueFetch($serverBackup, $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Taking :name back from the remote destination. This page shows when it is on the server.', ['name' => $serverBackup->name])]);

        return back();
    }

    /**
     * Bring a backup of the panel that is only on the destination back to
     * the server.
     */
    public function fetchSystem(Request $request, SystemBackup $systemBackup): RedirectResponse
    {
        try {
            $this->remote->queueFetch($systemBackup, $request->user());
        } catch (ValidationException $exception) {
            throw ValidationException::withMessages(['system' => collect($exception->errors())->flatten()->first()]);
        }

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Taking :name back from the remote destination. This page shows when it is on the server.', ['name' => $systemBackup->name])]);

        return back();
    }

    /**
     * Take the newest backup of every account back from the destination,
     * where it is there only.
     */
    public function fetchNewest(Request $request): RedirectResponse
    {
        $queued = $this->remote->fetchNewest($request->user());

        Inertia::flash('toast', $queued > 0
            ? ['type' => 'success', 'message' => __('Taking the newest backup of :count account(s) back from the remote destination. Restore every account once they are on the server.', ['count' => $queued])]
            : ['type' => 'error', 'message' => __('No account has a newest backup that is only on the remote destination.')]);

        return back();
    }

    /**
     * Take a backup the panel does not know from a server's folder on the
     * destination.
     */
    public function take(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            'server' => ['required', 'string', 'max:253', 'regex:/^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/'],
            'kind' => ['required', Rule::in(['account', 'system'])],
            'username' => ['exclude_unless:kind,account', 'required', 'string', 'max:32'],
            'name' => ['required', 'string', 'regex:/^'.BackupService::NAME_PATTERN.'$/'],
        ]);

        $this->remote->queueTake($validated['server'], $validated['kind'], $validated['username'] ?? null, $validated['name'], $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('Taking :name from the remote destination. It shows in the list once it is on the server.', ['name' => $validated['name']])]);

        return back();
    }
}
