<?php

namespace App\Http\Controllers\Admin;

use App\Http\Controllers\Controller;
use App\Services\ServerSettingsService;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Inertia\Inertia;
use Inertia\Response;

/**
 * The admin Antivirus page: ClamAV's status and scan modes, an on-demand scan,
 * and the quarantine of infected files found by any scan (view, restore, delete).
 */
class AntivirusController extends Controller
{
    public function __construct(private ServerSettingsService $settings) {}

    public function index(): Response
    {
        return Inertia::render('admin/antivirus/index', [
            'status' => $this->settings->clamavStatus(),
            'quarantine' => $this->settings->quarantineList(),
        ]);
    }

    /**
     * Turn ClamAV and its scan modes on or off.
     */
    public function update(Request $request): RedirectResponse
    {
        $request->validate([
            'enabled' => ['required', 'boolean'],
            'email' => ['required', 'boolean'],
            'on_access' => ['required', 'boolean'],
            'scheduled' => ['required', 'boolean'],
        ]);

        // The sub-modes only apply while ClamAV itself is on.
        $enabled = $request->boolean('enabled');
        $this->settings->setClamav([
            'enabled' => $enabled,
            'email' => $enabled && $request->boolean('email'),
            'on_access' => $enabled && $request->boolean('on_access'),
            'scheduled' => $enabled && $request->boolean('scheduled'),
        ], $request->user());

        Inertia::flash('toast', [
            'type' => 'success',
            'message' => $enabled
                ? __('ClamAV is being turned on; downloading signatures can take a few minutes.')
                : __('ClamAV is off.'),
        ]);

        return back();
    }

    /**
     * Scan a path under /home now. Infected files are moved to quarantine.
     */
    public function scan(Request $request): RedirectResponse
    {
        $validated = $request->validate([
            // Exactly /home or a path under it (not /homexyz), and no "..".
            'path' => ['required', 'string', 'max:255', 'regex:#^/home(/|$)#', 'not_regex:/\.\./'],
        ]);

        $result = $this->settings->scanClamav($validated['path'], $request->user());
        $count = count($result['infected']);

        Inertia::flash('toast', [
            'type' => $result['clean'] ? 'success' : 'warning',
            'message' => $result['clean']
                ? __('Scan clean: no viruses in :path.', ['path' => $validated['path']])
                : __(':count infected file(s) in :path moved to quarantine.', ['count' => $count, 'path' => $validated['path']]),
        ]);

        return back();
    }

    /**
     * Restore a quarantined file to where it came from.
     */
    public function restore(Request $request): RedirectResponse
    {
        $validated = $request->validate(['id' => ['required', 'string', 'max:64']]);

        $this->settings->restoreQuarantine($validated['id'], $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('File restored from quarantine.')]);

        return back();
    }

    /**
     * Permanently delete a quarantined file.
     */
    public function delete(Request $request): RedirectResponse
    {
        $validated = $request->validate(['id' => ['required', 'string', 'max:64']]);

        $this->settings->deleteQuarantine($validated['id'], $request->user());

        Inertia::flash('toast', ['type' => 'success', 'message' => __('File deleted from quarantine.')]);

        return back();
    }
}
