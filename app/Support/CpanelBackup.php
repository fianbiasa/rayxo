<?php

namespace App\Support;

use App\Enums\DomainType;
use App\Enums\PointerMode;
use App\Rules\ValidDomain;
use Illuminate\Validation\ValidationException;

/**
 * A cPanel backup (cpmove-<user>.tar.gz) as an import reads it: the
 * account in cp/<user>, its domains in userdata/ (YAML), forwarders in
 * va/<domain>, cron jobs in cron/<user>, database users in mysql.sql and
 * mysql.sql-auth.json, and mailboxes in the shadow and quota files of
 * each mail domain in the home.
 *
 * cPanel keeps the main domain's website in public_html, addon domains and
 * subdomains in the folder each was given, and the mail of each mailbox in
 * mail/<domain>/<mailbox>; here every website has domains/<name>/public_html
 * and every mailbox imap/<domain>/<mailbox>/Maildir.
 */
final class CpanelBackup extends ForeignBackup
{
    /**
     * Folders and files at the top of a cPanel home that are cPanel's own,
     * or hold what the import makes again here (mail and its passwords,
     * certificates, logs).
     */
    public const CPANEL_FILES = [
        '.cpanel', '.cpaddons', '.cphorde', '.caldav', '.cagefs', '.cl.selector', '.lastlogin', '.contactemail',
        '.ftpquota', '.my.cnf', '.spamassassin', '.spamassassinenable', '.spamassassinboxenable', '.razor', '.pyzor',
        '.sqmaildata', '.autorespond', '.wp-toolkit', '.softaculous', '.trash', '.cache',
        'access-logs', 'cpbackup-exclude.conf', 'etc', 'logs', 'mail', 'ssl', 'tmp', 'www',
    ];

    public function kind(): string
    {
        return 'cpanel';
    }

    public function panel(): string
    {
        return 'cPanel';
    }

    public function contactEmail(): ?string
    {
        $email = strtolower($this->account()['CONTACTEMAIL'] ?? '');

        return filter_var($email, FILTER_VALIDATE_EMAIL) !== false ? $email : null;
    }

    /**
     * @param  array<string, string>  $files
     */
    protected static function userIn(array $files): ?string
    {
        $accounts = array_values(array_filter(array_keys($files), fn (string $name): bool => str_starts_with($name, 'cp/')));
        $user = count($accounts) === 1 ? substr($accounts[0], 3) : '';

        return preg_match('/^[a-z0-9][a-z0-9._-]{0,63}$/D', $user) === 1 ? $user : null;
    }

    /**
     * The main domain, addon domains, subdomains and parked domains.
     *
     * @param  list<string>  $phpVersions
     * @param  list<string>  $notes
     * @return list<array{name: string, type: string, parent: string|null, pointer_mode: string|null, php_version: string|null, docroot: string|null, is_primary: bool}>
     *
     * @throws ValidationException
     */
    protected function domains(array $phpVersions, array &$notes): array
    {
        $main = self::yaml($this->files['userdata/main'] ?? '');
        $mainDomain = strtolower(self::scalar($main['main_domain'] ?? null) ?? ($this->account()['DNS'] ?? ''));

        if (! ValidDomain::isWellFormed($mainDomain)) {
            throw ValidationException::withMessages(['backup' => __('The backup names no main domain.')]);
        }

        $addons = is_array($main['addon_domains'] ?? null) ? $main['addon_domains'] : [];
        $internal = array_map(fn (mixed $sub): string => strtolower((string) self::scalar($sub)), array_values($addons));
        $domains = [$this->domain($mainDomain, DomainType::Domain, null, $mainDomain, $phpVersions, $notes, 'public_html') + ['is_primary' => true]];
        $parents = [$mainDomain];

        foreach ($addons as $addon => $sub) {
            $addon = strtolower((string) $addon);

            if ($this->wellFormed($addon, $notes)) {
                $domains[] = $this->domain($addon, DomainType::Domain, null, strtolower((string) self::scalar($sub)), $phpVersions, $notes);
                $parents[] = $addon;
            }
        }

        foreach (self::list($main['sub_domains'] ?? null) as $sub) {
            $sub = strtolower($sub);

            if (in_array($sub, $internal, true) || ! $this->wellFormed($sub, $notes)) {
                continue;
            }

            $parent = collect($parents)->filter(fn (string $name): bool => str_ends_with($sub, '.'.$name))->sortByDesc(fn (string $name): int => strlen($name))->first();

            if ($parent === null) {
                $notes[] = self::note(':domain is left out: it is not a subdomain of a domain of the account.', ['domain' => $sub]);

                continue;
            }

            $domains[] = $this->domain($sub, DomainType::Subdomain, $parent, $sub, $phpVersions, $notes);
        }

        foreach (self::list($main['parked_domains'] ?? null) as $parked) {
            $parked = strtolower($parked);

            if ($this->wellFormed($parked, $notes)) {
                $domains[] = ['name' => $parked, 'type' => DomainType::Pointer->value, 'parent' => $mainDomain, 'pointer_mode' => PointerMode::Alias->value, 'php_version' => null, 'docroot' => null];
            }
        }

        return array_map(fn (array $domain): array => $domain + ['is_primary' => false], $domains);
    }

