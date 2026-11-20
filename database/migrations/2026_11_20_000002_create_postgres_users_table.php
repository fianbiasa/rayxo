<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    /**
     * Run the migrations.
     *
     * The PostgreSQL roles of hosting accounts, named <username>_<name>. The
     * password is kept encrypted at rest (cast in the model), not hashed,
     * because PostgreSQL computes its own SCRAM verifier from the plaintext;
     * the panel shows and rotates it.
     */
    public function up(): void
    {
        Schema::create('postgres_users', function (Blueprint $table) {
            $table->id();
            $table->foreignId('user_id')->constrained()->cascadeOnDelete();
            $table->string('name', 63)->unique();
            $table->text('password');
            $table->string('status', 16);
            $table->timestamps();
        });
    }

    /**
     * Reverse the migrations.
     */
    public function down(): void
    {
        Schema::dropIfExists('postgres_users');
    }
};
