import { Head, Link, router, usePoll } from '@inertiajs/react';
import { ArrowLeftRight, FileArchive, RefreshCw, Upload } from 'lucide-react';
import { useState } from 'react';
import { GeneratedPasswordInput } from '@/components/generated-password-input';
import InputError from '@/components/input-error';
import { PageHeader } from '@/components/page-header';
import { Panel, tableStyles } from '@/components/panel';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { Spinner } from '@/components/ui/spinner';
import { formatBytes, formatDateTime } from '@/lib/format';
import { index as transfersIndex } from '@/routes/admin/transfers';
import { store } from '@/routes/admin/transfers/imports';

type AccountImport = {
    id: number;
    file: string;
    source: string | null;
    username: string | null;
    status: string;
    error: string | null;
    notes: string[];
    created_at: string | null;
};

type BackupFile = {
    name: string;
    size: number;
    modified: number;
};

type Props = {
    imports: AccountImport[];
    going: boolean;
    files: { items: BackupFile[]; error: string | null };
    packages: { id: number; name: string }[];
    folder: string;
};

const statusLabels: Record<string, string> = {
    queued: 'Antre',
    reading: 'Membaca backup',
    converting: 'Diubah ke format RAYXO',
    restoring: 'Isinya dipulihkan',
    completed: 'Selesai',
    failed: 'Gagal',
};

