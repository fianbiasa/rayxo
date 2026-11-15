<?php

namespace App\Support;

use App\Enums\DomainType;
use App\Rules\CronSchedule;
use App\Rules\ValidDomain;
use App\Services\BackupService;
use Illuminate\Validation\ValidationException;

/**
 * A backup another panel made (cPanel or DirectAdmin), as the agent read it
 * (backup.import_inspect), and the RAYXO backup it becomes: the settings an
 * account is made from, the dumps it holds under their names here, and
 * where each part of its home goes in the home here. Each panel's class
 * reads what that panel keeps of the account; turning that into an
 * account here is the same for both. What cannot come along is noted for
 * the administrator.
 */
abstract class ForeignBackup
{
    /**
     * Folders of a home here that belong to the panel.
     */
    public const RESERVED = ['domains', 'imap', 'backups', '.rayxo-backup', '.rayxo-restore'];

    private const MAILBOX_HASH = '#^\$6\$[./0-9A-Za-z]{1,16}\$[./0-9A-Za-z]{86}$#D';

    private const DATABASE_HASH = '/^\*[0-9A-F]{40}$/D';

    private const LOCAL_PART = '/^[a-z0-9](?:[a-z0-9._+-]{0,62}[a-z0-9])?$/D';

    /**
     * @param  array<string, string>  $files  The small files read, by their name in the backup.
     * @param  list<array{name: string, size: int}>  $dumps
     * @param  list<array{name: string, type: string, bytes: int}>  $home
     */
    final protected function __construct(
        public readonly string $top,
        public readonly string $user,
        protected readonly array $files,
        protected readonly array $dumps,
        protected readonly array $home,
    ) {}

    /**
     * Read what the agent found in a backup of another panel.
     *
     * @param  array<string, mixed>  $inspection
     *
     * @throws ValidationException
     */
    public static function read(array $inspection): self
    {
        $files = array_filter((array) ($inspection['files'] ?? []), fn (mixed $content, mixed $name): bool => is_string($name) && is_string($content), ARRAY_FILTER_USE_BOTH);
        $class = ($inspection['kind'] ?? null) === 'directadmin' ? DirectAdminBackup::class : CpanelBackup::class;
        $user = $class::userIn($files);

        if ($user === null || ! is_string($inspection['top'] ?? null)) {
            throw ValidationException::withMessages(['backup' => __('This file is not a cPanel or DirectAdmin backup.')]);
        }

        $dumps = [];
        foreach ((array) ($inspection['databases'] ?? []) as $dump) {
            if (is_array($dump) && is_string($dump['name'] ?? null)) {
                $dumps[] = ['name' => $dump['name'], 'size' => (int) ($dump['size'] ?? 0)];
            }
        }

        $home = [];
        foreach ((array) ($inspection['home'] ?? []) as $entry) {
            if (is_array($entry) && is_string($entry['name'] ?? null)) {
                $home[] = ['name' => $entry['name'], 'type' => (string) ($entry['type'] ?? ''), 'bytes' => (int) ($entry['bytes'] ?? 0)];
            }
        }

        return new $class($inspection['top'], $user, $files, $dumps, $home);
    }

    /**
     * The kind of backup, as the agent names it: cpanel or directadmin.
     */
    abstract public function kind(): string;

    /**
     * The panel that made the backup, as people write it.
     */
    abstract public function panel(): string;

    /**
     * The contact email address of the account in the panel.
     */
    abstract public function contactEmail(): ?string;

    /**
     * The username in the panel, from what it keeps of the account; null
     * when the files are not what that panel keeps.
     *
     * @param  array<string, string>  $files
     */
    abstract protected static function userIn(array $files): ?string;

    /**
     * The websites, with the folder each had relative to the home (null
     * for a parked domain or pointer). The main domain comes first.
     *
     * @param  list<string>  $phpVersions
     * @param  list<string>  $notes
     * @return list<array{name: string, type: string, parent: string|null, pointer_mode: string|null, php_version: string|null, docroot: string|null, is_primary: bool}>
     *
     * @throws ValidationException
     */
    abstract protected function domains(array $phpVersions, array &$notes): array;

