<?php

namespace App\Support;

use App\Enums\DomainType;
use App\Enums\PointerMode;
use Illuminate\Validation\ValidationException;

/**
 * A DirectAdmin backup (user.<creator>.<user>.tar.zst from Admin Backup, or
 * the one an account makes of itself) as an import reads it: the account
 * in backup/user.conf, its cron jobs in crontab.conf, the users of each
 * database in <database>.conf, and per domain its domain.conf, pointers,
 * subdomains and email/ (passwd, quota, aliases, email.conf).
 *
 * DirectAdmin lays websites and mail out as here, domains/<name>/public_html
 * and imap/<domain>/<mailbox>/Maildir, so most stays where it was; only a
 * subdomain in the folder of its domain moves to a folder of its own.
 */
final class DirectAdminBackup extends ForeignBackup
{
    /**
     * Names at the top of a DirectAdmin home that are its own, or hold what
     * the import makes again here: the account's own mailbox, PHP's mail
     * log, SpamAssassin's settings, the link to the main website, and the
     * websites and mail, which rules of their own move.
     */
    public const DIRECTADMIN_FILES = ['Maildir', '.php', '.spamassassin', '.trash', 'tmp', '.cache', 'public_html', 'domains', 'imap'];

    public function kind(): string
    {
        return 'directadmin';
    }

    public function panel(): string
    {
        return 'DirectAdmin';
    }

    public function contactEmail(): ?string
    {
        $email = strtolower(trim(explode(',', $this->userConf()['email'] ?? '')[0]));

        return filter_var($email, FILTER_VALIDATE_EMAIL) !== false ? $email : null;
    }

    /**
     * @param  array<string, string>  $files
     */
    protected static function userIn(array $files): ?string
    {
        $user = self::keyValues($files['backup/user.conf'] ?? '')['username'] ?? '';

        return preg_match('/^[a-z0-9][a-z0-9._-]{0,63}$/D', $user) === 1 ? $user : null;
    }

    /**
     * The domains, then the subdomains and pointers of each.
     *
     * @param  list<string>  $phpVersions
     * @param  list<string>  $notes
     * @return list<array{name: string, type: string, parent: string|null, pointer_mode: string|null, php_version: string|null, docroot: string|null, is_primary: bool}>
     *
     * @throws ValidationException
     */
    protected function domains(array $phpVersions, array &$notes): array
    {
        $names = [];

        foreach (array_keys($this->files) as $name) {
            if (preg_match('#^backup/([^/]+)/domain\.conf$#D', $name, $matches) === 1) {
                $names[] = strtolower($matches[1]);
            }
        }

        $main = strtolower($this->userConf()['domain'] ?? '');

        if (! in_array($main, $names, true)) {
            $main = collect($names)->first(fn (string $name): bool => ($this->settingsOf($name)['defaultdomain'] ?? '') === 'yes') ?? '';
        }

        if ($main === '' || ! $this->wellFormed($main, $notes)) {
            throw ValidationException::withMessages(['backup' => __('The backup names no main domain.')]);
        }

        sort($names);
        $ordered = [$main];

        foreach ($names as $name) {
            if ($name !== $main && $this->wellFormed($name, $notes)) {
                $ordered[] = $name;
            }
        }

        $domains = [];

        foreach ($ordered as $name) {
            $this->notePhpChoice($name, $this->settingsOf($name)['php1_select'] ?? null, $notes);
            $domains[] = ['name' => $name, 'type' => DomainType::Domain->value, 'parent' => null, 'pointer_mode' => null, 'php_version' => null, 'docroot' => 'domains/'.$name.'/public_html', 'is_primary' => $name === $main];
        }

        foreach ($ordered as $name) {
            $domains = [...$domains, ...$this->subdomainsOf($name, $notes), ...$this->pointersOf($name, $notes)];
        }

        return $domains;
    }

