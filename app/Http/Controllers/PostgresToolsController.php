<?php

namespace App\Http\Controllers;

use App\Models\PostgresDatabase;
use App\Rules\FilePath;
use App\Services\PostgresService;
use Illuminate\Http\RedirectResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Gate;
use Illuminate\Validation\ValidationException;
use Inertia\Inertia;
use Symfony\Component\HttpFoundation\BinaryFileResponse;

/**
 * Export and import for the signed-in account's PostgreSQL databases.
 */
class PostgresToolsController extends Controller
{
    /**
     * Where the import dialog uploads a file before it is imported; such a
     * file is removed again afterwards.
     */
    public const IMPORT_UPLOAD_PATTERN = '/^tmp\/rayxo-import-[A-Za-z0-9-]{8,64}\.(?:sql|sql\.gz|gz)$/D';

    public function __construct(private PostgresService $postgres) {}

    /**
     * Download the database as a gzipped SQL dump.
     */
    public function export(Request $request, PostgresDatabase $postgresDatabase): BinaryFileResponse|RedirectResponse
    {
        Gate::authorize('update', $postgresDatabase);

        try {
            $file = $this->postgres->export($postgresDatabase, $request->user());
        } catch (ValidationException $exception) {
            Inertia::flash('toast', ['type' => 'error', 'message' => collect($exception->errors())->flatten()->first()]);

            return back();
        }

        $response = response()->download($file['file'], $file['name'], ['Content-Type' => 'application/gzip']);
        $response->headers->set('Cache-Control', 'no-store, private');

        return $response->deleteFileAfterSend();
    }

    /**
     * Run an SQL file of the account's home (plain or gzip) in the database.
     * A file the import dialog uploaded is removed afterwards.
     */
    public function import(Request $request, PostgresDatabase $postgresDatabase): RedirectResponse
    {
        Gate::authorize('update', $postgresDatabase);

        $request->validate([
            'path' => ['required', 'string', new FilePath, 'regex:/\.(?:sql|sql\.gz|gz)$/iD'],
        ], [
            'path.regex' => __('Choose a .sql or .sql.gz file.'),
        ]);

        $path = $request->string('path')->value();

        $this->postgres->import($postgresDatabase, $path, $request->user(), remove: preg_match(self::IMPORT_UPLOAD_PATTERN, $path) === 1);

        Inertia::flash('toast', ['type' => 'success', 'message' => __('The import into :name has started.', ['name' => $postgresDatabase->name])]);

        return back();
    }
}