    /**
     * The mailboxes, with the hash of their password and their quota in
     * bytes (null: no limit).
     *
     * @return list<array{domain: string, local_part: string, hash: string, quota_bytes: int|null}>
     */
    abstract protected function mailboxEntries(): array;

    /**
     * Where the mail of a mailbox is in the home of the backup, and where
     * it goes here.
     *
     * @return array{from: string, to: string}
     */
    abstract protected function mailRule(string $domain, string $localPart): array;

    /**
     * The database users with the hash of their password (empty when it
     * cannot move) and the databases they reach, by their names there.
     *
     * @return array<string, array{hash: string, databases: list<string>}>
     */
    abstract protected function databaseLogins(): array;

    /**
     * The forwarders as the panel keeps them, local part * being the
     * catch-all of the domain.
     *
     * @param  list<string>  $notes
     * @return list<array{domain: string, local_part: string, targets: list<string>}>
     */
    abstract protected function forwarderEntries(array &$notes): array;

    /**
     * The lines of the account's crontab.
     *
     * @return list<string>
     */
    abstract protected function cronLines(): array;

    /**
     * Names at the top of the home that are the panel's own, or hold what
     * the import makes again here.
     *
     * @return list<string>
     */
    abstract protected function ownFiles(): array;

    /**
     * The home of the account on the other server.
     */
    abstract protected function homedir(): string;

    /**
     * The RAYXO backup the backup becomes for account username: its
     * manifest and settings, the dumps it holds (from the name there to the
     * name here), where the parts of the home go, and what was left out.
     *
     * @param  list<string>  $phpVersions  The PHP versions this server has.
     * @return array{manifest: array<string, mixed>, settings: array<string, mixed>, databases: list<array{from: string, to: string}>, rules: list<array{from: string, to: string, mail: bool}>, notes: list<string>}
     *
     * @throws ValidationException
     */
    public function plan(string $username, array $phpVersions): array
    {
        $notes = [];
        $domains = $this->domains($phpVersions, $notes);
        $mailDomains = array_column(array_filter($domains, fn (array $domain): bool => $domain['type'] === DomainType::Domain->value), 'name');
        [$rules, $docroots] = $this->websiteRules($domains, $username, $notes);
        $databases = $this->databases($username, $notes);
        $mailboxes = $this->mailboxes($mailDomains, $notes);

        foreach ($mailboxes as $mailbox) {
            $rules[] = [...$this->mailRule($mailbox['domain'], $mailbox['local_part']), 'mail' => true];
        }

        $rules = [...$rules, ...$this->homeRules(array_column($rules, 'from'), $notes)];
        $users = $this->databaseUsers($username, $databases, $notes);
        $forwarders = $this->forwarders($mailDomains, $notes);
        $cronJobs = $this->cronJobs($username, $docroots, $notes);
        $notes[] = self::note('DNS zones are made new here: records added by hand in the old panel (such as MX or TXT) must be added again.');

        return [
            'manifest' => [
                'format' => BackupService::FORMAT,
                'panel' => 'RAYXO',
                'source' => $this->panel(),
                'username' => $username,
                'created_at' => now()->toIso8601String(),
                'files' => true,
                'email' => true,
                'databases' => array_column($databases, 'to'),
            ],
            'settings' => [
                'format' => BackupService::FORMAT,
                'account' => ['name' => $this->user, 'email' => $this->contactEmail(), 'password_hash' => null, 'package' => null, 'owner' => null],
                'domains' => array_map(fn (array $domain): array => array_diff_key($domain, ['docroot' => true]), $domains),
                'databases' => array_column($databases, 'to'),
                'database_users' => $users,
                'mailboxes' => $mailboxes,
                'forwarders' => $forwarders,
                'cron_jobs' => $cronJobs,
            ],
            'databases' => $databases,
            'rules' => $rules,
            'notes' => $notes,
        ];
    }

