import { Form, Head } from '@inertiajs/react';
import { Copy } from 'lucide-react';
import { toast } from 'sonner';
import InputError from '@/components/input-error';
import { PageHeader } from '@/components/page-header';
import { Panel } from '@/components/panel';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Spinner } from '@/components/ui/spinner';
import { destroy, rotate, store } from '@/routes/redis';

interface Connection {
    host: string;
    port: number;
    password: string;
    prefix: string;
    client: string;
    max_memory_mb: number;
}

export default function RedisIndex({
    enabled,
    connection,
}: {
    enabled: boolean;
    connection: Connection | null;
}) {
    return (
        <>
            <Head title="Redis" />
            <div className="space-y-6">
                <PageHeader
                    title="Redis"
                    description="Cache Redis khusus untuk akunmu, mempercepat aplikasi seperti Laravel atau WordPress. Terisolasi: hanya aplikasimu yang bisa mengaksesnya."
                />

                {!enabled || !connection ? (
                    <EnablePanel />
                ) : (
                    <ConnectionPanels connection={connection} />
                )}
            </div>
        </>
    );
}

function EnablePanel() {
    return (
        <Panel
            title="Aktifkan Redis"
            description="Menyalakan satu instance Redis untuk akun ini, berjalan sebagai user-mu dan hanya lewat socket pribadi. Default memori 256 MB."
        >
            <Form
                {...store.form()}
                options={{ preserveScroll: true }}
                className="flex flex-wrap items-end gap-4"
            >
                {({ processing, errors }) => (
                    <>
                        <div className="space-y-1.5">
                            <Label htmlFor="max_memory_mb">
                                Batas memori (MB)
                            </Label>
                            <Input
                                id="max_memory_mb"
                                name="max_memory_mb"
                                type="number"
                                min={16}
                                max={8192}
                                defaultValue={256}
                                className="w-40"
                            />
                        </div>
                        <Button type="submit" disabled={processing}>
                            {processing && <Spinner />}
                            Aktifkan Redis
                        </Button>
                        <InputError message={errors.max_memory_mb} />
                    </>
                )}
            </Form>
        </Panel>
    );
}

function ConnectionPanels({ connection }: { connection: Connection }) {
    const copy = (value: string, what: string) => {
        void navigator.clipboard?.writeText(value);
        toast.success(`${what} disalin.`);
    };

    const envExample = [
        `REDIS_CLIENT=${connection.client}`,
        `REDIS_HOST=${connection.host}`,
        `REDIS_PORT=${connection.port}`,
        `REDIS_PASSWORD=${connection.password}`,
        `REDIS_PREFIX=${connection.prefix}`,
    ].join('\n');

    return (
        <>
            <Panel
                title="Koneksi"
                description="Pakai nilai ini di aplikasimu. Redis berjalan tanpa TCP; aplikasi menyambung lewat socket unix di bawah."
                action={<StatusBadge status="active" label="Aktif" />}
            >
                <dl className="grid gap-4 sm:grid-cols-2">
                    <Row
                        label="Host (socket)"
                        value={connection.host}
                        onCopy={copy}
                    />
                    <Row
                        label="Port"
                        value={String(connection.port)}
                        onCopy={copy}
                    />
                    <Row
                        label="Password"
                        value={connection.password}
                        onCopy={copy}
                    />
                    <Row
                        label="Prefix kunci"
                        value={connection.prefix}
                        onCopy={copy}
                    />
                    <Row
                        label="Client"
                        value={connection.client}
                        onCopy={copy}
                    />
                    <Row
                        label="Batas memori"
                        value={`${connection.max_memory_mb} MB`}
                        onCopy={copy}
                    />
                </dl>

                <div className="mt-6 space-y-2">
                    <div className="flex items-center justify-between">
                        <Label>Contoh .env (Laravel)</Label>
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => copy(envExample, 'Blok .env')}
                        >
                            <Copy className="size-4" /> Salin
                        </Button>
                    </div>
                    <pre className="overflow-x-auto rounded-xl bg-muted p-4 text-xs">
                        {envExample}
                    </pre>
                    <p className="text-xs text-muted-foreground">
                        Untuk WordPress (plugin Redis Object Cache): host dan
                        password sama, lalu set prefix ke{' '}
                        <code>{connection.prefix}</code>.
                    </p>
                </div>
            </Panel>

            <Panel
                title="Kelola"
                description="Ganti password atau matikan cache akun ini."
            >
                <div className="flex flex-wrap items-center gap-3">
                    <Form {...rotate.form()} options={{ preserveScroll: true }}>
                        {({ processing }) => (
                            <Button
                                type="submit"
                                variant="outline"
                                disabled={processing}
                            >
                                {processing && <Spinner />}
                                Ganti password
                            </Button>
                        )}
                    </Form>
                    <Form
                        {...destroy.form()}
                        options={{ preserveScroll: true }}
                    >
                        {({ processing }) => (
                            <Button
                                type="submit"
                                variant="destructive"
                                disabled={processing}
                            >
                                {processing && <Spinner />}
                                Matikan Redis
                            </Button>
                        )}
                    </Form>
                </div>
            </Panel>
        </>
    );
}

function Row({
    label,
    value,
    onCopy,
}: {
    label: string;
    value: string;
    onCopy: (value: string, what: string) => void;
}) {
    return (
        <div className="space-y-1">
            <dt className="text-xs font-medium text-muted-foreground">
                {label}
            </dt>
            <dd className="flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded-md bg-muted px-2 py-1 text-sm">
                    {value}
                </code>
                <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => onCopy(value, label)}
                    aria-label={`Salin ${label}`}
                >
                    <Copy className="size-4" />
                </Button>
            </dd>
        </div>
    );
}
