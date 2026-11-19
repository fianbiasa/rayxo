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
        Schema::create('redis_instances', function (Blueprint $table) {
            $table->id();
            // One per-account Redis cache instance; the account owns it.
            $table->foreignId('user_id')->unique()->constrained()->cascadeOnDelete();
            // The account's Redis password, encrypted at rest (cast in the model).
            $table->text('password');
            $table->unsignedInteger('max_memory_mb')->default(256);
            $table->boolean('enabled')->default(true);
            $table->timestamps();
        });
    }

    /**
     * Reverse the migrations.
     */
    public function down(): void
    {
        Schema::dropIfExists('redis_instances');
    }
};
