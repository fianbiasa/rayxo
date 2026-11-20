<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    /**
     * Run the migrations: the per-website ModSecurity (WAF) override. Websites
     * start at "inherit", so they follow the server-wide default until an
     * account overrides it. Adding a column never rebuilds the table, so the
     * views on it stay.
     */
    public function up(): void
    {
        Schema::table('domains', function (Blueprint $table) {
            $table->string('modsecurity_mode', 16)->default('inherit');
        });
    }

    /**
     * Reverse the migrations.
     */
    public function down(): void
    {
        Schema::table('domains', function (Blueprint $table) {
            $table->dropColumn('modsecurity_mode');
        });
    }
};
