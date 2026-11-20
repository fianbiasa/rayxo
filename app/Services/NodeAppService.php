<?php

namespace App\Services;

use App\Enums\AccountStatus;
use App\Enums\NodeAppStatus;
use App\Models\Domain;
use App\Models\NodeApp;
use App\Models\User;
use Illuminate\Support\Facades\DB;
use Illuminate\Validation\ValidationException;

/**
 * Turns a domain's Node.js app on and off. The panel record is written right
 * away and the work on the server is queued for the agent: nodeapp.provision /
 * nodeapp.deprovision run the account's built app as a systemd service on a
 * localhost port, and the domain's website is re-applied so Nginx proxies the
 * domain to that app (or back to Apache/PHP when the app is turned off).
 *
 * The account builds its own app over SSH; the panel only runs the built
 * output. The environment is stored encrypted so it can be shown to the
 * account and sent to the agent inside the (encrypted) task payload.
 */
class NodeAppService
{
    public function __construct(
        private TaskDispatcher $tasks,
        private AuditLogger $audit,
        private DomainService $domains,
    ) {}

    /**
     * Turn on, or update, the domain's Node.js app and have the agent run it.
     * The app starts first, then the domain is re-applied so Nginx proxies to
     * it. Calling it again updates the entry and environment.
     *
     * @param  array<string, string>  $env
     *
     * @throws ValidationException
     */
    public function enable(Domain $domain, string $entry, array $env, User $actor): NodeApp
    {
        $this->ensureCanRunNodeApp($domain);

        return DB::transaction(function () use ($domain, $entry, $env, $actor): NodeApp {
            $app = $domain->nodeApp()->lockForUpdate()->first() ?? new NodeApp;
            $app->domain()->associate($domain);

            if (! $app->exists) {
                $app->port = $this->allocatePort();
            }

            $app->entry = $entry;
            $app->env = $env === [] ? null : $env;
            $app->enabled = true;
            $app->status = NodeAppStatus::Pending;
            $app->save();

            $this->dispatchProvision($domain, $app, $actor);
            $this->domains->reapplyWebsite($domain, $actor);

            $this->audit->record('nodeapp.enabled', $domain, [
                'domain' => $domain->name,
                'entry' => $entry,
            ], $actor);

            return $app;
        });
    }

    /**
     * Change the running app's entry and environment, then restart it.
     *
     * @param  array<string, string>  $env
     *
     * @throws ValidationException
     */
    public function update(Domain $domain, string $entry, array $env, User $actor): NodeApp
    {
        $app = $domain->nodeApp;

        if ($app === null || ! $app->enabled) {
            throw ValidationException::withMessages(['node_app' => __('Turn the Node.js app on first.')]);
        }

        return DB::transaction(function () use ($domain, $app, $entry, $env, $actor): NodeApp {
            $app->forceFill([
                'entry' => $entry,
                'env' => $env === [] ? null : $env,
                'status' => NodeAppStatus::Pending,
            ])->save();

            $this->dispatchProvision($domain, $app, $actor);

            $this->audit->record('nodeapp.env_changed', $domain, [
                'domain' => $domain->name,
                'entry' => $entry,
            ], $actor);

            return $app;
        });
    }

    /**
     * Turn off the domain's Node.js app: the domain goes back to Apache/PHP
     * first, then the agent stops and removes the app. The panel record is
     * deleted.
     */
    public function disable(Domain $domain, User $actor): void
    {
        $app = $domain->nodeApp;

        if ($app === null) {
            return;
        }

        DB::transaction(function () use ($domain, $app, $actor): void {
            $app->forceFill(['enabled' => false])->save();

            // Send the domain back to Apache/PHP before the app is stopped, so
            // no request reaches a port that is about to go.
            $this->domains->reapplyWebsite($domain, $actor);
            $this->tasks->dispatch('nodeapp.deprovision', [
                'username' => $domain->account->username,
                'domain' => $domain->name,
            ], $domain->account, $actor);

            $app->delete();

            $this->audit->record('nodeapp.disabled', $domain, ['domain' => $domain->name], $actor);
        });
    }

    /**
     * Restart the app after the account rebuilt it over SSH.
     *
     * @throws ValidationException
     */
    public function restart(Domain $domain, User $actor): void
    {
        $app = $domain->nodeApp;

        if ($app === null || ! $app->enabled) {
            throw ValidationException::withMessages(['node_app' => __('Turn the Node.js app on first.')]);
        }

        $this->tasks->dispatch('nodeapp.restart', [
            'username' => $domain->account->username,
            'domain' => $domain->name,
        ], $domain->account, $actor);

        $this->audit->record('nodeapp.restarted', $domain, ['domain' => $domain->name], $actor);
    }

    /**
     * Stop and remove every Node.js app of an account being deleted. Runs
     * before account.delete so the apps are stopped before the Linux user goes.
     */
    public function cleanup(User $account, User $actor): void
    {
        $account->domains()
            ->whereHas('nodeApp')
            ->get()
            ->each(fn (Domain $domain) => $this->tasks->dispatch('nodeapp.deprovision', [
                'username' => $account->username,
                'domain' => $domain->name,
            ], $account, $actor));
    }

    private function dispatchProvision(Domain $domain, NodeApp $app, ?User $actor): void
    {
        $this->tasks->dispatch('nodeapp.provision', [
            'username' => $domain->account->username,
            'domain' => $domain->name,
            'entry' => $app->entry,
            'env' => (object) ($app->env ?? []),
            'port' => $app->port,
        ], $domain->account, $actor);
    }

    /**
     * A port in NodeApp's range that no other app holds. Call inside a
     * transaction that locked the node_apps rows.
     */
    private function allocatePort(): int
    {
        $taken = array_map(intval(...), NodeApp::query()->lockForUpdate()->pluck('port')->all());

        return NodeApp::firstFreePort(array_values($taken));
    }

    /**
     * @throws ValidationException
     */
    private function ensureCanRunNodeApp(Domain $domain): void
    {
        if ($domain->account->status !== AccountStatus::Active) {
            throw ValidationException::withMessages(['node_app' => __('The account must be active to run a Node.js app.')]);
        }

        if (! $domain->hasOwnFolder()) {
            throw ValidationException::withMessages(['node_app' => __('Only a domain or subdomain with its own folder can run a Node.js app.')]);
        }
    }
}
