<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    /**
     * Run the migrations.
     *
     * Which PostgreSQL roles own (have full access to) which databases.
     */
    public function up(): void
    {
        Schema::create('postgres_database_postgres_user', function (Blueprint $table) {
            $table->foreignId('postgres_database_id')->constrained()->cascadeOnDelete();
            $table->foreignId('postgres_user_id')->constrained()->cascadeOnDelete();

            $table->primary(['postgres_database_id', 'postgres_user_id'], 'postgres_db_user_primary');
        });
    }

    /**
     * Reverse the migrations.
     */
    public function down(): void
    {
        Schema::dropIfExists('postgres_database_postgres_user');
    }
};
