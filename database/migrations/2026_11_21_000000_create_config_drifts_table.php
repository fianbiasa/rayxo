<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('config_drifts', function (Blueprint $table): void {
            $table->id();
            // The account the config belongs to, for the re-apply; null for
            // server-wide config such as the ModSecurity engine.
            $table->foreignId('user_id')->nullable()->constrained('users')->nullOnDelete();
            // The target kind, e.g. nginx-vhost / php-pool / dns-zone /
            // mail-sieve / waf-engine.
            $table->string('kind');
            // The on-disk path (or logical name) that drifted; unique so a
            // scan upserts one row per target.
            $table->string('path')->unique();
            // The agent apply action that re-applies this target (what the
            // "rewrite" button dispatches) and the task payload it needs.
            $table->string('apply_action');
            $table->text('payload');
            $table->boolean('missing')->default(false);
            $table->text('diff')->nullable();
            $table->timestamp('checked_at');
            $table->timestamps();
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('config_drifts');
    }
};