    /**
     * Where the folder of each website goes. A folder two websites share
     * goes to the first. Also returns each folder's full path then and now.
     *
     * @param  list<array{name: string, type: string, docroot: string|null}>  $domains
     * @param  list<string>  $notes
     * @return array{0: list<array{from: string, to: string, mail: bool}>, 1: array<string, string>}
     */
    private function websiteRules(array $domains, string $username, array &$notes): array
    {
        $rules = [];
        $docroots = [];

        foreach ($domains as $domain) {
            if ($domain['docroot'] === null) {
                continue;
            }

            if (isset($docroots[$domain['docroot']])) {
                $notes[] = self::note(':domain shared its folder with another website; its files went to that one.', ['domain' => $domain['name']]);

                continue;
            }

            $docroots[$domain['docroot']] = '/home/'.$username.'/domains/'.$domain['name'].'/public_html';
            $rules[] = ['from' => $domain['docroot'], 'to' => 'domains/'.$domain['name'].'/public_html', 'mail' => false];
        }

        return [$rules, $docroots];
    }

    /**
     * The other files and folders at the top of the home stay where they
     * are, but for the panel's own and those of the panel here.
     *
     * @param  list<string>  $taken  Paths other rules move.
     * @param  list<string>  $notes
     * @return list<array{from: string, to: string, mail: bool}>
     */
    private function homeRules(array $taken, array &$notes): array
    {
        $rules = [];

        foreach ($this->home as $entry) {
            $name = $entry['name'];

            if (in_array($name, $taken, true) || in_array($name, $this->ownFiles(), true)) {
                continue;
            }

            if (in_array($name, self::RESERVED, true)) {
                $notes[] = self::note('The folder :name of the old home is left out: the panel uses that name.', ['name' => $name]);

                continue;
            }

            if ($name === '.ssh') {
                $notes[] = self::note('SSH keys (.ssh) are left out: add them again in the panel.');

                continue;
            }

            $rules[] = ['from' => $name, 'to' => $name, 'mail' => false];
        }

        return $rules;
    }

    /**
     * The databases with a dump, named for the account here.
     *
     * @param  list<string>  $notes
     * @return list<array{from: string, to: string}>
     */
    private function databases(string $username, array &$notes): array
    {
        $databases = [];

        foreach ($this->dumps as $dump) {
            $to = $this->renamed($dump['name'], $username);

            if ($to === null || strlen($to) > DatabaseNames::MAX_DATABASE) {
                $notes[] = self::note('Database :name is left out: its name does not fit here.', ['name' => $dump['name']]);

                continue;
            }

            if ($to !== $dump['name']) {
                $notes[] = self::note('Database :from is now :to.', ['from' => $dump['name'], 'to' => $to]);
            }

            $databases[] = ['from' => $dump['name'], 'to' => $to];
        }

        return $databases;
    }

    /**
     * The database users, with their passwords and the databases they had.
     * The account's own MySQL login there has no counterpart here.
     *
     * @param  list<array{from: string, to: string}>  $databases
     * @param  list<string>  $notes
     * @return list<array{name: string, password_hash: string, databases: list<string>}>
     */
    private function databaseUsers(string $username, array $databases, array &$notes): array
    {
        $names = array_column($databases, 'to', 'from');
        $users = [];

        foreach ($this->databaseLogins() as $user => $login) {
            $user = (string) $user;
            $access = array_values(array_unique(array_filter(array_map(fn (string $database): ?string => $names[$database] ?? null, $login['databases']))));

            if ($user === $this->user) {
                if ($access !== []) {
                    $notes[] = self::note('The :panel login :name to MySQL is not moved: websites that sign in with it need another database user.', ['panel' => $this->panel(), 'name' => $user]);
                }

                continue;
            }

            $to = $this->renamed($user, $username);

            if ($to === null || strlen($to) > DatabaseNames::MAX_USER) {
                $notes[] = self::note('Database user :name is left out: its name does not fit here.', ['name' => $user]);

                continue;
            }

            if (preg_match(self::DATABASE_HASH, $login['hash']) !== 1) {
                $notes[] = self::note('Database user :name is left out: its password cannot be moved; make it again.', ['name' => $user]);

                continue;
            }

            if ($to !== $user) {
                $notes[] = self::note('Database user :from is now :to.', ['from' => $user, 'to' => $to]);
            }

            $users[] = ['name' => $to, 'password_hash' => $login['hash'], 'databases' => $access];
        }

        return $users;
    }

