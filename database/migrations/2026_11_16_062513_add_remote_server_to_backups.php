<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    /**
     * Run the migrations.
     */
    public function up(): void
    {
        // The server folder a copy on the remote destination lives under, kept
        // per copy so a panel restored on a differently-named server still
        // finds, fetches and rotates the copies the old server uploaded.
        $host = parse_url((string) config('app.url'), PHP_URL_HOST) ?: null;

        foreach (['server_backups', 'system_backups'] as $table) {
            Schema::table($table, function (Blueprint $blueprint): void {
                $blueprint->string('remote_server')->nullable()->after('remote_at');
            });

            // Existing copies live under this server's current folder.
            if ($host !== null) {
                DB::table($table)->where('remote_status', 'uploaded')->update(['remote_server' => $host]);
            }
        }
    }

    /**
     * Reverse the migrations.
     */
    public function down(): void
    {
        foreach (['server_backups', 'system_backups'] as $table) {
            Schema::table($table, function (Blueprint $blueprint): void {
                $blueprint->dropColumn('remote_server');
            });
        }
    }
};
