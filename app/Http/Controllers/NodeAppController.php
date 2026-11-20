<?php

namespace App\Http\Controllers;

use App\Http\Requests\NodeAppRequest;
use App\Models\Domain;
use App\Services\NodeAppService;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Gate;
use Inertia\Inertia;

/**
 * Turns a domain's Node.js app on and off from its web settings page.
 */
class NodeAppController extends Controller
{
    public function __construct(private NodeAppService $nodeApps) {}

    /**
     * Turn the domain's Node.js app on.
     */
    public function store(NodeAppRequest $request, Domain $domain): RedirectResponse
    {
        $this->nodeApps->enable($domain, $request->entry(), $request->env(), $request->user());

        return $this->applied(__('The Node.js app of :domain is being started.', ['domain' => $domain->display_name]));
    }

    /**
     * Change the running app's entry and environment.
     */
    public function update(NodeAppRequest $request, Domain $domain): RedirectResponse
    {
        $this->nodeApps->update($domain, $request->entry(), $request->env(), $request->user());

        return $this->applied(__('The Node.js app of :domain is being updated.', ['domain' => $domain->display_name]));
    }

    /**
     * Turn the domain's Node.js app off.
     */
    public function destroy(Request $request, Domain $domain): RedirectResponse
    {
        Gate::authorize('update', $domain);

        $this->nodeApps->disable($domain, $request->user());

        return $this->applied(__('The Node.js app of :domain is being turned off.', ['domain' => $domain->display_name]));
    }

    /**
     * Restart the app after the account rebuilt it.
     */
    public function restart(Request $request, Domain $domain): RedirectResponse
    {
        Gate::authorize('update', $domain);

        $this->nodeApps->restart($domain, $request->user());

        return $this->applied(__('The Node.js app of :domain is being restarted.', ['domain' => $domain->display_name]));
    }

    /**
     * Back to the page, with a toast saying what happens next.
     */
    private function applied(string $message): RedirectResponse
    {
        Inertia::flash('toast', ['type' => 'success', 'message' => $message]);

        return back();
    }
}
