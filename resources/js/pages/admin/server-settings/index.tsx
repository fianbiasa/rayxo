import { Form, Head, router, useForm, usePoll } from '@inertiajs/react';
import {
    ArrowUpCircle,
    CheckCircle2,
    Clock,
    Mail,
    MailCheck,
    Network,
    PencilLine,
    Send,
    ShieldAlert,
    ShieldCheck,
    Upload,
} from 'lucide-react';
import type { FormEvent, ReactNode } from 'react';
import { useEffect, useState } from 'react';
import InputError from '@/components/input-error';
import { PageHeader } from '@/components/page-header';
import { Panel } from '@/components/panel';
import {
    Field,
    indonesianTimezones,
    SizeField,
} from '@/components/php-settings-form';
import { StatusBadge } from '@/components/status-badge';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
    Dialog,
    DialogClose,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Spinner } from '@/components/ui/spinner';
import { formatDateTime, formatRelative } from '@/lib/format';
import {
    bandwidth as updateBandwidth,
    clamav as updateClamav,
    clamavScan,
    customSmtp as updateCustomSmtp,
    databaseRemoteAccess as updateDatabaseRemoteAccess,
    hostname as updateHostname,
    hostnameCertificate,
    ioncube as updateIonCube,
    mailLimits as updateMailLimits,
    nameservers as updateNameservers,
    phpUploads,
    port as tryPort,
    securityPolicy as updateSecurityPolicy,
    telegram as updateTelegram,
    timezone as updateTimezone,
    update as startUpdate,
    updateChannel as setUpdateChannel,
    waf as updateWaf,
} from '@/routes/admin/server-settings';
import { test as testCustomSmtp } from '@/routes/admin/server-settings/custom-smtp';
import { destroy as removeTelegram } from '@/routes/admin/server-settings/telegram';
import {
    cancel as cancelPort,
    confirm as confirmPort,
} from '@/routes/admin/server-settings/port';

type HostnameCertificate = {
    hostname: string;
    names: string[];
    issuer: string;
    expires_at: string;
    lets_encrypt: boolean;
};

type CertificateRequest = { pending: boolean; error: string | null };

/** The latest hostname change: setup.sh running again in the background. */
type Reconfigure = {
    hostname?: string;
    panel_port?: number;
    started_at: string;
    running: boolean;
    exit_code: number | null;
    error: string;
    log: string[];
} | null;

/** A port opened next to the panel's own, until confirmed or closed. */
type PortTrial = {
    port: number;
    previous: number;
    expires_at: string;
    confirmed: boolean;
};

/** The latest one-click update: setup.sh of a new release running in the background. */
type UpdateState = {
    version: string;
    started_at: string;
    running: boolean;
    exit_code: number | null;
    error: string;
    log: string[];
} | null;

/** A published release the panel can update to. */
type Release = {
    version: string;
    tag: string;
    prerelease: boolean;
    setup_url: string;
    notes_url: string;
};

type UpdateInfo = {
    installed: string;
    channel: 'stable' | 'beta';
    stable: Release | null;
    beta: Release | null;
    available: Release | null;
};

type Props = {
    server: {
        hostname: string;
        version: string;
        timezone: string;
        certificate: HostnameCertificate | null;
        reconfigure: Reconfigure;
        update: UpdateState;
        panel_port: number;
        port_trial: PortTrial | null;
    } | null;
    error: string | null;
    certificate_request: CertificateRequest;
    timezones: string[];
    php_uploads: { limit: string; default: string; sizes: string[] };
    nameservers: string[];
    public_ip: string | null;
    mail_limits: MailLimits;
    bandwidth: { suspend_over_limit: boolean };
    database_remote_access: boolean;
    ioncube: IonCube;
    clamav: Clamav;
    waf: Waf;
    custom_smtp: CustomSmtp;
    update: UpdateInfo;
    telegram_bot: string | null;
    security_policy: SecurityPolicy;
};

type Waf = { mode: 'off' | 'detection' | 'on' };

type CustomSmtp = {
    enabled: boolean;
    host: string;
    port: number;
    username: string;
    encryption: 'none' | 'tls' | 'ssl';
    from_address: string;
    from_name: string;
    password_set: boolean;
};

type IonCube = {
    enabled: boolean;
    versions: string[];
    missing: string[];
    loader: string | null;
};

type ClamavSettings = {
    enabled: boolean;
    email: boolean;
    on_access: boolean;
    scheduled: boolean;
};

type Clamav = ClamavSettings;

type MailLimits = {
    account: number | null;
    server: number | null;
    default_account: number;
};

type SecurityPolicy = {
    require_2fa: string[];
    password_min_length: { admin: number; reseller: number; user: number };
    default_min: number;
    max_min: number;
};

export default function ServerSettings({
    server,
    error,
    certificate_request,
    timezones,
    php_uploads,
    nameservers,
    public_ip,
    mail_limits,
    bandwidth,
    database_remote_access,
    ioncube,
    clamav,
    waf,
    custom_smtp,
    update,
    telegram_bot,
    security_policy,
}: Props) {
    const { start, stop } = usePoll(
        5_000,
        { only: ['server', 'certificate_request'] },
        { autoStart: false },
    );

    const busy =
        certificate_request.pending ||
        server?.reconfigure?.running === true ||
        server?.update?.running === true ||
        (server?.port_trial ?? null) !== null;

    // Follow a certificate request or a hostname change until it finishes.
    useEffect(() => {
        if (busy) {
            start();
        } else {
            stop();
        }
    }, [busy, start, stop]);

    return (
        <>
            <Head title="Pengaturan Server" />

            <PageHeader
                title="Pengaturan Server"
                description="Hostname dan SSL panel, zona waktu server, ukuran upload yang boleh dipakai website, batas kirim email, dan nameserver DNS."
            />

            {error !== null && (
                <Alert variant="destructive">
                    <ShieldAlert />
                    <AlertDescription>{error}</AlertDescription>
                </Alert>
            )}

            {server !== null && (
                <HostnamePanel
                    hostname={server.hostname}
                    certificate={server.certificate}
                    request={certificate_request}
                />
            )}

            {server !== null && (
                <HostnameChangePanel
                    current={server.hostname}
                    change={hostnameChange(server.reconfigure, server.hostname)}
                />
            )}

            {server !== null && (
                <PortPanel
                    current={server.panel_port}
                    trial={server.port_trial}
                    change={
                        server.reconfigure?.panel_port
                            ? server.reconfigure
                            : null
                    }
                />
            )}

            <UpdatePanel info={update} change={server?.update ?? null} />

            <div className="grid gap-6 lg:grid-cols-2">
                {server !== null && (
                    <TimezonePanel
                        current={server.timezone}
                        timezones={timezones}
                    />
                )}
                <UploadPanel uploads={php_uploads} />
            </div>

            <MailLimitPanel limits={mail_limits} />

            <SecurityPolicyPanel policy={security_policy} />

            <BandwidthPanel suspendOverLimit={bandwidth.suspend_over_limit} />

            <DatabaseRemotePanel enabled={database_remote_access} />

            <IonCubePanel ioncube={ioncube} />
            <ClamavPanel clamav={clamav} />
            <WafPanel waf={waf} />
            <CustomSmtpPanel customSmtp={custom_smtp} />

            <TelegramPanel bot={telegram_bot} />

            <NameserverPanel current={nameservers} publicIp={public_ip} />
        </>
    );
}

