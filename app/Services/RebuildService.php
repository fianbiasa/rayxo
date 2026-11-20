<?php

namespace App\Services;

use App\Models\User;

/**
 * Writes the server's configuration again from the panel's database, like
 * DirectAdmin's rewrite_confs: every website (Nginx, Apache and its PHP
 * pool), every account's disk quota and crontab, every FTP login's folder
 * every DNS zone, every mail domain and the daily send limits. Each task renders the full state, so a rebuild changes nothing
 * on a server that matches the database, however often it runs.
 */
class RebuildService
{
    public function __construct(
        private DomainService $domains,
        private QuotaService $quotas,
        private CronJobService $cronJobs,
        private FtpService $ftp,
        private PhpVersionService $phpVersions,
        private DnsService $dns,
        private MailService $mail,
        private MailRulesService $mailRules,
        private MailSendLimitService $sendLimits,
        private NodeAppService $nodeApps,
    ) {}

    /**
     * Queue the whole rebuild; the queue worker and the agent do the rest.
     *
     * @return array{node_apps: int, websites: int, quotas: int, crontabs: int, ftp_folders: int, dns_zones: int, mail_domains: int}
     */
    public function queueAll(?User $actor = null): array
    {
        $this->phpVersions->forget();

        // Node.js apps first, so each app's unit is re-rendered and its socket
        // recreated before the website rebuild re-points Nginx at that socket.
        $nodeApps = $this->nodeApps->reapplyAll($actor);

        // Mailbox filters and automatic replies follow their mail domains.
        $mailDomains = $this->mail->reapplyAll($actor);
        $this->mailRules->reapplyAll($actor);
        $this->sendLimits->sync($actor, force: true);

        return [
            'node_apps' => $nodeApps,
            'websites' => $this->domains->reapplyAll($actor),
            'quotas' => $this->quotas->applyAll($actor),
            'crontabs' => $this->cronJobs->reapplyAll($actor),
            'ftp_folders' => $this->ftp->reapplyAll($actor),
            'dns_zones' => $this->dns->syncAll($actor),
            'mail_domains' => $mailDomains,
        ];
    }
}
