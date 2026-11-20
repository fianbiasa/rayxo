<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    /**
     * Run the migrations.
     *
     * The PostgreSQL databases of hosting accounts, named <username>_<name>.
     */
    public function up(): void
    {
        Schema::create('postgres_databases', function (Blueprint $table) {
            $table->id();
            $table->foreignId('user_id')->constrained()->cascadeOnDelete();
            $table->string('name', 63)->unique();
            $table->string('status', 16);
            $table->unsignedBigInteger('size_bytes')->nullable();
            $table->timestamp('measured_at')->nullable();
            // How the last import went: running, done or failed, with the error.
            $table->string('import_status', 16)->nullable();
            $table->text('import_error')->nullable();
            $table->timestamp('imported_at')->nullable();
            $table->timestamps();
        });
    }

    /**
     * Reverse the migrations.
     */
    public function down(): void
    {
        Schema::dropIfExists('postgres_databases');
    }
};
