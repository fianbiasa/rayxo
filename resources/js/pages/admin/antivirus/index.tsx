import { Form, Head } from '@inertiajs/react';
import { RotateCcw, Trash2 } from 'lucide-react';
import { useState } from 'react';
import InputError from '@/components/input-error';
import { PageHeader } from '@/components/page-header';
import { Panel } from '@/components/panel';
import { StatusBadge } from '@/components/status-badge';
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
import { Label } from '@/components/ui/label';
import { Spinner } from '@/components/ui/spinner';
import { formatDateTime } from '@/lib/format';
import {
    deleteMethod as deleteQuarantine,
    restore as restoreQuarantine,
    scan as clamavScan,
    update as updateClamav,
} from '@/actions/App/Http/Controllers/Admin/AntivirusController';

type Status = {
    enabled: boolean;
    running: boolean;
    database: boolean;
    email: boolean;
    on_access: boolean;
    scheduled: boolean;
};

type Settings = Pick<Status, 'enabled' | 'email' | 'on_access' | 'scheduled'>;

type QuarantineEntry = {
    id: string;
    original_path: string;
    owner: string;
    virus: string;
    quarantined_at: string;
    source: string;
};

const sourceLabels: Record<string, string> = {
    'on-demand': 'Manual',
    scheduled: 'Terjadwal',
    'on-access': 'On-access',
};

export default function Antivirus({
    status,
    quarantine,
}: {
    status: Status;
    quarantine: QuarantineEntry[];
}) {
    return (
        <>
            <Head title="Antivirus" />

            <PageHeader
                title="Antivirus"
                description="ClamAV: status dan mode pindai, pindai sesuai permintaan, dan karantina file terinfeksi yang ditemukan pindaian mana pun."
            />

            <StatusPanel status={status} />
            {status.enabled && <ScanPanel />}
            <QuarantinePanel entries={quarantine} />
        </>
    );
}

function StatusPanel({ status }: { status: Status }) {
    const base: Settings = {
        enabled: status.enabled,
        email: status.email,
        on_access: status.on_access,
        scheduled: status.scheduled,
    };

    return (
        <Panel
            title="ClamAV"
            description="clamd memuat database signature besar ke memori, jadi mati sampai kamu nyalakan."
        >
            <div className="flex flex-wrap items-center gap-2">
                <StatusBadge
                    status={status.enabled ? 'active' : 'neutral'}
                    label={status.enabled ? 'Aktif' : 'Mati'}
                />
                {status.enabled && !status.running && (
                    <StatusBadge status="failed" label="clamd tidak berjalan" />
                )}
                {status.enabled && !status.database && (
                    <StatusBadge
                        status="suspended"
                        label="Database signature belum siap"
                    />
                )}
            </div>

            <Form
                {...updateClamav.form()}
                options={{ preserveScroll: true }}
                transform={() => ({ ...base, enabled: !status.enabled })}
                className="mt-4"
            >
                {({ processing }) => (
                    <>
                        <Button
                            type="submit"
                            variant={status.enabled ? 'outline' : 'default'}
                            disabled={processing}
                        >
                            {processing && <Spinner />}
                            {status.enabled
                                ? 'Matikan ClamAV'
                                : 'Nyalakan ClamAV'}
                        </Button>
                        <p className="mt-2 text-xs text-muted-foreground">
                            Mengaktifkan mengunduh database signature (~300 MB)
                            dan menjalankan clamd (perlu ~1–2 GB RAM); bisa
                            makan beberapa menit.
                        </p>
                    </>
                )}
            </Form>

            {status.enabled && (
                <div className="mt-5 space-y-3 border-t pt-5">
                    <SubToggle
                        label="Scan lampiran email"
                        description="Pindai lampiran masuk & keluar lewat Rspamd; email berisi virus ditolak."
                        field="email"
                        base={base}
                    />
                    <SubToggle
                        label="Scan terjadwal"
                        description="Pindai /home tiap hari; file terinfeksi dipindah ke karantina."
                        field="scheduled"
                        base={base}
                    />
                    <SubToggle
                        label="Scan on-access"
                        description="Pindai tiap file yang dibuka di /home (clamonacc). Berat untuk server sibuk."
                        field="on_access"
                        base={base}
                    />
                </div>
            )}
        </Panel>
    );
}

function SubToggle({
    label,
    description,
    field,
    base,
}: {
    label: string;
    description: string;
    field: 'email' | 'on_access' | 'scheduled';
    base: Settings;
}) {
    const on = base[field];

    return (
        <Form
            {...updateClamav.form()}
            options={{ preserveScroll: true }}
            transform={() => ({ ...base, [field]: !on })}
        >
            {({ processing }) => (
                <div className="flex items-start justify-between gap-4">
                    <div>
                        <p className="text-sm font-medium">{label}</p>
                        <p className="text-xs text-muted-foreground">
                            {description}
                        </p>
                    </div>
                    <Button
                        type="submit"
                        variant="outline"
                        size="sm"
                        disabled={processing}
                    >
                        {processing && <Spinner />}
                        {on ? 'Matikan' : 'Nyalakan'}
                    </Button>
                </div>
            )}
        </Form>
    );
}

