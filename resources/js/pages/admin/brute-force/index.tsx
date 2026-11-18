import { Head, router, usePoll } from '@inertiajs/react';
import { Ban, Plus, ShieldAlert, ShieldCheck, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { PageHeader } from '@/components/page-header';
import { Panel, tableStyles } from '@/components/panel';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
    Dialog,
    DialogClose,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Spinner } from '@/components/ui/spinner';
import {
    unban as unbanRoute,
    whitelist as whitelistRoute,
} from '@/routes/admin/brute-force';

type Jail = {
    name: string;
    currently_failed: number;
    total_failed: number;
    currently_banned: number;
    total_banned: number;
    banned_ips: string[];
};

type Report = {
    jails: Jail[];
    whitelist: string[];
};

type Props = {
    report: Report | null;
    error: string | null;
};

const jailLabels: Record<string, string> = {
    sshd: 'SSH',
    dovecot: 'Email (IMAP/POP)',
    'postfix-sasl': 'Email (SMTP)',
    'pure-ftpd': 'FTP',
    'rayxo-panel': 'Login panel',
    'wordpress-hard': 'WordPress',
};

function jailLabel(name: string): string {
    return jailLabels[name] ?? name;
}

/** Whether value looks like a plain IP address or a CIDR range. */
function isIpOrCidr(value: string): boolean {
    const [address, bits, ...rest] = value.split('/');
    if (rest.length > 0) {
        return false;
    }

    const isV6 = address.includes(':');
    const looksLikeV4 = /^(\d{1,3}\.){3}\d{1,3}$/.test(address);
    if (!isV6 && !looksLikeV4) {
        return false;
    }

    if (bits === undefined) {
        return true;
    }

    if (!/^\d+$/.test(bits)) {
        return false;
    }

    return Number(bits) <= (isV6 ? 128 : 32);
}