/**
 * The hostname change to show: none when the latest run of setup.sh was
 * for the port, or failed for the name the server has by now (changed
 * outside the panel, such as by the VPS provider along with the PTR).
 */
function hostnameChange(change: Reconfigure, current: string): Reconfigure {
    if (!change?.hostname) {
        return null;
    }

    const stale =
        !change.running &&
        change.exit_code !== 0 &&
        change.hostname === current;

    return stale ? null : change;
}

function HostnamePanel({
    hostname,
    certificate,
    request,
}: {
    hostname: string;
    certificate: HostnameCertificate | null;
    request: CertificateRequest;
}) {
    const [confirming, setConfirming] = useState(false);
    const [processing, setProcessing] = useState(false);
    const [actionError, setActionError] = useState<string | null>(null);

    const issue = () => {
        setProcessing(true);
        setActionError(null);
        router.post(
            hostnameCertificate().url,
            {},
            {
                preserveScroll: true,
                onSuccess: () => setConfirming(false),
                onError: (errors) =>
                    setActionError(Object.values(errors)[0] ?? null),
                onFinish: () => setProcessing(false),
            },
        );
    };

    return (
        <Panel
            title="Hostname & SSL"
            description="Sertifikat ini dipakai panel, phpMyAdmin dan FTP. Let's Encrypt memperpanjangnya otomatis."
            action={
                <Button
                    variant="secondary"
                    disabled={request.pending}
                    onClick={() => setConfirming(true)}
                >
                    {request.pending ? <Spinner /> : <ShieldCheck />}
                    {request.pending
                        ? 'Sedang meminta sertifikat…'
                        : 'Terbitkan ulang SSL'}
                </Button>
            }
        >
            <dl className="grid gap-x-8 gap-y-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
                <Detail label="Hostname">
                    <span className="font-mono">{hostname || '—'}</span>
                </Detail>
                <Detail label="Sertifikat">
                    {certificate === null ? (
                        <StatusBadge status="failed" label="Tidak terbaca" />
                    ) : certificate.lets_encrypt ? (
                        <StatusBadge status="active" label="Let's Encrypt" />
                    ) : (
                        <StatusBadge status="suspended" label="Self-signed" />
                    )}
                </Detail>
                <Detail label="Penerbit">{certificate?.issuer || '—'}</Detail>
                <Detail label="Berlaku sampai">
                    {certificate
                        ? `${formatDateTime(certificate.expires_at)} (${formatRelative(certificate.expires_at)})`
                        : '—'}
                </Detail>
            </dl>

            {certificate !== null && !certificate.lets_encrypt && (
                <Alert className="mt-5">
                    <ShieldAlert />
                    <AlertDescription>
                        Panel masih memakai sertifikat self-signed, jadi browser
                        menampilkan peringatan. Pastikan A record {hostname}{' '}
                        mengarah ke server ini dan port 80 terbuka, lalu
                        terbitkan ulang SSL.
                    </AlertDescription>
                </Alert>
            )}

            {request.error !== null && (
                <Alert variant="destructive" className="mt-5">
                    <ShieldAlert />
                    <AlertDescription className="break-words">
                        Permintaan terakhir gagal: {request.error}
                    </AlertDescription>
                </Alert>
            )}

            <Dialog
                open={confirming}
                onOpenChange={(open) => {
                    setConfirming(open);
                    setActionError(null);
                }}
            >
                <DialogContent className="rounded-2xl">
                    <DialogHeader>
                        <DialogTitle>
                            Terbitkan ulang SSL {hostname}?
                        </DialogTitle>
                        <DialogDescription>
                            Let's Encrypt memeriksa dulu bahwa {hostname} sampai
                            ke server ini lewat port 80. Sertifikat yang masih
                            berlaku lebih dari 30 hari dipakai lagi; panel dan
                            FTP lalu memakai sertifikat Let's Encrypt itu.
                        </DialogDescription>
                    </DialogHeader>
                    {actionError && (
                        <Alert variant="destructive">
                            <AlertDescription>{actionError}</AlertDescription>
                        </Alert>
                    )}
                    <DialogFooter className="gap-2">
                        <DialogClose asChild>
                            <Button type="button" variant="secondary">
                                Batal
                            </Button>
                        </DialogClose>
                        <Button disabled={processing} onClick={issue}>
                            {processing && <Spinner />}
                            Terbitkan ulang
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </Panel>
    );
}

function HostnameChangePanel({
    current,
    change,
}: {
    current: string;
    change: Reconfigure;
}) {
    const [open, setOpen] = useState(false);
    const port = typeof window === 'undefined' ? '' : window.location.port;
    const panelUrl = (name: string) =>
        `https://${name}${port ? `:${port}` : ''}`;

    return (
        <Panel
            title="Ganti hostname"
            description="Semua yang memakai hostname ikut diganti: SSL panel, phpMyAdmin, FTP, email panel (Postfix & DKIM) dan alamat panel."
            action={
                <Button
                    variant="secondary"
                    disabled={change?.running === true}
                    onClick={() => setOpen(true)}
                >
                    <PencilLine />
                    Ganti hostname
                </Button>
            }
        >
            {change === null ? (
                <p className="text-sm text-muted-foreground">
                    Syaratnya sama seperti saat instalasi: A record hostname
                    baru mengarah ke IP server ini, dan PTR (reverse DNS) IP
                    server mengarah ke hostname baru, diatur lewat provider VPS.
                    Kalau belum benar, tidak ada yang diubah.
                </p>
            ) : change.running ? (
                <Alert>
                    <Spinner />
                    <AlertDescription>
                        Mengganti hostname ke{' '}
                        <span className="font-mono">
                            {change.hostname ?? ''}
                        </span>
                        … Panel sempat tidak menjawab sebentar saat layanannya
                        dijalankan ulang; halaman ini diperbarui sendiri.
                    </AlertDescription>
                </Alert>
            ) : change.exit_code === 0 ? (
                <Alert>
                    <CheckCircle2 />
                    <AlertDescription>
                        Hostname sekarang{' '}
                        <span className="font-mono">
                            {change.hostname ?? ''}
                        </span>
                        . Buka panel di{' '}
                        <a
                            className="font-medium underline"
                            href={panelUrl(change.hostname ?? '')}
                        >
                            {panelUrl(change.hostname ?? '')}
                        </a>
                        , lalu perbarui rekaman SPF, DKIM dan DMARC di DNS
                        sesuai /etc/rayxo/dns-records.txt.
                    </AlertDescription>
                </Alert>
            ) : (
                <Alert variant="destructive">
                    <ShieldAlert />
                    <AlertDescription className="break-words">
                        Hostname tidak diganti ke{' '}
                        <span className="font-mono">
                            {change.hostname ?? ''}
                        </span>
                        :{' '}
                        {change.error ||
                            'proses berhenti sebelum selesai; lihat log di bawah.'}
                    </AlertDescription>
                </Alert>
            )}

            {change !== null && change.log.length > 0 && (
                <details className="mt-4 text-sm" open={change.running}>
                    <summary className="cursor-pointer text-muted-foreground">
                        Log proses
                    </summary>
                    <pre className="mt-2 max-h-72 overflow-auto rounded-xl bg-muted p-3 text-xs leading-relaxed whitespace-pre-wrap">
                        {change.log.join('\n')}
                    </pre>
                </details>
            )}

            <Dialog open={open} onOpenChange={setOpen}>
                <DialogContent className="rounded-2xl">
                    <Form
                        {...updateHostname.form()}
                        options={{ preserveScroll: true }}
                        onSuccess={() => setOpen(false)}
                        className="grid gap-5"
                    >
                        {({ processing, errors }) => (
                            <>
                                <DialogHeader>
                                    <DialogTitle>
                                        Ganti hostname server
                                    </DialogTitle>
                                    <DialogDescription>
                                        Sekarang{' '}
                                        <span className="font-mono">
                                            {current}
                                        </span>
                                        . Sebelum lanjut, arahkan A record
                                        hostname baru ke server ini dan minta
                                        provider VPS mengatur PTR ke hostname
                                        baru. Prosesnya beberapa menit; setelah
                                        selesai panel dibuka lewat hostname
                                        baru.
                                    </DialogDescription>
                                </DialogHeader>
                                <Field
                                    id="hostname"
                                    label="Hostname baru"
                                    error={errors.hostname}
                                >
                                    <Input
                                        id="hostname"
                                        name="hostname"
                                        placeholder="server2.domainmu.com"
                                        autoComplete="off"
                                        className="rounded-xl font-mono"
                                        required
                                    />
                                </Field>
                                <DialogFooter className="gap-2">
                                    <DialogClose asChild>
                                        <Button
                                            type="button"
                                            variant="secondary"
                                        >
                                            Batal
                                        </Button>
                                    </DialogClose>
                                    <Button type="submit" disabled={processing}>
                                        {processing && <Spinner />}
                                        Ganti hostname
                                    </Button>
                                </DialogFooter>
                            </>
                        )}
                    </Form>
                </DialogContent>
            </Dialog>
        </Panel>
    );
}

