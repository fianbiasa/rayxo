<?php

namespace App\Console\Commands;

use App\Services\DomainService;
use App\Services\NodeAppService;
use App\Services\PhpVersionService;
use Illuminate\Console\Command;

/**
 * Re-applies the web server configuration of every website. setup.sh runs
 * it after each upgrade, so websites set up by an older release pick up
 * changed templates (new locations, HTTPS, security fixes). It also forgets
 * the cached PHP versions, so versions the upgrade installed can be chosen
 * right away.
 */
class RebuildWebCommand extends Command
{
    /**
     * @var string
     */
    protected $signature = 'rayxo:rebuild-web';

    /**
     * @var string
     */
    protected $description = 'Queue the web server configuration of every active website again, e.g. after an upgrade';

    public function handle(DomainService $domains, PhpVersionService $phpVersions, NodeAppService $nodeApps): int
    {
        $phpVersions->forget();

        // Re-provision Node.js apps first, so each app's unit is re-rendered and
        // its socket recreated before the website rebuild re-points Nginx at that
        // socket. An upgrade that changed how apps are served (e.g. a TCP port to
        // a unix socket) would otherwise leave the socket missing.
        $nodeApps = $nodeApps->reapplyAll();

        $queued = $domains->reapplyAll();

        $this->components->info($queued === 1
            ? '1 website queued for reconfiguration.'
            : "{$queued} websites queued for reconfiguration.");

        if ($nodeApps > 0) {
            $this->components->info($nodeApps === 1
                ? '1 Node.js app queued for re-provisioning.'
                : "{$nodeApps} Node.js apps queued for re-provisioning.");
        }

        return self::SUCCESS;
    }
}