    /**
     * One website, with what cPanel's userdata of its virtual host says.
     *
     * @param  list<string>  $phpVersions
     * @param  list<string>  $notes
     * @param  string|null  $usual  The folder cPanel gives the website, for userdata that names none.
     * @return array{name: string, type: string, parent: string|null, pointer_mode: null, php_version: string|null, docroot: string|null}
     */
    private function domain(string $name, DomainType $type, ?string $parent, string $vhost, array $phpVersions, array &$notes, ?string $usual = null): array
    {
        $userdata = self::yaml($this->files['userdata/'.$vhost] ?? '');
        $version = null;

        if (preg_match('/^(?:ea|alt)-php(\d)(\d+)$/D', (string) self::scalar($userdata['phpversion'] ?? null), $matches) === 1) {
            $version = $matches[1].'.'.$matches[2];

            if (! in_array($version, $phpVersions, true)) {
                $notes[] = self::note(':domain used PHP :version, which this server does not have: it gets the default version.', ['domain' => $name, 'version' => $version]);
                $version = null;
            }
        }

        $home = $this->homedir();
        $root = self::scalar($userdata['documentroot'] ?? null) ?? ($usual !== null ? $home.'/'.$usual : null);
        $docroot = $root !== null && str_starts_with($root, $home.'/') ? trim(substr($root, strlen($home) + 1), '/') : null;

        if ($docroot === null || $docroot === '' || preg_match('#(^|/)\.\.?(/|$)#', $docroot) === 1) {
            $notes[] = self::note('The files of :domain are left out: its folder is not in the home.', ['domain' => $name]);
            $docroot = null;
        }

        return ['name' => $name, 'type' => $type->value, 'parent' => $parent, 'pointer_mode' => null, 'php_version' => $version, 'docroot' => $docroot];
    }

    /**
     * The mailboxes in the shadow file of each mail domain, with their
     * quotas from its quota file.
     *
     * @return list<array{domain: string, local_part: string, hash: string, quota_bytes: int|null}>
     */
    protected function mailboxEntries(): array
    {
        $entries = [];

        foreach ($this->files as $name => $content) {
            if (preg_match('#^homedir/etc/([^/]+)/shadow$#D', $name, $matches) !== 1) {
                continue;
            }

            $domain = strtolower($matches[1]);
            $quotas = $this->quotas($this->files['homedir/etc/'.$domain.'/quota'] ?? '');

            foreach (self::lines($content) as $line) {
                [$local, $hash] = array_pad(explode(':', $line, 3), 2, '');
                $local = strtolower($local);

                if ($local !== '') {
                    $entries[] = ['domain' => $domain, 'local_part' => $local, 'hash' => $hash, 'quota_bytes' => $quotas[$local] ?? null];
                }
            }
        }

        return $entries;
    }

    /**
     * @return array{from: string, to: string}
     */
    protected function mailRule(string $domain, string $localPart): array
    {
        return ['from' => 'mail/'.$domain.'/'.$localPart, 'to' => 'imap/'.$domain.'/'.$localPart.'/Maildir'];
    }

    /**
     * The users in mysql.sql-auth.json and the grants of mysql.sql.
     *
     * @return array<string, array{hash: string, databases: list<string>}>
     */
    protected function databaseLogins(): array
    {
        $logins = [];

        foreach ((array) json_decode($this->files['mysql.sql-auth.json'] ?? '', true) as $user => $hosts) {
            $logins[(string) $user] = ['hash' => '', 'databases' => []];

            foreach ((array) $hosts as $host) {
                $hash = is_array($host) && is_string($host['pass_hash'] ?? null) && ctype_xdigit($host['pass_hash']) ? (string) hex2bin($host['pass_hash']) : '';

                if (str_starts_with($hash, '*')) {
                    $logins[(string) $user]['hash'] = $hash;
                }
            }
        }

        foreach (self::lines($this->files['mysql.sql'] ?? '') as $line) {
            if (preg_match("/^GRANT .+ ON (.+?)\\.\\* TO '([^']+)'@'[^']*'(?: IDENTIFIED BY PASSWORD '([^']*)')?/", $line, $matches) !== 1) {
                continue;
            }

            $user = $matches[2];
            $logins[$user] ??= ['hash' => '', 'databases' => []];

            if ($logins[$user]['hash'] === '' && str_starts_with($matches[3] ?? '', '*')) {
                $logins[$user]['hash'] = $matches[3];
            }

            $logins[$user]['databases'][] = str_replace(['\\_', '\\%'], ['_', '%'], trim($matches[1], '`'));
        }

        return $logins;
    }