    /**
     * The mailboxes that can be made here, with their passwords. Mail here
     * belongs to domains, not to subdomains or parked domains.
     *
     * @param  list<string>  $mailDomains
     * @param  list<string>  $notes
     * @return list<array{domain: string, local_part: string, password_hash: string, quota_mb: int|null}>
     */
    private function mailboxes(array $mailDomains, array &$notes): array
    {
        $mailboxes = [];

        foreach ($this->mailboxEntries() as $entry) {
            $address = $entry['local_part'].'@'.$entry['domain'];

            if (! in_array($entry['domain'], $mailDomains, true)) {
                $notes[] = self::note('Mailbox :address is left out: mail here is for domains, not subdomains or parked domains.', ['address' => $address]);
            } elseif (preg_match(self::LOCAL_PART, $entry['local_part']) !== 1) {
                $notes[] = self::note('Mailbox :address is left out: its name does not fit here.', ['address' => $address]);
            } elseif (preg_match(self::MAILBOX_HASH, $entry['hash']) !== 1) {
                $notes[] = self::note('Mailbox :address is left out: its password cannot be moved; make it again.', ['address' => $address]);
            } else {
                $quota = $entry['quota_bytes'] !== null && $entry['quota_bytes'] > 0 ? (int) ceil($entry['quota_bytes'] / 1048576) : null;
                $mailboxes[] = ['domain' => $entry['domain'], 'local_part' => $entry['local_part'], 'password_hash' => $entry['hash'], 'quota_mb' => $quota];
            }
        }

        return $mailboxes;
    }

    /**
     * The forwarders of the account's domains that forward to addresses.
     * Pipes to programs, files and the account's own mailbox there have no
     * counterpart here.
     *
     * @param  list<string>  $mailDomains
     * @param  list<string>  $notes
     * @return list<array{domain: string, local_part: string, destinations: list<string>}>
     */
    private function forwarders(array $mailDomains, array &$notes): array
    {
        $forwarders = [];

        foreach ($this->forwarderEntries($notes) as $entry) {
            $domain = $entry['domain'];
            $address = $entry['local_part'].'@'.$domain;
            $destinations = [];
            $others = [];

            foreach ($entry['targets'] as $target) {
                $target = trim($target, " \t\"'");

                if ($target === '' || $target === $this->user || str_starts_with($target, ':fail:') || str_starts_with($target, ':blackhole:')) {
                    continue;
                }

                if (MailAddress::isValid(strtolower($target))) {
                    $destinations[] = strtolower($target);
                } else {
                    $others[] = $target;
                }
            }

            if ($destinations === [] && $others === []) {
                continue;
            }

            if ($entry['local_part'] === '*') {
                $notes[] = self::note('The catch-all of :domain is left out: set it again in the panel.', ['domain' => $domain]);

                continue;
            }

            if (! in_array($domain, $mailDomains, true)) {
                $notes[] = self::note('Forwarder :address is left out: mail here is for domains, not subdomains or parked domains.', ['address' => $address]);

                continue;
            }

            if ($others !== []) {
                $notes[] = self::note('Forwarder :address loses :targets: only forwarding to addresses comes along.', ['address' => $address, 'targets' => implode(', ', $others)]);
            }

            if ($destinations !== []) {
                $forwarders[] = ['domain' => $domain, 'local_part' => $entry['local_part'], 'destinations' => array_values(array_unique($destinations))];
            }
        }

        return $forwarders;
    }

