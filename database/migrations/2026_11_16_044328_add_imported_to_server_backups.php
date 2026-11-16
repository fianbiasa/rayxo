<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    /**
     * Run the migrations.
     */
    public function up(): void
    {
        Schema::table('server_backups', function (Blueprint $table) {
            // A backup taken from another server (scan, transfer or import): it
            // has no run, like a hand-made one, but the automatic "keep the
            // newest few manual backups" rotation must leave it alone.
            $table->boolean('imported')->default(false)->after('backup_run_id');
        });
    }

    /**
     * Reverse the migrations.
     */
    public function down(): void
    {
        Schema::table('server_backups', function (Blueprint $table) {
            $table->dropColumn('imported');
        });
    }
};