    /**
     * The forwarders in va/<domain>: address: targets.
     *
     * @param  list<string>  $notes
     * @return list<array{domain: string, local_part: string, targets: list<string>}>
     */
    protected function forwarderEntries(array &$notes): array
    {
        $entries = [];

        foreach ($this->files as $name => $content) {
            if (preg_match('#^va/([^/]+)$#D', $name, $matches) !== 1) {
                continue;
            }

            $domain = strtolower($matches[1]);

            foreach (self::lines($content) as $line) {
                [$address, $targets] = array_pad(array_map('trim', explode(':', $line, 2)), 2, '');
                $address = strtolower($address);

                if ($address === '*') {
                    $entries[] = ['domain' => $domain, 'local_part' => '*', 'targets' => explode(',', $targets)];
                } elseif (str_ends_with($address, '@'.$domain) && ! str_starts_with($address, '#')) {
                    $entries[] = ['domain' => $domain, 'local_part' => substr($address, 0, -strlen('@'.$domain)), 'targets' => explode(',', $targets)];
                }
            }
        }

        return $entries;
    }

    /**
     * @return list<string>
     */
    protected function cronLines(): array
    {
        return self::lines($this->files['cron/'.$this->user] ?? '');
    }

    /**
     * @return list<string>
     */
    protected function ownFiles(): array
    {
        return self::CPANEL_FILES;
    }

    protected function homedir(): string
    {
        $main = self::yaml($this->files['userdata/main'] ?? '');
        $vhost = self::yaml($this->files['userdata/'.strtolower((string) self::scalar($main['main_domain'] ?? null))] ?? '');
        $home = self::scalar($vhost['homedir'] ?? null) ?? trim(strtok($this->files['homedir_paths'] ?? '', "\n") ?: '');

        return rtrim($home !== '' ? $home : '/home/'.$this->user, '/');
    }

    /**
     * What cp/<user> says of the account: KEY=value lines.
     *
     * @return array<string, string>
     */
    private function account(): array
    {
        return self::keyValues($this->files['cp/'.$this->user] ?? '');
    }

    /**
     * The quota of each mailbox in a quota file, in bytes.
     *
     * @return array<string, int>
     */
    private function quotas(string $content): array
    {
        $quotas = [];

        foreach (self::lines($content) as $line) {
            [$local, $bytes] = array_pad(explode(':', $line, 2), 2, '');

            if (ctype_digit(trim($bytes))) {
                $quotas[strtolower($local)] = (int) $bytes;
            }
        }

        return $quotas;
    }

    /**
     * The top of a YAML file as cPanel writes its userdata: scalars, and
     * maps and lists one level deep. Anything deeper is skipped.
     *
     * @return array<string, mixed>
     */
    public static function yaml(string $text): array
    {
        $values = [];
        $key = null;

        foreach (preg_split('/\R/', $text) ?: [] as $line) {
            if (trim($line) === '' || trim($line) === '---' || str_starts_with(ltrim($line), '#')) {
                continue;
            }

            if (preg_match('/^([A-Za-z0-9_.-]+):(?:\s+(.*))?$/D', $line, $matches) === 1) {
                $key = $matches[1];
                $value = trim($matches[2] ?? '');
                $values[$key] = match ($value) {
                    '', '{}', '[]' => [],
                    '~' => null,
                    default => self::unquote($value),
                };

                continue;
            }

            if ($key === null || ! is_array($values[$key]) || preg_match('/^  (\S.*)$/D', $line, $matches) !== 1) {
                continue;
            }

            if (preg_match('/^-\s+(.+)$/D', $matches[1], $item) === 1) {
                $values[$key][] = self::unquote(trim($item[1]));
            } elseif (preg_match('/^(\'[^\']*\'|"[^"]*"|[^:\s][^:]*):\s+(.+)$/D', $matches[1], $pair) === 1) {
                $values[$key][self::unquote(trim($pair[1]))] = self::unquote(trim($pair[2]));
            }
        }

        return $values;
    }

    private static function unquote(string $value): string
    {
        if (strlen($value) >= 2 && $value[0] === "'" && str_ends_with($value, "'")) {
            return str_replace("''", "'", substr($value, 1, -1));
        }

        if (strlen($value) >= 2 && $value[0] === '"' && str_ends_with($value, '"')) {
            return stripcslashes(substr($value, 1, -1));
        }

        return $value;
    }

    private static function scalar(mixed $value): ?string
    {
        return is_string($value) && $value !== '' ? $value : null;
    }

    /**
     * @return list<string>
     */
    private static function list(mixed $value): array
    {
        return is_array($value) ? array_values(array_filter($value, fn (mixed $item): bool => is_string($item) && $item !== '')) : [];
    }
}