export default function AccountImports({
    imports,
    going,
    files,
    packages,
    folder,
}: Props) {
    const [form, setForm] = useState({
        file: '',
        username: '',
        email: '',
        password: '',
        package: 'none',
    });
    const [processing, setProcessing] = useState(false);
    const [refreshing, setRefreshing] = useState(false);
    const [errors, setErrors] = useState<Record<string, string>>({});

    usePoll(5000, { only: ['imports', 'going'] }, { autoStart: going });

    const refresh = () =>
        router.reload({
            only: ['files'],
            onStart: () => setRefreshing(true),
            onFinish: () => setRefreshing(false),
        });

    const submit = () => {
        setProcessing(true);
        setErrors({});
        router.post(
            store.url(),
            {
                file: form.file,
                username: form.username || null,
                email: form.email || null,
                password: form.password,
                package_id:
                    form.package === 'none' ? null : Number(form.package),
            },
            {
                preserveScroll: true,
                onSuccess: () =>
                    setForm({ ...form, file: '', username: '', password: '' }),
                onError: (failed) => setErrors(failed),
                onFinish: () => setProcessing(false),
            },
        );
    };

    return (
        <>
            <Head title="Impor akun" />

            <PageHeader
                title="Impor akun"
                description="Buat akun hosting di server ini dari backup akun cPanel atau DirectAdmin."
                actions={
                    <Button variant="outline" asChild>
                        <Link href={transfersIndex()}>
                            <ArrowLeftRight />
                            Dari server RAYXO
                        </Link>
                    </Button>
                }
            />

            <Panel
                title="1. Siapkan backupnya"
                description="Backup lengkap akun berisi file situs, database, email, dan pengaturannya. Server asal tidak diubah."
            >
                <ol className="grid list-decimal gap-3 pl-5 text-sm">
                    <li>
                        <strong>cPanel:</strong> sebagai root (WHM),{' '}
                        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
                            /scripts/pkgacct username
                        </code>{' '}
                        menghasilkan{' '}
                        <code className="font-mono text-xs">
                            /home/cpmove-username.tar.gz
                        </code>
                        . Tanpa akses root, pemilik akun bisa membuatnya di
                        cPanel: Backup, lalu Download a Full Account Backup.
                        <br />
                        <strong>DirectAdmin:</strong> di Admin Backup/Transfer,
                        buat backup akun yang dipilih ke folder lokal; hasilnya{' '}
                        <code className="font-mono text-xs">
                            /home/admin/admin_backups/user.admin.username.tar.zst
                        </code>
                        . Pemilik akun juga bisa membuatnya sendiri di
                        DirectAdmin: Create/Restore Backups.
                    </li>
                    <li>
                        Salin file itu ke server ini, ke folder{' '}
                        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
                            {folder}
                        </code>
                        , misalnya dengan{' '}
                        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs break-all">
                            scp file-backup root@server-ini:{folder}/
                        </code>
                        . Selama impor, disk server ini perlu ruang sekitar tiga
                        kali ukuran file itu.
                    </li>
                    <li>Pilih filenya di bawah, lalu mulai impor.</li>
                </ol>
            </Panel>

            <Panel
                title="2. Impor akun"
                description="Ikut pindah: domain dan domain tambahan (addon), subdomain, parked domain atau pointer, file situs, database beserta pengguna dan password-nya, akun email beserta password dan isinya, forwarder, dan cron job. Tidak ikut: password login panel asal (diganti password di bawah), SSL (dibuat lagi), record DNS buatan sendiri, akun FTP, dan SSH key."
            >
                <div className="grid gap-4 sm:max-w-xl">
                    <div className="grid gap-2">
                        <div className="flex items-center justify-between gap-2">
                            <Label htmlFor="import-file">File backup</Label>
                            <Button
                                variant="ghost"
                                size="sm"
                                onClick={refresh}
                                disabled={refreshing}
                            >
                                {refreshing ? <Spinner /> : <RefreshCw />}
                                Muat ulang
                            </Button>
                        </div>
                        {files.items.length === 0 ? (
                            <p className="rounded-xl border border-dashed px-4 py-3 text-sm text-muted-foreground">
                                {files.error ??
                                    `Belum ada file baru di ${folder}.`}
                            </p>
                        ) : (
                            <Select
                                value={form.file}
                                onValueChange={(file) =>
                                    setForm({ ...form, file })
                                }
                            >
                                <SelectTrigger id="import-file">
                                    <SelectValue placeholder="Pilih file" />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    {files.items.map((file) => (
                                        <SelectItem
                                            key={file.name}
                                            value={file.name}
                                        >
                                            {file.name} (
                                            {formatBytes(file.size)})
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        )}
                        <InputError message={errors.file} />
                    </div>

                    <div className="grid gap-2">
                        <Label htmlFor="import-username">Username</Label>
                        <Input
                            id="import-username"
                            value={form.username}
                            onChange={(event) =>
                                setForm({
                                    ...form,
                                    username: event.target.value,
                                })
                            }
                            placeholder="Kosongkan untuk memakai username di panel asal"
                            autoComplete="off"
                        />
                        <p className="text-xs text-muted-foreground">
                            Nama database dan penggunanya mengikuti username
                            ini. Kalau berbeda dari panel asal, pengaturan situs
                            yang menyebut nama database lama perlu disesuaikan.
                        </p>
                        <InputError message={errors.username} />
                    </div>

                    <div className="grid gap-2">
                        <Label htmlFor="import-email">Email</Label>
                        <Input
                            id="import-email"
                            type="email"
                            value={form.email}
                            onChange={(event) =>
                                setForm({ ...form, email: event.target.value })
                            }
                            placeholder="Kosongkan untuk memakai email kontak di panel asal"
                        />
                        <InputError message={errors.email} />
                    </div>

                    <div className="grid gap-2">
                        <Label htmlFor="import-password">Password panel</Label>
                        <GeneratedPasswordInput
                            id="import-password"
                            value={form.password}
                            onChange={(password) =>
                                setForm({ ...form, password })
                            }
                            required
                            placeholder="Password baru untuk login ke panel"
                            invalid={!!errors.password}
                        />
                        <InputError message={errors.password} />
                    </div>

                    <div className="grid gap-2">
                        <Label htmlFor="import-package">Paket</Label>
                        <Select
                            value={form.package}
                            onValueChange={(value) =>
                                setForm({ ...form, package: value })
                            }
                        >
                            <SelectTrigger id="import-package">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="rounded-xl">
                                <SelectItem value="none">
                                    Tanpa paket
                                </SelectItem>
                                {packages.map((item) => (
                                    <SelectItem
                                        key={item.id}
                                        value={String(item.id)}
                                    >
                                        {item.name}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <p className="text-xs text-muted-foreground">
                            Pastikan paketnya cukup untuk jumlah domain,
                            database, dan akun email di panel asal.
                        </p>
                        <InputError message={errors.package_id} />
                    </div>

                    <div>
                        <Button
                            onClick={submit}
                            disabled={
                                processing ||
                                form.file === '' ||
                                form.password === ''
                            }
                        >
                            {processing ? <Spinner /> : <Upload />}
                            Impor
                        </Button>
                    </div>
                </div>
            </Panel>

            <Panel
                flush
                title="Riwayat impor"
                description="Setelah akun dibuat di sini, domain, database, email, dan file-nya kembali di latar belakang; perkembangannya ada di menu Backup bagian Akun yang dibuat lagi dari backup. Setelah selesai, arahkan DNS domain-domainnya ke server ini."
            >
                {imports.length === 0 ? (
                    <div className="flex flex-col items-center gap-2 px-6 pt-4 pb-10 text-center text-sm text-muted-foreground">
                        <FileArchive className="size-8" />
                        Belum ada akun yang diimpor.
                    </div>
                ) : (
                    <div className="overflow-x-auto">
                        <table className={tableStyles.table}>
                            <thead className={tableStyles.head}>
                                <tr>
                                    <th className={tableStyles.th}>Akun</th>
                                    <th className={tableStyles.th}>File</th>
                                    <th className={tableStyles.th}>Mulai</th>
                                    <th className={tableStyles.th}>Status</th>
                                </tr>
                            </thead>
                            <tbody className="divide-y">
                                {imports.map((item) => (
                                    <tr
                                        key={item.id}
                                        className={tableStyles.row}
                                    >
                                        <td
                                            className={`${tableStyles.td} font-medium`}
                                        >
                                            {item.username ?? '–'}
                                        </td>
                                        <td className={tableStyles.td}>
                                            <div className="font-mono text-xs">
                                                {item.file}
                                            </div>
                                            {item.source && (
                                                <div className="text-xs text-muted-foreground">
                                                    {item.source}
                                                </div>
                                            )}
                                        </td>
                                        <td
                                            className={`${tableStyles.td} whitespace-nowrap`}
                                        >
                                            {item.created_at
                                                ? formatDateTime(
                                                      item.created_at,
                                                  )
                                                : '–'}
                                        </td>
                                        <td className={tableStyles.td}>
                                            <StatusBadge
                                                status={item.status}
                                                label={
                                                    statusLabels[item.status]
                                                }
                                            />
                                            {item.error && (
                                                <p className="mt-1 max-w-md text-xs text-destructive">
                                                    {item.error}
                                                </p>
                                            )}
                                            {item.notes.length > 0 && (
                                                <details className="mt-1 max-w-md text-xs text-muted-foreground">
                                                    <summary className="cursor-pointer">
                                                        Catatan (
                                                        {item.notes.length})
                                                    </summary>
                                                    <ul className="mt-1 grid list-disc gap-1 pl-4">
                                                        {item.notes.map(
                                                            (note) => (
                                                                <li key={note}>
                                                                    {note}
                                                                </li>
                                                            ),
                                                        )}
                                                    </ul>
                                                </details>
                                            )}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </Panel>
        </>
    );
}
