import { router } from '@inertiajs/react';
import {
    CloudDownload,
    FolderSearch,
    KeyRound,
    PlugZap,
    ShieldCheck,
    TriangleAlert,
} from 'lucide-react';
import { useEffect, useState } from 'react';
import { BackupChoice } from '@/components/backup-restore-dialog';
import InputError from '@/components/input-error';
import { Panel, tableStyles } from '@/components/panel';
import { Button } from '@/components/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { formatBytes, formatDateTime } from '@/lib/format';
import { cn } from '@/lib/utils';
import {
    hostKey as fetchHostKey,
    take,
    test,
    update,
} from '@/routes/admin/backups/remote';

export type RemoteStatus = 'uploading' | 'uploaded' | 'failed' | 'fetching';

/**
 * Where a backup's copy on the remote destination stands.
 */
export type RemoteCopy = {
    on_server: boolean;
    remote_status: RemoteStatus | null;
    remote_error: string | null;
};

type RemoteType = 'sftp' | 'ftp' | 's3';

export type RemoteSettings = {
    enabled: boolean;
    type: RemoteType;
    host: string;
    port: number | null;
    user: string;
    tls: boolean;
    host_key: string;
    endpoint: string;
    region: string;
    bucket: string;
    access_key: string;
    path: string;
    keep: number;
    keep_local: boolean;
    has_password: boolean;
    has_secret_key: boolean;
    host_key_fingerprint: string | null;
    server: string;
    keep_max: number;
};

type RemoteFile = {
    username?: string;
    name: string;
    size: number;
    modified: number;
    known: boolean;
};

export type RemoteListing = {
    server: string;
    accounts: RemoteFile[];
    system: RemoteFile[];
};

const types: { value: RemoteType; label: string }[] = [
    { value: 'sftp', label: 'SFTP' },
    { value: 'ftp', label: 'FTP / FTPS' },
    { value: 's3', label: 'S3 (AWS, R2, B2, Wasabi, MinIO, …)' },
];

/**
 * A line under a backup's status about its copy on the remote destination.
 */
export function RemoteCopyNote({ copy }: { copy: RemoteCopy }) {
    const line = 'mt-1 flex max-w-xs items-center gap-1 text-xs';

    return (
        <>
            {copy.remote_status === 'uploading' && (
                <p className={cn(line, 'text-muted-foreground')}>
                    <Spinner className="size-3" /> Menyalin ke remote…
                </p>
            )}
            {copy.remote_status === 'fetching' && (
                <p className={cn(line, 'text-muted-foreground')}>
                    <Spinner className="size-3" /> Mengambil dari remote…
                </p>
            )}
            {copy.remote_status === 'uploaded' && (
                <p className={cn(line, 'text-muted-foreground')}>
                    <ShieldCheck className="size-3" />
                    {copy.on_server ? 'Juga di remote' : 'Hanya di remote'}
                </p>
            )}
            {copy.remote_status === 'failed' && (
                <p className={cn(line, 'text-destructive')}>
                    Gagal disalin ke remote
                </p>
            )}
            {copy.remote_error && (
                <p className="mt-1 max-w-xs text-xs text-destructive">
                    {copy.remote_error}
                </p>
            )}
        </>
    );
}

/**
 * Bring a backup that is only on the remote destination back to the
 * server.
 */
export function FetchFromRemoteButton({ url }: { url: string }) {
    const [busy, setBusy] = useState(false);

    return (
        <Button
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={() => {
                setBusy(true);
                router.post(
                    url,
                    {},
                    { preserveScroll: true, onFinish: () => setBusy(false) },
                );
            }}
        >
            {busy ? <Spinner /> : <CloudDownload />} Ambil dari remote
        </Button>
    );
}

type Form = {
    enabled: boolean;
    type: RemoteType;
    host: string;
    port: string;
    user: string;
    password: string;
    tls: boolean;
    host_key: string;
    endpoint: string;
    region: string;
    bucket: string;
    access_key: string;
    secret_key: string;
    path: string;
    keep: number;
    keep_local: boolean;
};

function formOf(remote: RemoteSettings): Form {
    return {
        enabled: remote.enabled,
        type: remote.type,
        host: remote.host,
        port: remote.port ? String(remote.port) : '',
        user: remote.user,
        password: '',
        tls: remote.tls,
        host_key: remote.host_key,
        endpoint: remote.endpoint,
        region: remote.region,
        bucket: remote.bucket,
        access_key: remote.access_key,
        secret_key: '',
        path: remote.path,
        keep: remote.keep,
        keep_local: remote.keep_local,
    };
}