function PortPanel({
    current,
    trial,
    change,
}: {
    current: number;
    trial: PortTrial | null;
    change: Reconfigure;
}) {
    const [processing, setProcessing] = useState(false);
    const [actionError, setActionError] = useState<string | null>(null);
    const browser = typeof window === 'undefined' ? null : window.location;
    const onTrialPort =
        trial !== null &&
        browser !== null &&
        browser.port === String(trial.port);
    const trialUrl =
        trial !== null && browser !== null
            ? `https://${browser.hostname}:${trial.port}${browser.pathname}`
            : '';

    const send = (action: 'confirm' | 'cancel') => {
        setProcessing(true);
        setActionError(null);
        const options = {
            preserveScroll: true,
            onError: (errors: Record<string, string>) =>
                setActionError(Object.values(errors)[0] ?? null),
            onFinish: () => setProcessing(false),
        };

        if (action === 'confirm') {
            router.post(confirmPort().url, {}, options);
        } else {
            router.delete(cancelPort().url, options);
        }
    };

    return (
        <Panel
            title="Port panel"
            description={`Panel sekarang di port ${current}. Port baru dibuka dulu berdampingan, lalu dipakai setelah dikonfirmasi lewat port itu; kalau tidak dikonfirmasi dalam 5 menit, port baru ditutup lagi otomatis.`}
        >
            {actionError && (
                <Alert variant="destructive" className="mb-4">
                    <ShieldAlert />
                    <AlertDescription>{actionError}</AlertDescription>
                </Alert>
            )}

            {change !== null && (change.running || trial?.confirmed) ? (
                <Alert>
                    <Spinner />
                    <AlertDescription>
                        Memindahkan panel ke port {change.panel_port}… Halaman
                        ini diperbarui sendiri.
                    </AlertDescription>
                </Alert>
            ) : trial !== null && onTrialPort ? (
                <div className="grid gap-4">
                    <Alert>
                        <CheckCircle2 />
                        <AlertDescription>
                            Port {trial.port} bisa dijangkau dari jaringan kamu.
                            Pakai port ini untuk panel? Port {trial.previous}{' '}
                            ditutup setelahnya.
                        </AlertDescription>
                    </Alert>
                    <div className="flex flex-wrap gap-2">
                        <Button
                            disabled={processing}
                            onClick={() => send('confirm')}
                        >
                            {processing && <Spinner />}
                            Pakai port {trial.port}
                        </Button>
                        <Button
                            variant="secondary"
                            disabled={processing}
                            onClick={() => send('cancel')}
                        >
                            Batalkan
                        </Button>
                    </div>
                </div>
            ) : trial !== null ? (
                <div className="grid gap-4">
                    <Alert>
                        <Clock />
                        <AlertDescription>
                            Port {trial.port} dibuka sementara sampai{' '}
                            {formatDateTime(trial.expires_at)}. Buka{' '}
                            <a
                                className="font-medium underline"
                                href={trialUrl}
                            >
                                {trialUrl}
                            </a>{' '}
                            lalu konfirmasi di sana. Kalau halaman itu tidak
                            terbuka, port {trial.port} kemungkinan diblokir
                            firewall provider; biarkan saja, nanti ditutup
                            otomatis.
                        </AlertDescription>
                    </Alert>
                    <div>
                        <Button
                            variant="secondary"
                            disabled={processing}
                            onClick={() => send('cancel')}
                        >
                            {processing && <Spinner />}
                            Batalkan
                        </Button>
                    </div>
                </div>
            ) : (
                <Form
                    {...tryPort.form()}
                    options={{ preserveScroll: true }}
                    className="grid gap-4"
                >
                    {({ processing: trying, errors }) => (
                        <>
                            {change !== null &&
                                !change.running &&
                                (change.exit_code === 0 ? (
                                    <Alert>
                                        <CheckCircle2 />
                                        <AlertDescription>
                                            Panel sekarang di port{' '}
                                            {change.panel_port}.
                                        </AlertDescription>
                                    </Alert>
                                ) : (
                                    <Alert variant="destructive">
                                        <ShieldAlert />
                                        <AlertDescription className="break-words">
                                            Panel tidak dipindah ke port{' '}
                                            {change.panel_port}:{' '}
                                            {change.error ||
                                                'proses berhenti sebelum selesai.'}
                                        </AlertDescription>
                                    </Alert>
                                ))}
                            <div className="grid gap-4 sm:grid-cols-[1fr_auto] sm:items-end">
                                <Field
                                    id="port"
                                    label="Port baru"
                                    error={errors.port}
                                >
                                    <Input
                                        id="port"
                                        name="port"
                                        type="number"
                                        min={1024}
                                        max={65535}
                                        placeholder="misalnya 8443"
                                        className="rounded-xl"
                                        required
                                    />
                                </Field>
                                <Button type="submit" disabled={trying}>
                                    {trying && <Spinner />}
                                    Coba port ini
                                </Button>
                            </div>
                            <p className="text-sm text-muted-foreground">
                                Buka dulu port itu di firewall atau security
                                group provider VPS.
                            </p>
                        </>
                    )}
                </Form>
            )}
        </Panel>
    );
}

