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
        Schema::create('node_apps', function (Blueprint $table) {
            $table->id();
            // One Node.js app per domain; it goes when the domain goes.
            $table->foreignId('domain_id')->unique()->constrained()->cascadeOnDelete();
            // The script node runs, relative to the domain's document root.
            $table->string('entry');
            // The app's own environment variables, encrypted at rest (cast in
            // the model), sent to the agent to write as the app's EnvironmentFile.
            $table->text('env')->nullable();
            // The localhost port Nginx reverse-proxies the domain to; one per
            // app (see NodeApp::PORT_MIN / PORT_MAX).
            $table->unsignedInteger('port')->unique();
            $table->boolean('enabled')->default(true);
            // pending while the agent is starting the app, active once it runs,
            // failed when the agent could not start it.
            $table->string('status')->default('pending');
            $table->timestamps();
        });
    }

    /**
     * Reverse the migrations.
     */
    public function down(): void
    {
        Schema::dropIfExists('node_apps');
    }
};
