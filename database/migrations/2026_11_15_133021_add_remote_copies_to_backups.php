<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

/**
 * Backups may have a copy on the remote destination (FTP, SFTP or S3), and
 * may be there only: once the administrator keeps no copy on the server,
 * or once the server's rotation took it away.
 */
return new class extends Migration
{
    /**
     * Run the migrations.
     */
    public function up(): void
    {
        foreach (['server_backups', 'system_backups'] as $table) {
            Schema::table($table, function (Blueprint $table) {
                $table->boolean('on_server')->default(true)->after('status');
                $table->string('remote_status', 16)->nullable()->after('on_server');
                $table->text('remote_error')->nullable()->after('remote_status');
                $table->timestamp('remote_at')->nullable()->after('remote_error');
            });
        }
    }

    /**
     * Reverse the migrations.
     */
    public function down(): void
    {
        foreach (['server_backups', 'system_backups'] as $table) {
            Schema::table($table, function (Blueprint $table) {
                $table->dropColumn(['on_server', 'remote_status', 'remote_error', 'remote_at']);
            });
        }
    }
};