    /**
     * The subdomains of domain, in the folder DirectAdmin gave each: its
     * own when the docroot override says so, else one in the domain's.
     *
     * @param  list<string>  $notes
     * @return list<array{name: string, type: string, parent: string, pointer_mode: null, php_version: null, docroot: string|null, is_primary: bool}>
     */
    private function subdomainsOf(string $domain, array &$notes): array
    {
        $overrides = [];

        foreach (self::lines($this->files['backup/'.$domain.'/domain.subdomains.docroot.override'] ?? '') as $line) {
            [$label, $query] = array_pad(explode('=', $line, 2), 2, '');
            parse_str($query, $settings);
            $overrides[strtolower($label)] = $settings;
        }

        $subdomains = [];

        foreach (self::lines($this->files['backup/'.$domain.'/subdomain.list'] ?? '') as $label) {
            $label = strtolower($label);
            $name = $label.'.'.$domain;

            if (! $this->wellFormed($name, $notes)) {
                continue;
            }

            $override = $overrides[$label] ?? [];
            $root = is_string($override['public_html'] ?? null) ? trim($override['public_html'], '/') : 'domains/'.$domain.'/public_html/'.$label;
            $docroot = $root !== '' && preg_match('#(^|/)\.\.?(/|$)#', $root) !== 1 ? $root : null;

            if ($docroot === null) {
                $notes[] = self::note('The files of :domain are left out: its folder is not in the home.', ['domain' => $name]);
            }

            $this->notePhpChoice($name, is_string($override['php1_select'] ?? null) ? $override['php1_select'] : null, $notes);
            $subdomains[] = ['name' => $name, 'type' => DomainType::Subdomain->value, 'parent' => $domain, 'pointer_mode' => null, 'php_version' => null, 'docroot' => $docroot, 'is_primary' => false];
        }

        return $subdomains;
    }

    /**
     * The pointers of domain: aliases, and redirects (type=pointer).
     *
     * @param  list<string>  $notes
     * @return list<array{name: string, type: string, parent: string, pointer_mode: string, php_version: null, docroot: null, is_primary: bool}>
     */
    private function pointersOf(string $domain, array &$notes): array
    {
        $pointers = [];

        foreach (self::lines($this->files['backup/'.$domain.'/domain.pointers'] ?? '') as $line) {
            [$name, $query] = array_pad(explode('=', $line, 2), 2, '');
            $name = strtolower($name);
            parse_str($query, $settings);

            if ($this->wellFormed($name, $notes)) {
                $mode = ($settings['type'] ?? 'alias') === 'pointer' ? PointerMode::Redirect : PointerMode::Alias;
                $pointers[] = ['name' => $name, 'type' => DomainType::Pointer->value, 'parent' => $domain, 'pointer_mode' => $mode->value, 'php_version' => null, 'docroot' => null, 'is_primary' => false];
            }
        }

        return $pointers;
    }