/**
 * The remote destination automatic backups and backups of the panel are
 * copied to, and browsing what is there.
 */
export function RemoteBackupPanel({
    remote,
    servers,
    listing,
}: {
    remote: RemoteSettings;
    servers?: string[];
    listing?: RemoteListing | null;
}) {
    const [form, setForm] = useState<Form>(() => formOf(remote));
    const [offered, setOffered] = useState<{
        key: string;
        fingerprint: string;
    } | null>(null);
    const [errors, setErrors] = useState<Record<string, string>>({});
    const [busy, setBusy] = useState<'save' | 'key' | 'test' | null>(null);
    const [browsing, setBrowsing] = useState(false);

    // The key the SFTP server offered comes once, as flash data.
    useEffect(() => {
        return router.on('flash', (event) => {
            const flash = (event as CustomEvent).detail?.flash;

            if (flash?.remote_host_key) {
                setOffered(
                    flash.remote_host_key as {
                        key: string;
                        fingerprint: string;
                    },
                );
            }
        });
    }, []);

    const set = (values: Partial<Form>) => setForm({ ...form, ...values });
    const location = (values: Form) =>
        JSON.stringify([
            values.type,
            values.host,
            values.port,
            values.user,
            values.endpoint,
            values.bucket,
            values.path,
        ]);
    const moved = remote.enabled && location(form) !== location(formOf(remote));
    const network = form.type !== 's3';
    const accepted =
        form.type === 'sftp' &&
        form.host_key !== '' &&
        form.host_key === remote.host_key &&
        form.host === remote.host
            ? remote.host_key_fingerprint
            : offered && form.host_key === offered.key
              ? offered.fingerprint
              : null;

    const send = (
        kind: 'save' | 'key' | 'test',
        visit: (options: object) => void,
    ) => {
        setBusy(kind);
        setErrors({});
        visit({
            preserveScroll: true,
            onError: (failed: Record<string, string>) => setErrors(failed),
            onFinish: () => setBusy(null),
        });
    };

    const save = () =>
        send('save', (options) =>
            router.put(
                update.url(),
                {
                    ...form,
                    port: form.port === '' ? null : Number(form.port),
                },
                {
                    ...options,
                    onSuccess: () => set({ password: '', secret_key: '' }),
                },
            ),
        );

    const askKey = () =>
        send('key', (options) =>
            router.post(
                fetchHostKey.url(),
                {
                    host: form.host,
                    port: form.port === '' ? null : Number(form.port),
                },
                options,
            ),
        );

    const tryIt = () =>
        send('test', (options) => router.post(test.url(), {}, options));

    const openBrowse = () => {
        setBrowsing(true);
        router.reload({
            only: ['remote_servers', 'remote_listing'],
            data: { browse: remote.server },
        });
    };

    return (
        <>
            <Panel
                title="Tujuan backup remote"
                description={`Salin backup otomatis dan backup sistem ke server lain (SFTP atau FTP/FTPS) atau ke bucket S3, supaya backup tetap ada kalau server ini rusak. Setiap backup disalin setelah selesai dibuat, oleh worker backup. Di tujuan, backup server ini ada di folder ${remote.server}.`}
            >
                <div className="grid gap-6">
                    <BackupChoice
                        id="remote-enabled"
                        checked={form.enabled}
                        onChange={(enabled) => set({ enabled })}
                        label="Salin backup ke tujuan remote"
                        hint="Bawaan mati. Mematikannya tidak menghapus salinan yang sudah ada di remote."
                    />

                    <div
                        className={cn(
                            'grid gap-6',
                            !form.enabled && 'pointer-events-none opacity-60',
                        )}
                        aria-disabled={!form.enabled}
                    >
                        <div className="grid gap-2 sm:max-w-xs">
                            <Label htmlFor="remote-type">Jenis tujuan</Label>
                            <Select
                                value={form.type}
                                onValueChange={(type) =>
                                    set({ type: type as RemoteType })
                                }
                            >
                                <SelectTrigger id="remote-type">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    {types.map((option) => (
                                        <SelectItem
                                            key={option.value}
                                            value={option.value}
                                        >
                                            {option.label}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        {network ? (
                            <div className="grid gap-4 sm:grid-cols-2">
                                <Field
                                    id="remote-host"
                                    label="Host"
                                    value={form.host}
                                    onChange={(host) => set({ host })}
                                    placeholder="backup.example.com"
                                    error={errors.host}
                                />
                                <Field
                                    id="remote-port"
                                    label="Port"
                                    value={form.port}
                                    onChange={(port) =>
                                        set({ port: port.replace(/\D/g, '') })
                                    }
                                    placeholder={
                                        form.type === 'sftp' ? '22' : '21'
                                    }
                                    error={errors.port}
                                />
                                <Field
                                    id="remote-user"
                                    label="User"
                                    value={form.user}
                                    onChange={(user) => set({ user })}
                                    error={errors.user}
                                />
                                <Field
                                    id="remote-password"
                                    label="Password"
                                    type="password"
                                    value={form.password}
                                    onChange={(password) => set({ password })}
                                    placeholder={
                                        remote.has_password
                                            ? 'Tersimpan; kosongkan untuk tetap memakainya'
                                            : ''
                                    }
                                    error={errors.password}
                                />
                            </div>
                        ) : (
                            <div className="grid gap-4 sm:grid-cols-2">
                                <Field
                                    id="remote-endpoint"
                                    label="Endpoint"
                                    value={form.endpoint}
                                    onChange={(endpoint) => set({ endpoint })}
                                    placeholder="https://<akun>.r2.cloudflarestorage.com"
                                    hint="Kosongkan untuk Amazon S3."
                                    error={errors.endpoint}
                                />
                                <Field
                                    id="remote-region"
                                    label="Region"
                                    value={form.region}
                                    onChange={(region) => set({ region })}
                                    placeholder="auto, us-east-1, …"
                                    error={errors.region}
                                />
                                <Field
                                    id="remote-bucket"
                                    label="Bucket"
                                    value={form.bucket}
                                    onChange={(bucket) => set({ bucket })}
                                    placeholder="my-backups"
                                    hint="Bucket harus sudah ada dan privat."
                                    error={errors.bucket}
                                />
                                <Field
                                    id="remote-access-key"
                                    label="Access key"
                                    value={form.access_key}
                                    onChange={(access_key) =>
                                        set({ access_key })
                                    }
                                    error={errors.access_key}
                                />
                                <Field
                                    id="remote-secret-key"
                                    label="Secret key"
                                    type="password"
                                    value={form.secret_key}
                                    onChange={(secret_key) =>
                                        set({ secret_key })
                                    }
                                    placeholder={
                                        remote.has_secret_key
                                            ? 'Tersimpan; kosongkan untuk tetap memakainya'
                                            : ''
                                    }
                                    error={errors.secret_key}
                                />
                            </div>
                        )}

                        <Field
                            id="remote-path"
                            label={network ? 'Folder' : 'Folder di bucket'}
                            value={form.path}
                            onChange={(path) => set({ path })}
                            placeholder="rayxo-backups"
                            hint={
                                network
                                    ? 'Dari folder login, kecuali diawali /. Kosongkan untuk folder login itu sendiri.'
                                    : 'Kosongkan untuk menyimpan langsung di bucket.'
                            }
                            error={errors.path}
                        />

                        {form.type === 'ftp' && (
                            <BackupChoice
                                id="remote-tls"
                                checked={form.tls}
                                onChange={(tls) => set({ tls })}
                                label="Pakai TLS (FTPS)"
                                hint="Matikan hanya kalau server FTP tidak mendukung TLS: tanpa TLS, password dan backup dikirim tanpa enkripsi."
                            />
                        )}

                        {form.type === 'sftp' && (
                            <div className="grid gap-3 rounded-xl border px-4 py-3 text-sm">
                                <p className="flex items-center gap-2 font-semibold">
                                    <KeyRound className="size-4" />
                                    Host key server SFTP
                                </p>
                                <p className="text-muted-foreground">
                                    RAYXO hanya mengirim backup ke server yang
                                    menunjukkan host key yang kamu terima,
                                    supaya tidak ada server lain yang menyamar.
                                    Ambil host key-nya, lalu cocokkan
                                    fingerprint-nya dengan server tujuan
                                    (misalnya dengan <code>
                                        ssh-keygen -lf
                                    </code>{' '}
                                    di sana) sebelum menerimanya.
                                </p>
                                {accepted && (
                                    <p className="flex items-center gap-2 text-success">
                                        <ShieldCheck className="size-4" />
                                        Diterima: <code>{accepted}</code>
                                    </p>
                                )}
                                {offered && form.host_key !== offered.key && (
                                    <div className="grid gap-2 rounded-lg bg-muted/50 px-3 py-2">
                                        <p>
                                            Server menunjukkan:{' '}
                                            <code>{offered.fingerprint}</code>
                                        </p>
                                        <div>
                                            <Button
                                                size="sm"
                                                onClick={() =>
                                                    set({
                                                        host_key: offered.key,
                                                    })
                                                }
                                            >
                                                <ShieldCheck /> Fingerprint
                                                cocok, terima
                                            </Button>
                                        </div>
                                    </div>
                                )}
                                <div>
                                    <Button
                                        variant="outline"
                                        size="sm"
                                        onClick={askKey}
                                        disabled={busy !== null || !form.host}
                                    >
                                        {busy === 'key' ? (
                                            <Spinner />
                                        ) : (
                                            <KeyRound />
                                        )}
                                        Ambil host key
                                    </Button>
                                </div>
                                <InputError message={errors.host_key} />
                            </div>
                        )}

                        <div className="grid gap-4 sm:grid-cols-2">
                            <Field
                                id="remote-keep"
                                label="Backup per akun yang disimpan di remote"
                                value={String(form.keep)}
                                onChange={(keep) =>
                                    set({
                                        keep: Math.min(
                                            remote.keep_max,
                                            Number(keep.replace(/\D/g, '')) ||
                                                1,
                                        ),
                                    })
                                }
                                hint={`1–${remote.keep_max}. Juga untuk backup sistem. Yang lebih lama dihapus dari remote setelah salinan baru masuk.`}
                                error={errors.keep}
                            />
                        </div>

                        <BackupChoice
                            id="remote-keep-local"
                            checked={form.keep_local}
                            onChange={(keep_local) => set({ keep_local })}
                            label="Simpan juga di server ini"
                            hint="Kalau dimatikan, backup dihapus dari server setelah tersalin ke remote, jadi disk server lebih lega. Untuk memulihkan atau mengunduhnya, backup diambil dulu dari remote."
                        />

                        {!form.keep_local && (
                            <p className="flex items-start gap-2 rounded-xl border border-warning/30 bg-warning/5 px-4 py-3 text-sm">
                                <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                                Backup yang gagal disalin tetap disimpan di
                                server, jadi tidak ada backup yang hilang.
                            </p>
                        )}
                    </div>

                    {moved && (
                        <p className="flex items-start gap-2 rounded-xl border border-warning/30 bg-warning/5 px-4 py-3 text-sm">
                            <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                            Tujuan pindah ke tempat lain: setelah disimpan,
                            panel melupakan salinan di tujuan lama (file di sana
                            tidak dihapus), dan backup yang hanya ada di sana
                            tidak lagi tercantum.
                        </p>
                    )}

                    <InputError message={errors.remote} />

                    <div className="flex flex-wrap items-center gap-3">
                        <Button onClick={save} disabled={busy !== null}>
                            {busy === 'save' && <Spinner />}
                            Simpan
                        </Button>
                        {remote.enabled && (
                            <>
                                <Button
                                    variant="outline"
                                    onClick={tryIt}
                                    disabled={busy !== null}
                                >
                                    {busy === 'test' ? (
                                        <Spinner />
                                    ) : (
                                        <PlugZap />
                                    )}
                                    Uji koneksi
                                </Button>
                                <Button variant="outline" onClick={openBrowse}>
                                    <FolderSearch /> Telusuri isi remote
                                </Button>
                            </>
                        )}
                        <p className="text-xs text-muted-foreground">
                            Simpan juga mengirim file uji ke tujuan.
                        </p>
                    </div>
                </div>
            </Panel>

            <RemoteBrowseDialog
                open={browsing}
                onOpenChange={setBrowsing}
                current={remote.server}
                servers={servers}
                listing={listing}
            />
        </>
    );
}

function Field({
    id,
    label,
    value,
    onChange,
    type = 'text',
    placeholder,
    hint,
    error,
}: {
    id: string;
    label: string;
    value: string;
    onChange: (value: string) => void;
    type?: string;
    placeholder?: string;
    hint?: string;
    error?: string;
}) {
    return (
        <div className="grid gap-2">
            <Label htmlFor={id}>{label}</Label>
            <Input
                id={id}
                type={type}
                value={value}
                onChange={(event) => onChange(event.target.value)}
                placeholder={placeholder}
                autoComplete={type === 'password' ? 'new-password' : 'off'}
            />
            {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
            <InputError message={error} />
        </div>
    );
}

/**
 * What the destination holds: this server's folder, or another one's, such
 * as the old server's after the panel moved to a new one.
 */
function RemoteBrowseDialog({
    open,
    onOpenChange,
    current,
    servers,
    listing,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    current: string;
    servers?: string[];
    listing?: RemoteListing | null;
}) {
    const [taking, setTaking] = useState<string | null>(null);
    const shown = listing?.server ?? current;

    const choose = (server: string) =>
        router.reload({ only: ['remote_listing'], data: { browse: server } });

    const takeFile = (kind: 'account' | 'system', file: RemoteFile) => {
        setTaking(file.name);
        router.post(
            take.url(),
            {
                server: shown,
                kind,
                username: file.username ?? null,
                name: file.name,
            },
            { preserveScroll: true, onFinish: () => setTaking(null) },
        );
    };

    const rows = (kind: 'account' | 'system', files: RemoteFile[]) =>
        files.map((file) => (
            <tr key={file.name} className={tableStyles.row}>
                <td className={tableStyles.td}>
                    {file.username && (
                        <div className="font-medium">{file.username}</div>
                    )}
                    <div className="text-xs break-all text-muted-foreground">
                        {file.name}
                    </div>
                </td>
                <td className={`${tableStyles.td} whitespace-nowrap`}>
                    {file.modified
                        ? formatDateTime(
                              new Date(file.modified * 1000).toISOString(),
                          )
                        : '–'}
                </td>
                <td className={`${tableStyles.td} whitespace-nowrap`}>
                    {formatBytes(file.size)}
                </td>
                <td className={`${tableStyles.td} text-right`}>
                    {file.known ? (
                        <span className="text-xs text-muted-foreground">
                            Sudah ada di panel
                        </span>
                    ) : (
                        <Button
                            variant="ghost"
                            size="sm"
                            disabled={taking !== null}
                            onClick={() => takeFile(kind, file)}
                        >
                            {taking === file.name ? (
                                <Spinner />
                            ) : (
                                <CloudDownload />
                            )}{' '}
                            Ambil
                        </Button>
                    )}
                </td>
            </tr>
        ));

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-h-[85vh] overflow-y-auto rounded-2xl sm:max-w-3xl">
                <DialogHeader>
                    <DialogTitle>Isi tujuan remote</DialogTitle>
                    <DialogDescription>
                        Ambil backup yang belum dikenal panel ini, misalnya
                        setelah pindah ke server baru. Backup akun yang diambil
                        muncul di daftar Backup tersimpan dan bisa dipulihkan.
                        Backup sistem diambil ke /var/lib/rayxo/backups/system,
                        untuk <code>setup.sh --restore-system</code>.
                    </DialogDescription>
                </DialogHeader>

                <div className="grid gap-2 sm:max-w-xs">
                    <Label htmlFor="remote-server">Folder server</Label>
                    {servers === undefined ? (
                        <Skeleton className="h-10 w-full" />
                    ) : (
                        <Select value={shown} onValueChange={choose}>
                            <SelectTrigger id="remote-server">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="rounded-xl">
                                {Array.from(new Set([current, ...servers])).map(
                                    (server) => (
                                        <SelectItem key={server} value={server}>
                                            {server}
                                            {server === current
                                                ? ' (server ini)'
                                                : ''}
                                        </SelectItem>
                                    ),
                                )}
                            </SelectContent>
                        </Select>
                    )}
                </div>

                {!listing ? (
                    <div className="grid gap-2">
                        <Skeleton className="h-8 w-full" />
                        <Skeleton className="h-8 w-full" />
                        <Skeleton className="h-8 w-2/3" />
                    </div>
                ) : listing.accounts.length === 0 &&
                  listing.system.length === 0 ? (
                    <p className="py-6 text-center text-sm text-muted-foreground">
                        Belum ada backup di folder {listing.server}.
                    </p>
                ) : (
                    <div className="overflow-x-auto">
                        <table className={tableStyles.table}>
                            <thead className={tableStyles.head}>
                                <tr>
                                    <th className={tableStyles.th}>Backup</th>
                                    <th className={tableStyles.th}>Waktu</th>
                                    <th className={tableStyles.th}>Ukuran</th>
                                    <th className={tableStyles.th}>
                                        <span className="sr-only">Aksi</span>
                                    </th>
                                </tr>
                            </thead>
                            <tbody className="divide-y">
                                {rows('system', listing.system)}
                                {rows('account', listing.accounts)}
                            </tbody>
                        </table>
                    </div>
                )}
            </DialogContent>
        </Dialog>
    );
}