function TimezonePanel({
    current,
    timezones,
}: {
    current: string;
    timezones: string[];
}) {
    const [value, setValue] = useState(current || 'UTC');
    const ordered = [
        ...indonesianTimezones,
        ...timezones.filter((zone) => !indonesianTimezones.includes(zone)),
    ];

    return (
        <Panel
            title="Zona waktu"
            description="Dipakai jadwal cron job dan log server. Website punya zona waktunya sendiri di pengaturan PHP."
        >
            <Form
                {...updateTimezone.form()}
                options={{ preserveScroll: true }}
                className="grid gap-4"
            >
                {({ processing, errors }) => (
                    <>
                        <Field
                            id="timezone"
                            label="Zona waktu server"
                            error={errors.timezone}
                        >
                            <input
                                type="hidden"
                                name="timezone"
                                value={value}
                            />
                            <Select value={value} onValueChange={setValue}>
                                <SelectTrigger
                                    id="timezone"
                                    aria-invalid={
                                        errors.timezone ? true : undefined
                                    }
                                    className="w-full rounded-xl bg-card px-3.5 data-[size=default]:h-11"
                                >
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="max-h-80 rounded-xl">
                                    {ordered.map((zone) => (
                                        <SelectItem key={zone} value={zone}>
                                            {zone}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </Field>
                        <div>
                            <Button
                                type="submit"
                                disabled={processing || value === current}
                            >
                                {processing ? <Spinner /> : <Clock />}
                                Simpan zona waktu
                            </Button>
                        </div>
                    </>
                )}
            </Form>
        </Panel>
    );
}

function UploadPanel({
    uploads,
}: {
    uploads: { limit: string; default: string; sizes: string[] };
}) {
    const [limit, setLimit] = useState(uploads.limit);
    const [fallback, setFallback] = useState(uploads.default);

    return (
        <Panel
            title="Batas upload PHP"
            description="Website yang belum memilih ukurannya sendiri memakai ukuran bawaan. Menyimpan perubahan menerapkan ulang semua website."
        >
            <Form
                {...phpUploads.form()}
                options={{ preserveScroll: true }}
                className="grid gap-4"
            >
                {({ processing, errors }) => (
                    <>
                        <div className="grid gap-4 sm:grid-cols-2">
                            <SizeField
                                id="limit"
                                label="Maksimum yang boleh dipilih"
                                value={limit}
                                sizes={uploads.sizes}
                                onChange={setLimit}
                                error={errors.limit}
                            />
                            <SizeField
                                id="default"
                                label="Ukuran bawaan"
                                value={fallback}
                                sizes={uploads.sizes}
                                onChange={setFallback}
                                error={errors.default}
                            />
                        </div>
                        <div>
                            <Button
                                type="submit"
                                disabled={
                                    processing ||
                                    (limit === uploads.limit &&
                                        fallback === uploads.default)
                                }
                            >
                                {processing ? <Spinner /> : <Upload />}
                                Simpan batas upload
                            </Button>
                        </div>
                    </>
                )}
            </Form>
        </Panel>
    );
}

/**
 * How many recipients the mail of hosting accounts may reach per 24 hours,
 * per account and for all accounts together. Empty is no limit.
 */
/**
 * The Telegram bot that sends alerts to the chats accounts set up under
 * Settings > Notifikasi. Its token is kept encrypted and never shown again.
 */
function TelegramPanel({ bot }: { bot: string | null }) {
    return (
        <Panel
            title="Bot Telegram untuk notifikasi"
            description="Buat bot di @BotFather (perintah /newbot), lalu tempel token-nya di sini. Setiap akun memilih sendiri notifikasi apa yang dikirim bot ke chat Telegram-nya, di Pengaturan > Notifikasi."
        >
            <div className="grid gap-4">
                {bot !== null && (
                    <div className="flex flex-wrap items-center gap-3">
                        <StatusBadge status="active" label={`@${bot}`} />
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={() =>
                                router.delete(removeTelegram.url(), {
                                    preserveScroll: true,
                                })
                            }
                        >
                            Lepas bot
                        </Button>
                    </div>
                )}
                <Form
                    {...updateTelegram.form()}
                    options={{ preserveScroll: true }}
                    resetOnSuccess
                    className="flex flex-wrap items-start gap-3"
                >
                    {({ processing, errors }) => (
                        <>
                            <div className="grid min-w-72 flex-1 gap-1.5">
                                <Input
                                    name="token"
                                    type="password"
                                    autoComplete="off"
                                    required
                                    maxLength={100}
                                    placeholder={
                                        bot === null
                                            ? '123456789:AA…'
                                            : 'Token baru untuk mengganti bot'
                                    }
                                    aria-label="Token bot"
                                    aria-invalid={
                                        errors.token ? true : undefined
                                    }
                                />
                                <InputError message={errors.token} />
                            </div>
                            <Button type="submit" disabled={processing}>
                                {processing && <Spinner />}
                                {bot === null ? 'Pasang bot' : 'Ganti bot'}
                            </Button>
                        </>
                    )}
                </Form>
            </div>
        </Panel>
    );
}

/**
 * Whether MariaDB answers from outside the server, for the hosts accounts
 * allow on their Databases page. Changing it restarts MariaDB.
 */
/**
 * The ionCube Loader, which software encoded with ionCube (such as WHMCS)
 * needs. It is ionCube's own software, downloaded from ionCube only once
 * the administrator turns it on.
 */
function UpdatePanel({
    info,
    change,
}: {
    info: UpdateInfo;
    change: UpdateState;
}) {
    const [open, setOpen] = useState(false);
    const [channel, setChannel] = useState<UpdateInfo['channel']>(info.channel);

    const channels: { value: UpdateInfo['channel']; label: string }[] = [
        { value: 'stable', label: 'Stable (rilis resmi)' },
        { value: 'beta', label: 'Beta (termasuk pra-rilis)' },
    ];

    const latest = info.channel === 'beta' ? info.beta : info.stable;

    return (
        <Panel
            title="Update panel"
            description="Perbarui RAYXO langsung dari sini. Agent menjalankan setup.sh rilis baru di latar belakang; panel sempat tidak menjawab sebentar saat layanannya dijalankan ulang. Update ditolak saat ada backup atau task yang berjalan."
        >
            <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
                <span>
                    Versi terpasang:{' '}
                    <span className="font-mono font-medium">
                        {info.installed || 'tidak diketahui'}
                    </span>
                </span>
                {latest && (
                    <span className="text-muted-foreground">
                        Terbaru ({info.channel}):{' '}
                        <a
                            className="font-mono underline"
                            href={latest.notes_url}
                            target="_blank"
                            rel="noopener noreferrer"
                        >
                            v{latest.version}
                        </a>
                    </span>
                )}
            </div>

            <Form
                {...setUpdateChannel.form()}
                options={{ preserveScroll: true }}
                className="mt-4 flex flex-wrap items-center gap-3"
            >
                {({ processing }) => (
                    <>
                        <Label htmlFor="update-channel" className="text-sm">
                            Channel
                        </Label>
                        <input type="hidden" name="channel" value={channel} />
                        <Select
                            value={channel}
                            onValueChange={(value) =>
                                setChannel(value as UpdateInfo['channel'])
                            }
                        >
                            <SelectTrigger
                                id="update-channel"
                                className="w-64 rounded-xl bg-card px-3.5 data-[size=default]:h-11"
                            >
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="rounded-xl">
                                {channels.map((option) => (
                                    <SelectItem
                                        key={option.value}
                                        value={option.value}
                                    >
                                        {option.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <Button
                            type="submit"
                            variant="secondary"
                            disabled={processing || channel === info.channel}
                        >
                            Simpan channel
                        </Button>
                    </>
                )}
            </Form>

            <div className="mt-4">
                {change?.running ? (
                    <Alert>
                        <Spinner />
                        <AlertDescription>
                            Memperbarui ke{' '}
                            <span className="font-mono">v{change.version}</span>
                            … Panel sempat tidak menjawab sebentar saat
                            layanannya dijalankan ulang; halaman ini diperbarui
                            sendiri.
                        </AlertDescription>
                    </Alert>
                ) : change && change.exit_code === 0 ? (
                    <Alert>
                        <CheckCircle2 />
                        <AlertDescription>
                            Panel sudah diperbarui ke{' '}
                            <span className="font-mono">v{change.version}</span>
                            .
                        </AlertDescription>
                    </Alert>
                ) : change && change.exit_code !== null ? (
                    <Alert variant="destructive">
                        <ShieldAlert />
                        <AlertDescription className="break-words">
                            Update ke{' '}
                            <span className="font-mono">v{change.version}</span>{' '}
                            gagal:{' '}
                            {change.error ||
                                'proses berhenti sebelum selesai; lihat log di bawah.'}
                        </AlertDescription>
                    </Alert>
                ) : info.available ? (
                    <div className="flex flex-wrap items-center gap-4">
                        <StatusBadge
                            status="suspended"
                            label={`Update tersedia: v${info.available.version}`}
                        />
                        <Button onClick={() => setOpen(true)}>
                            <ArrowUpCircle />
                            Update ke v{info.available.version}
                        </Button>
                    </div>
                ) : (
                    <p className="text-sm text-muted-foreground">
                        {info.installed === ''
                            ? 'Versi terpasang tidak terbaca (agent tidak menjawab).'
                            : 'Panel sudah versi terbaru di channel ini.'}
                    </p>
                )}
            </div>

            {change !== null && change.log.length > 0 && (
                <details className="mt-4 text-sm" open={change.running}>
                    <summary className="cursor-pointer text-muted-foreground">
                        Log proses
                    </summary>
                    <pre className="mt-2 max-h-72 overflow-auto rounded-xl bg-muted p-3 text-xs leading-relaxed whitespace-pre-wrap">
                        {change.log.join('\n')}
                    </pre>
                </details>
            )}

            <Dialog open={open} onOpenChange={setOpen}>
                <DialogContent className="rounded-2xl">
                    <Form
                        {...startUpdate.form()}
                        options={{ preserveScroll: true }}
                        onSuccess={() => setOpen(false)}
                        className="grid gap-5"
                    >
                        {({ processing }) => (
                            <>
                                <DialogHeader>
                                    <DialogTitle>Update panel</DialogTitle>
                                    <DialogDescription>
                                        Perbarui dari{' '}
                                        <span className="font-mono">
                                            {info.installed || '?'}
                                        </span>{' '}
                                        ke{' '}
                                        <span className="font-mono">
                                            v{info.available?.version}
                                        </span>
                                        . Agent menjalankan setup.sh rilis baru;
                                        beberapa menit, dan panel sempat tidak
                                        menjawab saat layanan dijalankan ulang.
                                        Pastikan tidak ada backup penting yang
                                        sedang berjalan.
                                    </DialogDescription>
                                </DialogHeader>
                                <DialogFooter className="gap-2">
                                    <DialogClose asChild>
                                        <Button
                                            type="button"
                                            variant="secondary"
                                        >
                                            Batal
                                        </Button>
                                    </DialogClose>
                                    <Button type="submit" disabled={processing}>
                                        {processing && <Spinner />}
                                        Update sekarang
                                    </Button>
                                </DialogFooter>
                            </>
                        )}
                    </Form>
                </DialogContent>
            </Dialog>
        </Panel>
    );
}

function WafPanel({ waf }: { waf: Waf }) {
    const [mode, setMode] = useState<Waf['mode']>(waf.mode);

    const badge: Record<Waf['mode'], { status: string; label: string }> = {
        on: { status: 'active', label: 'Aktif' },
        detection: { status: 'suspended', label: 'Deteksi saja' },
        off: { status: 'neutral', label: 'Mati' },
    };

    const options: { value: Waf['mode']; label: string }[] = [
        { value: 'off', label: 'Mati' },
        { value: 'detection', label: 'Deteksi saja (catat, tidak blokir)' },
        { value: 'on', label: 'Aktif (blokir)' },
    ];

    return (
        <Panel
            title="ModSecurity (WAF)"
            description="Firewall aplikasi web dengan OWASP Core Rule Set, melindungi situs dari SQLi, XSS, dan serangan web umum. Ini mode bawaan untuk semua website; tiap domain bisa menimpanya di Pengaturan web. Mulai dari 'Deteksi saja' untuk melihat false-positive di log audit sebelum benar-benar memblokir."
        >
            <Form
                {...updateWaf.form()}
                options={{ preserveScroll: true }}
                className="flex flex-wrap items-center gap-4"
            >
                {({ processing, errors }) => (
                    <>
                        <StatusBadge
                            status={badge[waf.mode].status}
                            label={badge[waf.mode].label}
                        />
                        <input type="hidden" name="mode" value={mode} />
                        <Select
                            value={mode}
                            onValueChange={(value) =>
                                setMode(value as Waf['mode'])
                            }
                        >
                            <SelectTrigger
                                id="waf-mode"
                                aria-invalid={errors.mode ? true : undefined}
                                className="w-72 rounded-xl bg-card px-3.5 data-[size=default]:h-11"
                            >
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="rounded-xl">
                                {options.map((option) => (
                                    <SelectItem
                                        key={option.value}
                                        value={option.value}
                                    >
                                        {option.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <Button
                            type="submit"
                            disabled={processing || mode === waf.mode}
                        >
                            {processing ? <Spinner /> : <ShieldCheck />}
                            Simpan mode
                        </Button>
                        {errors.mode && (
                            <p className="w-full text-xs text-destructive">
                                {errors.mode}
                            </p>
                        )}
                    </>
                )}
            </Form>
        </Panel>
    );
}

function CustomSmtpPanel({ customSmtp }: { customSmtp: CustomSmtp }) {
    const s = customSmtp;
    const [enabled, setEnabled] = useState(s.enabled);
    const [encryption, setEncryption] = useState<CustomSmtp['encryption']>(
        s.encryption,
    );

    return (
        <Panel
            title="SMTP kustom (email panel)"
            description="Secara bawaan panel mengirim emailnya sendiri (notifikasi, reset kata sandi, tiket) lewat mail server yang dipasang installer. Aktifkan SMTP kustom untuk memakai server SMTP lain; kalau dimatikan atau dikosongkan, email bawaan server yang dipakai."
        >
            <Form
                {...updateCustomSmtp.form()}
                options={{ preserveScroll: true }}
                transform={(data) => ({ ...data, enabled })}
                className="space-y-5"
            >
                {({ processing, errors }) => (
                    <>
                        <label className="flex flex-wrap items-center gap-3">
                            <Checkbox
                                checked={enabled}
                                onCheckedChange={(value) =>
                                    setEnabled(value === true)
                                }
                            />
                            <span className="text-sm font-medium">
                                Aktifkan SMTP kustom
                            </span>
                            <StatusBadge
                                status={s.enabled ? 'active' : 'neutral'}
                                label={s.enabled ? 'Aktif' : 'Mati'}
                            />
                        </label>

                        <div className="grid gap-4 sm:grid-cols-2">
                            <Field
                                id="smtp-host"
                                label="Host SMTP"
                                error={errors.host}
                            >
                                <Input
                                    id="smtp-host"
                                    name="host"
                                    defaultValue={s.host}
                                    placeholder="smtp.contoh.com"
                                    autoComplete="off"
                                    className="rounded-xl"
                                />
                            </Field>
                            <Field
                                id="smtp-port"
                                label="Port"
                                error={errors.port}
                            >
                                <Input
                                    id="smtp-port"
                                    name="port"
                                    type="number"
                                    min={1}
                                    max={65535}
                                    defaultValue={s.port || 587}
                                    className="rounded-xl"
                                />
                            </Field>
                            <Field
                                id="smtp-encryption"
                                label="Keamanan"
                                error={errors.encryption}
                            >
                                <input
                                    type="hidden"
                                    name="encryption"
                                    value={encryption}
                                />
                                <Select
                                    value={encryption}
                                    onValueChange={(value) =>
                                        setEncryption(
                                            value as CustomSmtp['encryption'],
                                        )
                                    }
                                >
                                    <SelectTrigger
                                        id="smtp-encryption"
                                        className="w-full rounded-xl bg-card px-3.5 data-[size=default]:h-11"
                                    >
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent className="rounded-xl">
                                        <SelectItem value="tls">
                                            STARTTLS (umumnya port 587)
                                        </SelectItem>
                                        <SelectItem value="ssl">
                                            SSL/TLS (umumnya port 465)
                                        </SelectItem>
                                        <SelectItem value="none">
                                            Tanpa enkripsi
                                        </SelectItem>
                                    </SelectContent>
                                </Select>
                            </Field>
                            <Field
                                id="smtp-username"
                                label="Username"
                                error={errors.username}
                            >
                                <Input
                                    id="smtp-username"
                                    name="username"
                                    defaultValue={s.username}
                                    autoComplete="off"
                                    className="rounded-xl"
                                />
                            </Field>
                            <Field
                                id="smtp-password"
                                label="Password"
                                error={errors.password}
                            >
                                <Input
                                    id="smtp-password"
                                    name="password"
                                    type="password"
                                    autoComplete="new-password"
                                    placeholder={
                                        s.password_set
                                            ? 'Tersimpan — isi untuk mengganti'
                                            : ''
                                    }
                                    className="rounded-xl"
                                />
                            </Field>
                            <Field
                                id="smtp-from-address"
                                label="Email pengirim (from)"
                                error={errors.from_address}
                            >
                                <Input
                                    id="smtp-from-address"
                                    name="from_address"
                                    type="email"
                                    defaultValue={s.from_address}
                                    placeholder="panel@contoh.com"
                                    autoComplete="off"
                                    className="rounded-xl"
                                />
                            </Field>
                            <Field
                                id="smtp-from-name"
                                label="Nama pengirim"
                                error={errors.from_name}
                            >
                                <Input
                                    id="smtp-from-name"
                                    name="from_name"
                                    defaultValue={s.from_name}
                                    placeholder="Panel RAYXO"
                                    autoComplete="off"
                                    className="rounded-xl"
                                />
                            </Field>
                        </div>

                        <Button type="submit" disabled={processing}>
                            {processing ? <Spinner /> : <Mail />}
                            Simpan SMTP
                        </Button>
                    </>
                )}
            </Form>

            <div className="mt-5 space-y-3 border-t pt-5">
                <p className="text-sm font-medium">Kirim email tes</p>
                <p className="text-xs text-muted-foreground">
                    Simpan dulu pengaturannya, lalu kirim tes memakai
                    konfigurasi yang tersimpan.
                </p>
                <Form
                    {...testCustomSmtp.form()}
                    options={{ preserveScroll: true }}
                    className="flex flex-wrap items-end gap-3"
                >
                    {({ processing, errors }) => (
                        <>
                            <Field
                                id="smtp-test-email"
                                label="Kirim ke"
                                error={errors.email}
                            >
                                <Input
                                    id="smtp-test-email"
                                    name="email"
                                    type="email"
                                    placeholder="kamu@contoh.com"
                                    autoComplete="off"
                                    className="w-64 rounded-xl"
                                    required
                                />
                            </Field>
                            <Button
                                type="submit"
                                variant="outline"
                                disabled={processing}
                            >
                                {processing ? <Spinner /> : <Send />}
                                Kirim tes
                            </Button>
                        </>
                    )}
                </Form>
            </div>
        </Panel>
    );
}

function ClamavPanel({ clamav }: { clamav: Clamav }) {
    const s = clamav;
    const base = {
        enabled: s.enabled,
        email: s.email,
        on_access: s.on_access,
        scheduled: s.scheduled,
    };

    const label = s.enabled ? 'Aktif' : 'Mati';

    return (
        <Panel
            title="ClamAV antivirus"
            description="Pindai virus di lampiran email, terjadwal, on-access, dan sesuai permintaan. clamd memuat database signature besar ke memori, jadi mati sampai kamu nyalakan."
        >
            <Form
                {...updateClamav.form()}
                options={{ preserveScroll: true }}
                transform={() => ({ ...base, enabled: !s.enabled })}
                className="flex flex-wrap items-center gap-4"
            >
                {({ processing }) => (
                    <>
                        <StatusBadge
                            status={s.enabled ? 'active' : 'neutral'}
                            label={label}
                        />
                        <Button
                            type="submit"
                            variant={s.enabled ? 'outline' : 'default'}
                            disabled={processing}
                        >
                            {processing && <Spinner />}
                            {s.enabled ? 'Matikan ClamAV' : 'Nyalakan ClamAV'}
                        </Button>
                        <p className="w-full text-xs text-muted-foreground">
                            Mengaktifkan mengunduh database signature (~300 MB)
                            dan menjalankan clamd (perlu ~1–2 GB RAM); bisa
                            makan beberapa menit.
                        </p>
                    </>
                )}
            </Form>

            {s.enabled && (
                <div className="mt-5 space-y-3 border-t pt-5">
                    <ClamavSubToggle
                        label="Scan lampiran email"
                        description="Pindai lampiran masuk & keluar lewat Rspamd; email berisi virus ditolak."
                        field="email"
                        base={base}
                    />
                    <ClamavSubToggle
                        label="Scan terjadwal"
                        description="Pindai /home tiap hari; file terinfeksi dipindah ke karantina."
                        field="scheduled"
                        base={base}
                    />
                    <ClamavSubToggle
                        label="Scan on-access"
                        description="Pindai tiap file yang dibuka di /home (clamonacc). Berat untuk server sibuk."
                        field="on_access"
                        base={base}
                    />

                    <Form
                        {...clamavScan.form()}
                        options={{ preserveScroll: true }}
                        className="flex flex-wrap items-end gap-3 border-t pt-4"
                    >
                        {({ processing, errors }) => (
                            <>
                                <div className="space-y-1.5">
                                    <Label htmlFor="clamav_path">
                                        Scan sekarang (path di /home)
                                    </Label>
                                    <Input
                                        id="clamav_path"
                                        name="path"
                                        defaultValue="/home"
                                        className="w-72"
                                    />
                                </div>
                                <Button
                                    type="submit"
                                    variant="outline"
                                    disabled={processing}
                                >
                                    {processing && <Spinner />}
                                    Scan
                                </Button>
                                <InputError message={errors.path} />
                            </>
                        )}
                    </Form>
                </div>
            )}
        </Panel>
    );
}

function ClamavSubToggle({
    label,
    description,
    field,
    base,
}: {
    label: string;
    description: string;
    field: 'email' | 'on_access' | 'scheduled';
    base: ClamavSettings;
}) {
    const on = base[field];

    return (
        <Form
            {...updateClamav.form()}
            options={{ preserveScroll: true }}
            transform={() => ({ ...base, [field]: !on })}
            className="flex items-center justify-between gap-4"
        >
            {({ processing }) => (
                <>
                    <div className="min-w-0">
                        <div className="text-sm font-medium">{label}</div>
                        <div className="text-xs text-muted-foreground">
                            {description}
                        </div>
                    </div>
                    <Button
                        type="submit"
                        variant={on ? 'outline' : 'default'}
                        size="sm"
                        disabled={processing}
                    >
                        {processing && <Spinner />}
                        {on ? 'Matikan' : 'Nyalakan'}
                    </Button>
                </>
            )}
        </Form>
    );
}

function IonCubePanel({ ioncube }: { ioncube: IonCube }) {
    return (
        <Panel
            title="ionCube Loader"
            description="Dibutuhkan aplikasi PHP yang dienkripsi dengan ionCube, misalnya WHMCS. ionCube Loader adalah perangkat lunak gratis tapi tertutup milik ionCube Ltd; saat dinyalakan, server mengunduhnya dari ioncube.com dan memasangnya untuk semua versi PHP yang didukung ionCube."
        >
            <Form
                {...updateIonCube.form()}
                options={{ preserveScroll: true }}
                transform={() => ({ enabled: !ioncube.enabled })}
                className="flex flex-wrap items-center gap-4"
            >
                {({ processing, errors }) => (
                    <>
                        <StatusBadge
                            status={ioncube.enabled ? 'active' : 'neutral'}
                            label={
                                ioncube.enabled
                                    ? `Aktif${ioncube.loader ? ` (v${ioncube.loader})` : ''}`
                                    : 'Mati'
                            }
                        />
                        <Button
                            type="submit"
                            variant={ioncube.enabled ? 'outline' : 'default'}
                            disabled={processing}
                        >
                            {processing && <Spinner />}
                            {ioncube.enabled
                                ? 'Matikan ionCube'
                                : 'Nyalakan ionCube'}
                        </Button>
                        <p className="w-full text-xs text-muted-foreground">
                            {ioncube.enabled
                                ? `Dipakai PHP ${ioncube.versions.join(', ')}.${ioncube.missing.length > 0 ? ` Tidak tersedia untuk PHP ${ioncube.missing.join(', ')}.` : ''}`
                                : 'Proses ini mengunduh sekitar 20 MB dan memuat ulang semua pool PHP, jadi bisa memakan waktu sekitar satu menit.'}
                        </p>
                        <InputError message={errors.enabled} />
                    </>
                )}
            </Form>
        </Panel>
    );
}

function DatabaseRemotePanel({ enabled }: { enabled: boolean }) {
    return (
        <Panel
            title="Akses database dari luar"
            description="Buka MariaDB di port 3306 untuk host yang diizinkan masing-masing akun di halaman Databases. User database tetap hanya bisa masuk dari host yang diizinkan akunnya."
        >
            <Form
                {...updateDatabaseRemoteAccess.form()}
                options={{ preserveScroll: true }}
                transform={() => ({ enabled: !enabled })}
                className="flex flex-wrap items-center gap-4"
            >
                {({ processing, errors }) => (
                    <>
                        <StatusBadge
                            status={enabled ? 'active' : 'neutral'}
                            label={
                                enabled ? 'Terbuka' : 'Hanya dari server ini'
                            }
                        />
                        <Button
                            type="submit"
                            variant={enabled ? 'outline' : 'default'}
                            disabled={processing}
                        >
                            {processing && <Spinner />}
                            {enabled ? 'Tutup akses luar' : 'Buka akses luar'}
                        </Button>
                        <p className="w-full text-xs text-muted-foreground">
                            MariaDB di-restart, jadi website kehilangan
                            database-nya beberapa detik. Port 3306 juga perlu
                            dibuka di firewall penyedia VPS.
                        </p>
                        <InputError message={errors.enabled} />
                    </>
                )}
            </Form>
        </Panel>
    );
}

/**
 * Bandwidth is counted from the logs of websites, mail and FTP, per
 * calendar month. Accounts over their package's bandwidth can be
 * suspended until the next month.
 */
function BandwidthPanel({ suspendOverLimit }: { suspendOverLimit: boolean }) {
    const [enabled, setEnabled] = useState(suspendOverLimit);

    return (
        <Panel
            title="Batas bandwidth"
            description="Bandwidth dihitung dari log website, email, dan FTP setiap 15 menit, per bulan kalender. Batasnya diatur di paket."
        >
            <Form
                {...updateBandwidth.form()}
                options={{ preserveScroll: true }}
                className="grid gap-4"
            >
                {({ processing, errors }) => (
                    <>
                        <input
                            type="hidden"
                            name="suspend_over_limit"
                            value={enabled ? 1 : 0}
                        />
                        <div className="flex items-start gap-3">
                            <Checkbox
                                id="suspend_over_limit"
                                checked={enabled}
                                onCheckedChange={(checked) =>
                                    setEnabled(checked === true)
                                }
                                className="mt-0.5"
                            />
                            <div className="grid gap-1">
                                <Label htmlFor="suspend_over_limit">
                                    Suspend otomatis akun yang melewati batas
                                    bandwidth paketnya
                                </Label>
                                <p className="text-xs text-muted-foreground">
                                    Akun dibuka kembali otomatis saat bulan
                                    berganti. Tanpa ini, pemakaian di atas batas
                                    hanya ditampilkan.
                                </p>
                            </div>
                        </div>
                        <InputError message={errors.suspend_over_limit} />
                        <div>
                            <Button type="submit" disabled={processing}>
                                {processing && <Spinner />}
                                Simpan
                            </Button>
                        </div>
                    </>
                )}
            </Form>
        </Panel>
    );
}

function MailLimitPanel({ limits }: { limits: MailLimits }) {
    const [account, setAccount] = useState(limits.account?.toString() ?? '');
    const [server, setServer] = useState(limits.server?.toString() ?? '');

    return (
        <Panel
            title="Batas kirim email"
            description={`Dihitung per penerima dalam 24 jam, untuk email yang dikirim akun hosting (lewat login SMTP, webmail, maupun PHP mail() dan cron). Email yang melewati batas ditolak sementara dan dicoba lagi nanti, tidak dibuang. Kosongkan untuk tanpa batas. Bawaan per akun: ${limits.default_account}.`}
        >
            <Form
                {...updateMailLimits.form()}
                options={{ preserveScroll: true }}
                className="grid gap-4"
            >
                {({ processing, errors }) => (
                    <>
                        <div className="grid gap-4 sm:grid-cols-2">
                            <Field
                                id="account"
                                label="Per akun hosting"
                                error={errors.account}
                            >
                                <Input
                                    id="account"
                                    name="account"
                                    type="number"
                                    min={1}
                                    placeholder="Tanpa batas"
                                    value={account}
                                    onChange={(event) =>
                                        setAccount(event.target.value)
                                    }
                                    className="rounded-xl"
                                />
                            </Field>
                            <Field
                                id="server"
                                label="Seluruh server (semua akun digabung)"
                                error={errors.server}
                            >
                                <Input
                                    id="server"
                                    name="server"
                                    type="number"
                                    min={1}
                                    placeholder="Tanpa batas"
                                    value={server}
                                    onChange={(event) =>
                                        setServer(event.target.value)
                                    }
                                    className="rounded-xl"
                                />
                            </Field>
                        </div>
                        <div>
                            <Button
                                type="submit"
                                disabled={
                                    processing ||
                                    (account ===
                                        (limits.account?.toString() ?? '') &&
                                        server ===
                                            (limits.server?.toString() ?? ''))
                                }
                            >
                                {processing ? <Spinner /> : <MailCheck />}
                                Simpan batas kirim
                            </Button>
                        </div>
                    </>
                )}
            </Form>
        </Panel>
    );
}

const securityRoleLabels = {
    admin: 'Admin',
    reseller: 'Reseller',
    user: 'Akun hosting',
} as const;

/**
 * Require two-factor authentication for privileged roles, and set the shortest
 * password each role may use. The minimum can only make passwords stronger:
 * the server never lets it drop below the built-in default.
 */
function SecurityPolicyPanel({ policy }: { policy: SecurityPolicy }) {
    const { data, setData, put, processing, errors } = useForm({
        require_2fa: policy.require_2fa,
        password_min_length: { ...policy.password_min_length },
    });

    const fieldErrors = errors as Record<string, string>;

    const toggleRole = (role: 'admin' | 'reseller') =>
        setData(
            'require_2fa',
            data.require_2fa.includes(role)
                ? data.require_2fa.filter((value) => value !== role)
                : [...data.require_2fa, role],
        );

    const submit = (event: FormEvent) => {
        event.preventDefault();
        put(updateSecurityPolicy().url, { preserveScroll: true });
    };

    return (
        <Panel
            title="Kebijakan keamanan"
            description="Wajibkan verifikasi dua langkah untuk admin dan reseller, dan atur panjang minimum kata sandi per peran. Panjang minimum hanya bisa memperkuat; server tidak pernah menurunkannya di bawah bawaan."
        >
            <form onSubmit={submit} className="grid gap-6">
                <fieldset className="grid gap-3">
                    <legend className="mb-1 text-sm font-medium">
                        Wajib verifikasi dua langkah (2FA)
                    </legend>
                    {(['admin', 'reseller'] as const).map((role) => (
                        <label
                            key={role}
                            className="flex items-center gap-3 text-sm"
                        >
                            <Checkbox
                                checked={data.require_2fa.includes(role)}
                                onCheckedChange={() => toggleRole(role)}
                            />
                            {securityRoleLabels[role]}
                        </label>
                    ))}
                    <InputError message={errors.require_2fa} />
                </fieldset>

                <div className="grid gap-4 sm:grid-cols-3">
                    {(['admin', 'reseller', 'user'] as const).map((role) => (
                        <Field
                            key={role}
                            id={`min-${role}`}
                            label={`Panjang minimum — ${securityRoleLabels[role]}`}
                            error={fieldErrors[`password_min_length.${role}`]}
                        >
                            <Input
                                id={`min-${role}`}
                                type="number"
                                min={policy.default_min}
                                max={policy.max_min}
                                value={data.password_min_length[role]}
                                onChange={(event) =>
                                    setData('password_min_length', {
                                        ...data.password_min_length,
                                        [role]: Number(event.target.value),
                                    })
                                }
                                className="rounded-xl"
                            />
                        </Field>
                    ))}
                </div>

                <div>
                    <Button type="submit" disabled={processing}>
                        {processing ? <Spinner /> : <ShieldCheck />}
                        Simpan kebijakan keamanan
                    </Button>
                </div>
            </form>
        </Panel>
    );
}

/**
 * The nameservers every DNS zone of this server names. They must resolve
 * to this server, which the admin sets up at the registrar.
 */
function NameserverPanel({
    current,
    publicIp,
}: {
    current: string[];
    publicIp: string | null;
}) {
    const [ns1, setNs1] = useState(current[0] ?? '');
    const [ns2, setNs2] = useState(current[1] ?? '');
    const isUnchanged =
        ns1 === (current[0] ?? '') && ns2 === (current[1] ?? '');

    return (
        <Panel
            title="Nameserver"
            description="Dicantumkan di setiap zone DNS server ini. Arahkan nameserver domain customer ke nama-nama ini; menyimpan perubahan menulis ulang semua zone."
        >
            <Form
                {...updateNameservers.form()}
                options={{ preserveScroll: true }}
                className="grid gap-4"
            >
                {({ processing, errors }) => (
                    <>
                        <div className="grid gap-4 sm:grid-cols-2">
                            <Field
                                id="ns1"
                                label="Nameserver 1"
                                error={errors.ns1}
                            >
                                <Input
                                    id="ns1"
                                    name="ns1"
                                    value={ns1}
                                    onChange={(event) =>
                                        setNs1(event.target.value)
                                    }
                                    required
                                    autoComplete="off"
                                    spellCheck={false}
                                    className="font-mono"
                                    aria-invalid={errors.ns1 ? true : undefined}
                                />
                            </Field>
                            <Field
                                id="ns2"
                                label="Nameserver 2"
                                error={errors.ns2}
                            >
                                <Input
                                    id="ns2"
                                    name="ns2"
                                    value={ns2}
                                    onChange={(event) =>
                                        setNs2(event.target.value)
                                    }
                                    autoComplete="off"
                                    spellCheck={false}
                                    placeholder="Opsional"
                                    className="font-mono"
                                    aria-invalid={errors.ns2 ? true : undefined}
                                />
                            </Field>
                        </div>
                        <p className="text-sm text-muted-foreground">
                            Buat A record untuk setiap nameserver
                            {publicIp !== null && (
                                <>
                                    {' '}
                                    yang mengarah ke{' '}
                                    <span className="font-mono">
                                        {publicIp}
                                    </span>
                                </>
                            )}
                            . Kalau nameserver memakai domain yang zonenya juga
                            di server ini, daftarkan juga sebagai glue record di
                            registrar domain itu.
                        </p>
                        <div>
                            <Button
                                type="submit"
                                disabled={processing || isUnchanged}
                            >
                                {processing ? <Spinner /> : <Network />}
                                Simpan nameserver
                            </Button>
                        </div>
                    </>
                )}
            </Form>
        </Panel>
    );
}

function Detail({ label, children }: { label: string; children: ReactNode }) {
    return (
        <div className="grid gap-1">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="font-medium">{children}</dd>
        </div>
    );
}