    /**
     * The cron jobs, with the paths of the old home made those here: the
     * home, the folders of the websites and the old panel's PHP.
     *
     * @param  array<string, string>  $docroots  Folder in the old home => its full path here.
     * @param  list<string>  $notes
     * @return list<array{schedule: string, command: string, description: string, enabled: bool}>
     */
    private function cronJobs(string $username, array $docroots, array &$notes): array
    {
        $jobs = [];
        $changed = 0;
        $home = $this->homedir();
        uksort($docroots, fn (string $a, string $b): int => strlen($b) <=> strlen($a));

        foreach ($this->cronLines() as $line) {
            $line = trim($line);

            if ($line === '' || str_starts_with($line, '#') || preg_match('/^[A-Za-z_][A-Za-z0-9_]*\s*=/', $line) === 1) {
                continue;
            }

            $special = str_starts_with($line, '@');
            $parts = preg_split('/\s+/', $line, $special ? 2 : 6) ?: [];

            if (count($parts) !== ($special ? 2 : 6)) {
                continue;
            }

            $command = (string) array_pop($parts);
            $schedule = implode(' ', $parts);

            if ($special && ! in_array(strtolower($schedule), CronSchedule::SPECIALS, true)) {
                $notes[] = self::note('Cron job ":command" is left out: its schedule :schedule does not exist here.', ['command' => $command, 'schedule' => $schedule]);

                continue;
            }

            $moved = $command;

            foreach ($docroots as $old => $new) {
                $moved = (string) preg_replace('#'.preg_quote($home.'/'.$old, '#').'(?=[/\s\'";|&>]|$)#', $new, $moved);
            }

            $moved = (string) preg_replace('#'.preg_quote($home, '#').'(?=[/\s\'";|&>]|$)#', '/home/'.$username, $moved);
            $moved = (string) preg_replace('#(?<![\w/.-])(?:/usr/local/bin/ea-php\d+|/opt/cpanel/ea-php\d+/root/usr/bin/php(?:-cgi)?|/usr/local/php\d+/bin/php(?:-cli)?|/usr/local/bin/php)(?=\s|$)#', 'php', $moved);

            if ($moved !== $command) {
                $changed++;
            }

            $jobs[] = ['schedule' => $schedule, 'command' => $moved, 'description' => $this->panel(), 'enabled' => true];
        }

        if ($changed > 0) {
            $notes[] = self::note('Cron jobs changed to use the folders here: :count.', ['count' => $changed]);
        }

        return $jobs;
    }

    /**
     * Name, which there starts with the username of the account, for
     * account username here. Null when it holds what names here cannot.
     */
    private function renamed(string $name, string $username): ?string
    {
        $rest = strtolower(str_starts_with($name, $this->user.'_') ? substr($name, strlen($this->user) + 1) : $name);

        return preg_match('/^[a-z0-9_]+$/D', $rest) === 1 ? $username.'_'.$rest : null;
    }

    /**
     * Whether domain is a domain name, noting it when it is not.
     *
     * @param  list<string>  $notes
     */
    protected function wellFormed(string $domain, array &$notes): bool
    {
        if (ValidDomain::isWellFormed($domain)) {
            return true;
        }

        $notes[] = self::note(':domain is left out: it is not a valid domain name.', ['domain' => $domain]);

        return false;
    }

    /**
     * Lines of key=value, as both panels keep settings.
     *
     * @return array<string, string>
     */
    protected static function keyValues(string $text): array
    {
        $values = [];

        foreach (preg_split('/\R/', $text) ?: [] as $line) {
            if (preg_match('/^([A-Za-z0-9_.-]+)=(.*)$/D', $line, $matches) === 1) {
                $values[$matches[1]] = trim($matches[2]);
            }
        }

        return $values;
    }

    /**
     * The lines of text, without empty ones.
     *
     * @return list<string>
     */
    protected static function lines(string $text): array
    {
        return array_values(array_filter(array_map('trim', preg_split('/\R/', $text) ?: []), fn (string $line): bool => $line !== ''));
    }

    /**
     * A note for the administrator, in their language.
     *
     * @param  array<string, string|int>  $replace
     */
    protected static function note(string $key, array $replace = []): string
    {
        $text = __($key, $replace);

        return is_string($text) ? $text : $key;
    }
}