function ScanPanel() {
    return (
        <Panel
            title="Scan sekarang"
            description="Pindai sebuah path di /home. File terinfeksi langsung dipindah ke karantina."
        >
            <Form
                {...clamavScan.form()}
                options={{ preserveScroll: true }}
                className="flex flex-wrap items-end gap-3"
            >
                {({ processing, errors }) => (
                    <>
                        <div className="space-y-1.5">
                            <Label htmlFor="scan_path">Path (di /home)</Label>
                            <Input
                                id="scan_path"
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
        </Panel>
    );
}

function QuarantinePanel({ entries }: { entries: QuarantineEntry[] }) {
    const [action, setAction] = useState<{
        type: 'restore' | 'delete';
        entry: QuarantineEntry;
    } | null>(null);

    return (
        <Panel
            title="Karantina"
            description="File terinfeksi yang dipindahkan ke karantina oleh pindaian mana pun. Pulihkan ke tempat asal, atau hapus permanen."
        >
            {entries.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                    Tidak ada file di karantina.
                </p>
            ) : (
                <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                        <thead>
                            <tr className="border-b text-left text-xs text-muted-foreground">
                                <th className="py-2 pr-4 font-medium">File</th>
                                <th className="py-2 pr-4 font-medium">Akun</th>
                                <th className="py-2 pr-4 font-medium">Virus</th>
                                <th className="py-2 pr-4 font-medium">Waktu</th>
                                <th className="py-2 pr-4 font-medium">
                                    Sumber
                                </th>
                                <th className="py-2" />
                            </tr>
                        </thead>
                        <tbody>
                            {entries.map((entry) => (
                                <tr
                                    key={entry.id}
                                    className="border-b last:border-0"
                                >
                                    <td className="py-2 pr-4 font-mono text-xs break-all">
                                        {entry.original_path || (
                                            <span className="text-muted-foreground italic">
                                                asal tidak diketahui
                                            </span>
                                        )}
                                    </td>
                                    <td className="py-2 pr-4">
                                        {entry.owner || '—'}
                                    </td>
                                    <td className="py-2 pr-4 font-mono text-xs break-all">
                                        {entry.virus}
                                    </td>
                                    <td className="py-2 pr-4 whitespace-nowrap text-muted-foreground">
                                        {entry.quarantined_at
                                            ? formatDateTime(
                                                  entry.quarantined_at,
                                              )
                                            : '—'}
                                    </td>
                                    <td className="py-2 pr-4">
                                        <StatusBadge
                                            status="neutral"
                                            label={
                                                sourceLabels[entry.source] ??
                                                entry.source
                                            }
                                        />
                                    </td>
                                    <td className="py-2">
                                        <div className="flex justify-end gap-2">
                                            <Button
                                                variant="outline"
                                                size="sm"
                                                disabled={!entry.original_path}
                                                onClick={() =>
                                                    setAction({
                                                        type: 'restore',
                                                        entry,
                                                    })
                                                }
                                            >
                                                <RotateCcw className="size-4" />
                                                Pulihkan
                                            </Button>
                                            <Button
                                                variant="outline"
                                                size="sm"
                                                onClick={() =>
                                                    setAction({
                                                        type: 'delete',
                                                        entry,
                                                    })
                                                }
                                            >
                                                <Trash2 className="size-4" />
                                                Hapus
                                            </Button>
                                        </div>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}

            <ConfirmDialog action={action} onClose={() => setAction(null)} />
        </Panel>
    );
}

function ConfirmDialog({
    action,
    onClose,
}: {
    action: { type: 'restore' | 'delete'; entry: QuarantineEntry } | null;
    onClose: () => void;
}) {
    const isRestore = action?.type === 'restore';
    const form = isRestore ? restoreQuarantine : deleteQuarantine;

    return (
        <Dialog
            open={action !== null}
            onOpenChange={(open) => !open && onClose()}
        >
            <DialogContent className="rounded-2xl">
                {action && (
                    <Form
                        {...form.form()}
                        options={{ preserveScroll: true }}
                        transform={() => ({ id: action.entry.id })}
                        onSuccess={onClose}
                    >
                        {({ processing }) => (
                            <>
                                <DialogHeader>
                                    <DialogTitle>
                                        {isRestore
                                            ? 'Pulihkan file ini?'
                                            : 'Hapus permanen?'}
                                    </DialogTitle>
                                    <DialogDescription>
                                        {isRestore ? (
                                            <>
                                                File ini terdeteksi terinfeksi (
                                                <span className="font-mono">
                                                    {action.entry.virus}
                                                </span>
                                                ). Memulihkan akan
                                                mengembalikannya ke{' '}
                                                <span className="font-mono break-all">
                                                    {action.entry.original_path}
                                                </span>
                                                . Lakukan hanya jika kamu yakin
                                                ini false positive.
                                            </>
                                        ) : (
                                            <>
                                                File{' '}
                                                <span className="font-mono break-all">
                                                    {action.entry
                                                        .original_path ||
                                                        action.entry.id}
                                                </span>{' '}
                                                akan dihapus permanen dari
                                                karantina. Tindakan ini tidak
                                                bisa dibatalkan.
                                            </>
                                        )}
                                    </DialogDescription>
                                </DialogHeader>
                                <DialogFooter>
                                    <DialogClose asChild>
                                        <Button type="button" variant="outline">
                                            Batal
                                        </Button>
                                    </DialogClose>
                                    <Button
                                        type="submit"
                                        variant={
                                            isRestore
                                                ? 'default'
                                                : 'destructive'
                                        }
                                        disabled={processing}
                                    >
                                        {processing && <Spinner />}
                                        {isRestore ? 'Pulihkan' : 'Hapus'}
                                    </Button>
                                </DialogFooter>
                            </>
                        )}
                    </Form>
                )}
            </DialogContent>
        </Dialog>
    );
}
