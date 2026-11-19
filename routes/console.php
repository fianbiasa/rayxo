<?php

use App\Models\BandwidthUsage;
use App\Models\DomainBandwidth;
use App\Models\ResourceUsage;
use App\Models\UsageSnapshot;
use App\Models\WebmailSession;
use Illuminate\Foundation\Inspiring;
use Illuminate\Support\Facades\Artisan;
use Illuminate\Support\Facades\Schedule;

Artisan::command('inspire', function () {
    $this->comment(Inspiring::quote());
})->purpose('Display an inspiring quote');

// The frequent jobs below finish in seconds; give their overlap locks a short
// expiry so a run that is killed (a deploy or an OOM mid-run) cannot leave a
// stuck lock that silently skips every later run for the default 24 hours. The
// expiry is a few times the job's own runtime but well under its interval, so a
// legitimate slow run is still protected from overlapping. The long hourly and
// daily jobs below (backups especially) keep the default so they never overlap.
Schedule::command('rayxo:collect-usage')->everyFifteenMinutes()->withoutOverlapping(30);
Schedule::command('rayxo:collect-bandwidth')->everyFifteenMinutes()->withoutOverlapping(30);
Schedule::command('rayxo:close-phpmyadmin-sessions')->everyFiveMinutes()->withoutOverlapping(10);
Schedule::command('rayxo:sync-send-limits')->everyFiveMinutes()->withoutOverlapping(10);
Schedule::command('rayxo:check-alerts')->everyFiveMinutes()->withoutOverlapping(10);
Schedule::command('rayxo:collect-resources')->everyFiveMinutes()->withoutOverlapping(10);
Schedule::command('rayxo:clean-transfers')->hourly()->withoutOverlapping();
Schedule::command('rayxo:run-backups')->hourly()->withoutOverlapping();
Schedule::command('rayxo:measure-domain-disk')->hourly()->withoutOverlapping();
Schedule::command('model:prune', ['--model' => [WebmailSession::class, BandwidthUsage::class, DomainBandwidth::class, UsageSnapshot::class, ResourceUsage::class]])->hourly()->withoutOverlapping();
Schedule::command('rayxo:recount-ftp-quotas')->dailyAt('03:40')->withoutOverlapping();
Schedule::command('rayxo:sync-mail-ssl')->dailyAt('04:20')->withoutOverlapping();
Schedule::command('rayxo:sync-ssl-expiry')->dailyAt('04:30')->withoutOverlapping();