    /**
     * The mailboxes in email/passwd of each domain, with their quotas from
     * email/quota (0 is no limit).
     *
     * @return list<array{domain: string, local_part: string, hash: string, quota_bytes: int|null}>
     */
    protected function mailboxEntries(): array
    {
        $entries = [];

        foreach ($this->files as $name => $content) {
            if (preg_match('#^backup/([^/]+)/email/passwd$#D', $name, $matches) !== 1) {
                continue;
            }

            $domain = strtolower($matches[1]);
            $quotas = [];

            foreach (self::lines($this->files['backup/'.$domain.'/email/quota'] ?? '') as $line) {
                [$local, $bytes] = array_pad(explode(':', $line, 2), 2, '');

                if (ctype_digit(trim($bytes))) {
                    $quotas[strtolower($local)] = (int) $bytes;
                }
            }

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
        return ['from' => 'imap/'.$domain.'/'.$localPart, 'to' => 'imap/'.$domain.'/'.$localPart];
    }

    /**
     * The users in <database>.conf of each database: user=settings, with
     * the hash of the password in passwd.
     *
     * @return array<string, array{hash: string, databases: list<string>}>
     */
    protected function databaseLogins(): array
    {
        $logins = [];

        foreach ($this->dumps as $dump) {
            foreach (self::lines($this->files['backup/'.$dump['name'].'.conf'] ?? '') as $line) {
                [$user, $query] = array_pad(explode('=', $line, 2), 2, '');

                if (in_array($user, ['accesshosts', 'db_collation'], true) || $user === '') {
                    continue;
                }

                parse_str($query, $settings);
                $logins[$user] ??= ['hash' => '', 'databases' => []];

                if ($logins[$user]['hash'] === '' && is_string($settings['passwd'] ?? null)) {
                    $logins[$user]['hash'] = $settings['passwd'];
                }

                $logins[$user]['databases'][] = $dump['name'];
            }
        }

        return $logins;
    }

    /**
     * The forwarders in email/aliases of each domain (local: targets) and
     * its catch-all in email.conf. Automatic replies are noted.
     *
     * @param  list<string>  $notes
     * @return list<array{domain: string, local_part: string, targets: list<string>}>
     */
    protected function forwarderEntries(array &$notes): array
    {
        $entries = [];

        foreach ($this->files as $name => $content) {
            if (preg_match('#^backup/([^/]+)/email/aliases$#D', $name, $matches) !== 1) {
                continue;
            }

            $domain = strtolower($matches[1]);

            foreach (self::lines($content) as $line) {
                [$local, $targets] = array_pad(array_map('trim', explode(':', $line, 2)), 2, '');

                if ($local !== '' && ! str_starts_with($local, '#')) {
                    $entries[] = ['domain' => $domain, 'local_part' => strtolower($local), 'targets' => explode(',', $targets)];
                }
            }

            $catchAll = self::keyValues($this->files['backup/'.$domain.'/email/email.conf'] ?? '')['catchall'] ?? '';
            $entries[] = ['domain' => $domain, 'local_part' => '*', 'targets' => [$catchAll]];

            foreach (['autoresponder.conf', 'vacation.conf'] as $replies) {
                if (trim($this->files['backup/'.$domain.'/email/'.$replies] ?? '') !== '') {
                    $notes[] = self::note('The automatic replies of :domain are left out: set them again in the panel.', ['domain' => $domain]);

                    break;
                }
            }
        }

        return $entries;
    }

    /**
     * The jobs in crontab.conf: number=line.
     *
     * @return list<string>
     */
    protected function cronLines(): array
    {
        $lines = [];

        foreach (self::lines($this->files['backup/crontab.conf'] ?? '') as $line) {
            if (preg_match('/^\d+=(.+)$/D', $line, $matches) === 1) {
                $lines[] = $matches[1];
            }
        }

        return $lines;
    }

    /**
     * @return list<string>
     */
    protected function ownFiles(): array
    {
        return self::DIRECTADMIN_FILES;
    }

    protected function homedir(): string
    {
        return '/home/'.$this->user;
    }

    /**
     * What backup/user.conf says of the account.
     *
     * @return array<string, string>
     */
    private function userConf(): array
    {
        return self::keyValues($this->files['backup/user.conf'] ?? '');
    }

    /**
     * What domain.conf says of domain.
     *
     * @return array<string, string>
     */
    private function settingsOf(string $domain): array
    {
        return self::keyValues($this->files['backup/'.$domain.'/domain.conf'] ?? '');
    }

    /**
     * Note a website on another PHP than the first of the DirectAdmin
     * server: the backup names the choice, not its version.
     *
     * @param  list<string>  $notes
     */
    private function notePhpChoice(string $domain, ?string $choice, array &$notes): void
    {
        if ($choice !== null && $choice !== '' && $choice !== '1') {
            $notes[] = self::note(':domain used PHP choice :choice of DirectAdmin, which the backup does not name: it gets the default version; choose its version again.', ['domain' => $domain, 'choice' => $choice]);
        }
    }
}
