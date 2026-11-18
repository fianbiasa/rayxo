import { Head, router, usePoll } from '@inertiajs/react';
import { Plus, ShieldAlert, TriangleAlert, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { PageHeader } from '@/components/page-header';
import { Panel } from '@/components/panel';
import { StatusBadge } from '@/components/status-badge';
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
import { formatDateTime, formatRelative } from '@/lib/format';
import {
    apply as applyRoute,
    cancel as cancelRoute,
    confirm as confirmRoute,
} from '@/routes/admin/firewall';

type Config = {
    enabled: boolean;
    tcp_ports: number[];
    udp_ports: number[];
    allow_ips: string[];
    deny_ips: string[];
};

type Trial = {
    config: Config;
    expires_at: string;
};

type Status = {
    active: Config;
    pending: Trial | null;
    always_open_tcp: number[];
    always_open_udp: number[];
    ftp_passive: string;
};

type Props = {
    status: Status | null;
    error: string | null;
};

function isPort(value: string): boolean {
    return /^\d+$/.test(value) && Number(value) >= 1 && Number(value) <= 65535;
}

function isIpOrCidr(value: string): boolean {
    const [address, bits, ...rest] = value.split('/');
    if (rest.length > 0) {
        return false;
    }

    const isV6 = address.includes(':');
    if (!isV6 && !/^(\d{1,3}\.){3}\d{1,3}$/.test(address)) {
        return false;
    }

    if (bits === undefined) {
        return true;
    }

    return /^\d+$/.test(bits) && Number(bits) <= (isV6 ? 128 : 32);
}

export default function FirewallIndex({ status, error }: Props) {
    usePoll(10_000, { only: ['status', 'error'] });

    return (
        <>
            <Head title="Firewall" />

            <PageHeader
                title="Firewall"
                description="Firewall server (nftables) yang kamu kelola dari sini. Mati sampai kamu nyalakan. Port layanan RAYXO (SSH, panel, web, email, FTP, DNS) selalu terbuka, dan setiap perubahan dicoba dulu — kalau tidak kamu konfirmasi dalam 5 menit, otomatis dikembalikan supaya kamu tidak terkunci."
            />

            {error !== null && (
                <Alert variant="destructive">
                    <ShieldAlert />
                    <AlertDescription>{error}</AlertDescription>
                </Alert>
            )}

            {status !== null && (
                <>
                    {status.pending && <PendingTrial trial={status.pending} />}

                    <Panel
                        title="Status"
                        description="Firewall hanya memblokir koneksi masuk. Koneksi keluar server dan balasan koneksi yang sedang berjalan tidak terpengaruh."
                    >
                        <div className="flex items-center gap-3">
                            {status.active.enabled ? (
                                <StatusBadge status="active" label="Aktif" />
                            ) : (
                                <StatusBadge status="cancelled" label="Mati" />
                            )}
                            <span className="text-sm text-muted-foreground">
                                {status.active.enabled
                                    ? 'Firewall menyaring koneksi masuk sesuai aturan di bawah.'
                                    : 'Semua port terbuka seperti biasa (diatur di provider VPS-mu).'}
                            </span>
                        </div>

                        <div className="mt-4 text-sm">
                            <div className="font-medium">Selalu terbuka</div>
                            <p className="mt-1 text-muted-foreground">
                                Port layanan RAYXO tidak bisa ditutup, supaya
                                kamu tidak terkunci:
                            </p>
                            <p className="mt-1 font-mono text-xs break-all text-muted-foreground">
                                TCP {status.always_open_tcp.join(', ')},{' '}
                                {status.ftp_passive} (FTP passive) · UDP{' '}
                                {status.always_open_udp.join(', ')}
                            </p>
                        </div>
                    </Panel>

                    {!status.pending && <Editor active={status.active} />}
                </>
            )}
        </>
    );
}

function PendingTrial({ trial }: { trial: Trial }) {
    const [processing, setProcessing] = useState<'confirm' | 'cancel' | null>(
        null,
    );

    const send = (which: 'confirm' | 'cancel') => {
        setProcessing(which);
        const route = which === 'confirm' ? confirmRoute() : cancelRoute();
        router.post(
            route.url,
            {},
            {
                preserveScroll: true,
                onFinish: () => setProcessing(null),
            },
        );
    };

    return (
        <Alert>
            <TriangleAlert />
            <AlertDescription>
                <div className="space-y-3">
                    <p>
                        Perubahan firewall sedang <strong>dicoba</strong>. Kalau
                        kamu masih bisa membuka halaman ini, berarti aksesmu
                        aman — konfirmasi supaya perubahan ini permanen. Kalau
                        tidak dikonfirmasi, firewall otomatis dikembalikan{' '}
                        <span
                            className="whitespace-nowrap"
                            title={formatDateTime(trial.expires_at)}
                        >
                            {formatRelative(trial.expires_at)}
                        </span>
                        .
                    </p>
                    <div className="flex gap-2">
                        <Button
                            size="sm"
                            disabled={processing !== null}
                            onClick={() => send('confirm')}
                        >
                            {processing === 'confirm' && <Spinner />}
                            Konfirmasi
                        </Button>
                        <Button
                            size="sm"
                            variant="secondary"
                            disabled={processing !== null}
                            onClick={() => send('cancel')}
                        >
                            {processing === 'cancel' && <Spinner />}
                            Batalkan sekarang
                        </Button>
                    </div>
                </div>
            </AlertDescription>
        </Alert>
    );
}

function Editor({ active }: { active: Config }) {
    // Guard against a config whose arrays arrive null (e.g. an older agent):
    // the fields are typed as arrays, but never crash the page if one is not.
    const tcpPorts = active.tcp_ports ?? [];
    const udpPorts = active.udp_ports ?? [];
    const allowIps = active.allow_ips ?? [];
    const denyIps = active.deny_ips ?? [];
    const activeKey = JSON.stringify(active);
    const [enabled, setEnabled] = useState(active.enabled);
    const [tcp, setTcp] = useState(tcpPorts.map(String));
    const [udp, setUdp] = useState(udpPorts.map(String));
    const [allow, setAllow] = useState(allowIps);
    const [deny, setDeny] = useState(denyIps);
    const [confirming, setConfirming] = useState(false);
    const [saving, setSaving] = useState(false);
    const [formError, setFormError] = useState<string | null>(null);

    // Re-sync from the server only when the confirmed config actually changes.
    useEffect(() => {
        setEnabled(active.enabled);
        setTcp((active.tcp_ports ?? []).map(String));
        setUdp((active.udp_ports ?? []).map(String));
        setAllow(active.allow_ips ?? []);
        setDeny(active.deny_ips ?? []);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [activeKey]);

    const submit = () => {
        setSaving(true);
        setFormError(null);
        router.put(
            applyRoute().url,
            {
                enabled,
                tcp_ports: tcp.map(Number),
                udp_ports: udp.map(Number),
                allow_ips: allow,
                deny_ips: deny,
            },
            {
                preserveScroll: true,
                onError: (errors) =>
                    setFormError(Object.values(errors)[0] ?? null),
                onSuccess: () => setConfirming(false),
                onFinish: () => setSaving(false),
            },
        );
    };

    const onApply = () => {
        if (enabled) {
            setConfirming(true);
        } else {
            submit();
        }
    };

    return (
        <Panel
            title="Aturan"
            description="Port tambahan di luar layanan RAYXO, dan alamat yang selalu diizinkan atau selalu diblokir."
        >
            <div className="flex items-center gap-3">
                <input
                    id="fw-enabled"
                    type="checkbox"
                    className="size-4"
                    checked={enabled}
                    onChange={(event) => setEnabled(event.target.checked)}
                />
                <label htmlFor="fw-enabled" className="text-sm font-medium">
                    Nyalakan firewall
                </label>
            </div>

            <div className="mt-6 grid gap-6 lg:grid-cols-2">
                <ListField
                    label="Port TCP tambahan"
                    placeholder="mis. 8080"
                    values={tcp}
                    onChange={setTcp}
                    valid={isPort}
                    invalidMessage="Masukkan nomor port 1–65535."
                />
                <ListField
                    label="Port UDP tambahan"
                    placeholder="mis. 1194"
                    values={udp}
                    onChange={setUdp}
                    valid={isPort}
                    invalidMessage="Masukkan nomor port 1–65535."
                />
                <ListField
                    label="Selalu izinkan (IP/CIDR)"
                    placeholder="mis. 203.0.113.7 atau 203.0.113.0/24"
                    values={allow}
                    onChange={setAllow}
                    valid={isIpOrCidr}
                    invalidMessage="Masukkan alamat IP atau rentang CIDR yang benar."
                />
                <ListField
                    label="Selalu blokir (IP/CIDR)"
                    placeholder="mis. 198.51.100.9"
                    values={deny}
                    onChange={setDeny}
                    valid={isIpOrCidr}
                    invalidMessage="Masukkan alamat IP atau rentang CIDR yang benar."
                />
            </div>

            {formError && (
                <Alert variant="destructive" className="mt-4">
                    <AlertDescription className="break-all">
                        {formError}
                    </AlertDescription>
                </Alert>
            )}

            <div className="mt-6">
                <Button disabled={saving} onClick={onApply}>
                    {saving && <Spinner />}
                    {enabled ? 'Terapkan firewall' : 'Matikan firewall'}
                </Button>
            </div>

            <Dialog open={confirming} onOpenChange={setConfirming}>
                <DialogContent className="rounded-2xl">
                    <DialogHeader>
                        <DialogTitle>Nyalakan firewall?</DialogTitle>
                        <DialogDescription>
                            Firewall akan aktif untuk masa coba 5 menit.
                            Pastikan kamu masih bisa membuka panel dan SSH
                            setelahnya, lalu klik Konfirmasi di banner yang
                            muncul. Kalau tidak dikonfirmasi, firewall otomatis
                            dikembalikan ke keadaan sebelumnya — jadi kamu tidak
                            akan terkunci.
                        </DialogDescription>
                    </DialogHeader>
                    <DialogFooter className="gap-2">
                        <DialogClose asChild>
                            <Button type="button" variant="secondary">
                                Batal
                            </Button>
                        </DialogClose>
                        <Button disabled={saving} onClick={submit}>
                            {saving && <Spinner />}
                            Terapkan untuk dicoba
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </Panel>
    );
}

function ListField({
    label,
    placeholder,
    values,
    onChange,
    valid,
    invalidMessage,
}: {
    label: string;
    placeholder: string;
    values: string[];
    onChange: (values: string[]) => void;
    valid: (value: string) => boolean;
    invalidMessage: string;
}) {
    const [draft, setDraft] = useState('');
    const [error, setError] = useState<string | null>(null);

    const add = () => {
        const value = draft.trim();
        if (value === '') {
            return;
        }

        if (!valid(value)) {
            setError(invalidMessage);
            return;
        }

        if (!values.includes(value)) {
            onChange([...values, value]);
        }
        setDraft('');
        setError(null);
    };

    return (
        <div>
            <div className="text-sm font-medium">{label}</div>
            <div className="mt-2 flex gap-2">
                <Input
                    value={draft}
                    placeholder={placeholder}
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono"
                    onChange={(event) => {
                        setDraft(event.target.value);
                        setError(null);
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
                </Button>
            </div>
            {error && (
                <p className="mt-1 text-sm text-destructive-foreground">
                    {error}
                </p>
            )}
            {values.length > 0 && (
                <ul className="mt-2 flex flex-wrap gap-2">
                    {values.map((value) => (
                        <li
                            key={value}
                            className="flex items-center gap-1 rounded-md border px-2 py-1 font-mono text-xs"
                        >
                            {value}
                            <button
                                type="button"
                                aria-label={`Hapus ${value}`}
                                className="text-muted-foreground hover:text-foreground"
                                onClick={() =>
                                    onChange(values.filter((v) => v !== value))
                                }
                            >
                                <X className="size-3.5" />
                            </button>
                        </li>
                    ))}
                </ul>
            )}
        </div>
    );
}