export default function BruteForceIndex({ report, error }: Props) {
    const [pendingUnban, setPendingUnban] = useState<string | null>(null);
    const [unbanning, setUnbanning] = useState(false);
    const [unbanError, setUnbanError] = useState<string | null>(null);

    usePoll(10_000, { only: ['report', 'error'] });

    const blocked = (report?.jails ?? []).flatMap((jail) =>
        jail.banned_ips.map((ip) => ({ ip, jail: jail.name })),
    );

    const runUnban = () => {
        if (!pendingUnban) {
            return;
        }

        setUnbanning(true);
        setUnbanError(null);
        router.post(
            unbanRoute().url,
            { ip: pendingUnban },
            {
                preserveScroll: true,
                onSuccess: () => setPendingUnban(null),
                onError: (errors) =>
                    setUnbanError(Object.values(errors)[0] ?? null),
                onFinish: () => setUnbanning(false),
            },
        );
    };

    return (
        <>
            <Head title="Brute-force" />

            <PageHeader
                title="Monitor brute-force"
                description="fail2ban memblokir alamat IP yang gagal login berkali-kali ke SSH, email, FTP dan panel. Di sini kamu lihat yang sedang diblokir, buka blokirnya, dan atur alamat yang tidak boleh diblokir."
            />

            {error !== null && (
                <Alert variant="destructive">
                    <ShieldAlert />
                    <AlertDescription>{error}</AlertDescription>
                </Alert>
            )}

            {report !== null && (
                <>
                    <Panel
                        title="IP yang sedang diblokir"
                        description="Alamat yang fail2ban blokir saat ini. Buka blokir kalau itu kamu atau pengguna yang sah."
                        flush
                    >
                        {blocked.length === 0 ? (
                            <p className="px-4 py-6 text-sm text-muted-foreground">
                                Tidak ada IP yang sedang diblokir.
                            </p>
                        ) : (
                            <div className="overflow-x-auto">
                                <table className={tableStyles.table}>
                                    <thead className={tableStyles.head}>
                                        <tr>
                                            <th className={tableStyles.th}>
                                                Alamat IP
                                            </th>
                                            <th className={tableStyles.th}>
                                                Layanan
                                            </th>
                                            <th className={tableStyles.th}>
                                                <span className="sr-only">
                                                    Aksi
                                                </span>
                                            </th>
                                        </tr>
                                    </thead>
                                    <tbody className="divide-y">
                                        {blocked.map(({ ip, jail }) => (
                                            <tr
                                                key={`${jail}-${ip}`}
                                                className={tableStyles.row}
                                            >
                                                <td
                                                    className={`${tableStyles.td} font-mono`}
                                                >
                                                    {ip}
                                                </td>
                                                <td className={tableStyles.td}>
                                                    {jailLabel(jail)}
                                                </td>
                                                <td className="px-4 py-2 text-right">
                                                    <Button
                                                        size="sm"
                                                        variant="ghost"
                                                        onClick={() =>
                                                            setPendingUnban(ip)
                                                        }
                                                    >
                                                        <Ban />
                                                        <span className="hidden sm:inline">
                                                            Buka blokir
                                                        </span>
                                                    </Button>
                                                </td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        )}
                    </Panel>

                    <Panel
                        title="Jail"
                        description="Tiap jail memantau satu layanan. Angka gagal dihitung dalam jendela waktu fail2ban; setelah batasnya, alamat diblokir."
                        flush
                    >
                        <div className="overflow-x-auto">
                            <table className={tableStyles.table}>
                                <thead className={tableStyles.head}>
                                    <tr>
                                        <th className={tableStyles.th}>Jail</th>
                                        <th
                                            className={`${tableStyles.th} text-right`}
                                        >
                                            Gagal (sekarang / total)
                                        </th>
                                        <th
                                            className={`${tableStyles.th} text-right`}
                                        >
                                            Diblokir (sekarang / total)
                                        </th>
                                    </tr>
                                </thead>
                                <tbody className="divide-y">
                                    {report.jails.map((jail) => (
                                        <tr
                                            key={jail.name}
                                            className={tableStyles.row}
                                        >
                                            <td className={tableStyles.td}>
                                                <div className="font-semibold">
                                                    {jailLabel(jail.name)}
                                                </div>
                                                <div className="font-mono text-xs text-muted-foreground">
                                                    {jail.name}
                                                </div>
                                            </td>
                                            <td
                                                className={`${tableStyles.td} text-right tabular-nums`}
                                            >
                                                {jail.currently_failed} /{' '}
                                                {jail.total_failed}
                                            </td>
                                            <td
                                                className={`${tableStyles.td} text-right tabular-nums`}
                                            >
                                                {jail.currently_banned > 0 ? (
                                                    <span className="font-semibold text-destructive-foreground">
                                                        {jail.currently_banned}
                                                    </span>
                                                ) : (
                                                    jail.currently_banned
                                                )}{' '}
                                                / {jail.total_banned}
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    </Panel>

                    <WhitelistEditor current={report.whitelist} />
                </>
            )}

            <Dialog
                open={pendingUnban !== null}
                onOpenChange={(open) => {
                    if (!open) {
                        setPendingUnban(null);
                        setUnbanError(null);
                    }
                }}
            >
                <DialogContent className="rounded-2xl">
                    <DialogHeader>
                        <DialogTitle>Buka blokir {pendingUnban}?</DialogTitle>
                        <DialogDescription>
                            Alamat ini bisa login lagi ke semua layanan. Kalau
                            masih mencoba menebak password, fail2ban akan
                            memblokirnya lagi.
                        </DialogDescription>
                    </DialogHeader>
                    {unbanError && (
                        <Alert variant="destructive">
                            <AlertDescription className="break-all">
                                {unbanError}
                            </AlertDescription>
                        </Alert>
                    )}
                    <DialogFooter className="gap-2">
                        <DialogClose asChild>
                            <Button type="button" variant="secondary">
                                Batal
                            </Button>
                        </DialogClose>
                        <Button disabled={unbanning} onClick={runUnban}>
                            {unbanning && <Spinner />}
                            Buka blokir
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </>
    );
}

function WhitelistEditor({ current }: { current: string[] }) {
    const [entries, setEntries] = useState<string[]>(current);
    const [draft, setDraft] = useState('');
    const [addError, setAddError] = useState<string | null>(null);
    const [saving, setSaving] = useState(false);
    const [saveError, setSaveError] = useState<string | null>(null);

    // Re-sync from the server only when it actually changes (e.g. after a
    // save, or another admin's edit), not on every poll, so edits in progress
    // are kept.
    const serverKey = current.join(' ');
    useEffect(() => {
        setEntries(current);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [serverKey]);

    const dirty = entries.join(' ') !== current.join(' ');

    const add = () => {
        const value = draft.trim();
        if (value === '') {
            return;
        }

        if (!isIpOrCidr(value)) {
            setAddError('Masukkan alamat IP atau rentang CIDR yang benar.');
            return;
        }

        if (entries.includes(value)) {
            setAddError('Alamat itu sudah ada di whitelist.');
            return;
        }

        setEntries([...entries, value]);
        setDraft('');
        setAddError(null);
    };

    const remove = (value: string) => {
        setEntries(entries.filter((entry) => entry !== value));
    };

    const save = () => {
        setSaving(true);
        setSaveError(null);
        router.put(
            whitelistRoute().url,
            { ips: entries },
            {
                preserveScroll: true,
                onError: (errors) =>
                    setSaveError(Object.values(errors)[0] ?? null),
                onFinish: () => setSaving(false),
            },
        );
    };

    return (
        <Panel
            title="Whitelist"
            description="Alamat yang tidak boleh diblokir fail2ban — tambahkan IP kantor atau rumahmu supaya tidak terkunci sendiri. Alamat loopback server selalu aman tanpa perlu dicantumkan."
        >
            <div className="flex flex-col gap-2 sm:flex-row">
                <Input
                    value={draft}
                    placeholder="203.0.113.7 atau 203.0.113.0/24"
                    inputMode="text"
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono"
                    onChange={(event) => {
                        setDraft(event.target.value);
                        setAddError(null);
                    }}
                    onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                            event.preventDefault();
                            add();
                        }
                    }}
                />
                <Button type="button" variant="secondary" onClick={add}>
                    <Plus />
                    Tambah
                </Button>
            </div>

            {addError && (
                <p className="mt-2 text-sm text-destructive-foreground">
                    {addError}
                </p>
            )}

            {entries.length === 0 ? (
                <p className="mt-4 text-sm text-muted-foreground">
                    Belum ada alamat di whitelist.
                </p>
            ) : (
                <ul className="mt-4 divide-y rounded-lg border">
                    {entries.map((entry) => (
                        <li
                            key={entry}
                            className="flex items-center justify-between gap-3 px-3 py-2"
                        >
                            <span className="font-mono text-sm">{entry}</span>
                            <Button
                                size="sm"
                                variant="ghost"
                                aria-label={`Hapus ${entry}`}
                                onClick={() => remove(entry)}
                            >
                                <X />
                            </Button>
                        </li>
                    ))}
                </ul>
            )}

            {saveError && (
                <Alert variant="destructive" className="mt-4">
                    <AlertDescription className="break-all">
                        {saveError}
                    </AlertDescription>
                </Alert>
            )}

            <div className="mt-4 flex items-center gap-3">
                <Button disabled={!dirty || saving} onClick={save}>
                    {saving ? <Spinner /> : <ShieldCheck />}
                    Simpan whitelist
                </Button>
                {dirty && (
                    <span className="text-sm text-muted-foreground">
                        Ada perubahan yang belum disimpan.
                    </span>
                )}
            </div>
        </Panel>
    );
}
