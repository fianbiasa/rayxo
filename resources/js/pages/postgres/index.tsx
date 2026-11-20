import { Form, Head, useForm } from '@inertiajs/react';
import {
    Copy,
    Database as DatabaseIcon,
    Download,
    KeyRound,
} from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import InputError from '@/components/input-error';
import { PageHeader } from '@/components/page-header';
import { Panel } from '@/components/panel';
import { StatusBadge } from '@/components/status-badge';
import { AffixInput } from '@/components/affix-input';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Spinner } from '@/components/ui/spinner';
import { formatBytes } from '@/lib/format';
import { destroy, exportMethod, importMethod, store } from '@/routes/postgres';
import {
    destroy as destroyRole,
    rotate,
    store as storeRole,
} from '@/routes/postgres-users';

interface PgDatabase {
    id: number;
    name: string;
    status: string;
    size_bytes: number | null;
    users: string[];
    import_status: string | null;
    import_error: string | null;
}

interface PgRole {
    id: number;
    name: string;
    status: string;
    password: string;
    database_ids: number[];
}

interface Props {
    databases: PgDatabase[];
    postgres_users: PgRole[];
    prefix: string;
    max_lengths: { database: number; user: number };
    connection: { host: string; port: number };
}

const STATUS: Record<
    string,
    { status: 'active' | 'pending' | 'neutral' | 'error'; label: string }
> = {
    active: { status: 'active', label: 'Aktif' },
    pending: { status: 'pending', label: 'Menyiapkan…' },
    deleting: { status: 'neutral', label: 'Menghapus…' },
    failed: { status: 'error', label: 'Gagal' },
};

function statusOf(value: string) {
    return STATUS[value] ?? { status: 'neutral' as const, label: value };
}

export default function PostgresIndex({
    databases,
    postgres_users,
    prefix,
    max_lengths,
    connection,
}: Props) {
    return (
        <>
            <Head title="PostgreSQL" />
            <div className="space-y-6">
                <PageHeader
                    title="PostgreSQL"
                    description="Database PostgreSQL untuk aplikasi akunmu. Tiap database punya role pemiliknya sendiri; aplikasi menyambung lewat 127.0.0.1 dengan password di bawah."
                />

                <DatabasesPanel
                    databases={databases}
                    prefix={prefix}
                    maxLength={max_lengths.database}
                />
                <RolesPanel
                    roles={postgres_users}
                    databases={databases}
                    prefix={prefix}
                    maxLength={max_lengths.user}
                    connection={connection}
                />
            </div>
        </>
    );
}

function DatabasesPanel({
    databases,
    prefix,
    maxLength,
}: {
    databases: PgDatabase[];
    prefix: string;
    maxLength: number;
}) {
    return (
        <Panel
            title="Database"
            description="Buat database PostgreSQL, unduh dump-nya, atau jalankan file SQL ke dalamnya."
        >
            <CreateDatabaseForm prefix={prefix} maxLength={maxLength} />

            {databases.length === 0 ? (
                <p className="mt-6 text-sm text-muted-foreground">
                    Belum ada database.
                </p>
            ) : (
                <ul className="mt-6 divide-y rounded-xl border">
                    {databases.map((database) => (
                        <DatabaseRow key={database.id} database={database} />
                    ))}
                </ul>
            )}
        </Panel>
    );
}

function CreateDatabaseForm({
    prefix,
    maxLength,
}: {
    prefix: string;
    maxLength: number;
}) {
    const form = useForm<{
        name: string;
        create_user: boolean;
        user_name: string;
    }>({
        name: '',
        create_user: true,
        user_name: '',
    });

    const submit = (event: React.FormEvent) => {
        event.preventDefault();
        form.post(store.url(), {
            preserveScroll: true,
            onSuccess: () => form.reset(),
        });
    };

    return (
        <form onSubmit={submit} className="space-y-4">
            <div className="flex flex-wrap items-end gap-4">
                <div className="space-y-1.5">
                    <Label htmlFor="name">Nama database</Label>
                    <AffixInput
                        id="name"
                        prefix={prefix}
                        maxLength={maxLength}
                        value={form.data.name}
                        onChange={(event) =>
                            form.setData('name', event.target.value)
                        }
                        className="w-56"
                    />
                    <InputError message={form.errors.name} />
                </div>
                <Button type="submit" disabled={form.processing}>
                    {form.processing && <Spinner />}
                    Buat database
                </Button>
            </div>
            <label className="flex items-center gap-2 text-sm">
                <Checkbox
                    checked={form.data.create_user}
                    onCheckedChange={(checked) =>
                        form.setData('create_user', checked === true)
                    }
                />
                Buat role pemilik sekaligus
            </label>
            {form.data.create_user && (
                <div className="space-y-1.5">
                    <Label htmlFor="user_name">Nama role</Label>
                    <AffixInput
                        id="user_name"
                        prefix={prefix}
                        maxLength={maxLength}
                        value={form.data.user_name}
                        onChange={(event) =>
                            form.setData('user_name', event.target.value)
                        }
                        className="w-56"
                    />
                    <InputError message={form.errors.user_name} />
                    <p className="text-xs text-muted-foreground">
                        Password role dibuat otomatis dan muncul di daftar role.
                    </p>
                </div>
            )}
        </form>
    );
}

function DatabaseRow({ database }: { database: PgDatabase }) {
    const [importing, setImporting] = useState(false);
    const badge = statusOf(database.status);

    return (
        <li className="flex flex-wrap items-center justify-between gap-3 p-4">
            <div className="min-w-0">
                <div className="flex items-center gap-2">
                    <DatabaseIcon className="size-4 text-muted-foreground" />
                    <span className="font-medium">{database.name}</span>
                    <StatusBadge status={badge.status} label={badge.label} />
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                    {formatBytes(database.size_bytes ?? 0)}
                    {database.users.length > 0 && (
                        <> · pemilik: {database.users.join(', ')}</>
                    )}
                    {database.import_status === 'running' && (
                        <> · import berjalan…</>
                    )}
                    {database.import_status === 'failed' && (
                        <> · import gagal: {database.import_error}</>
                    )}
                </p>
            </div>
            <div className="flex items-center gap-2">
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setImporting((value) => !value)}
                >
                    Import
                </Button>
                <Button asChild variant="outline" size="sm">
                    <a href={exportMethod.url(database.id)}>
                        <Download className="size-4" /> Export
                    </a>
                </Button>
                <Form
                    {...destroy.form(database.id)}
                    options={{ preserveScroll: true }}
                >
                    {({ processing }) => (
                        <Button
                            type="submit"
                            variant="destructive"
                            size="sm"
                            disabled={processing}
                        >
                            {processing && <Spinner />}
                            Hapus
                        </Button>
                    )}
                </Form>
            </div>
            {importing && (
                <Form
                    {...importMethod.form(database.id)}
                    options={{ preserveScroll: true }}
                    className="flex w-full flex-wrap items-end gap-3 border-t pt-3"
                >
                    {({ processing, errors }) => (
                        <>
                            <div className="space-y-1.5">
                                <Label htmlFor={`path-${database.id}`}>
                                    Path file SQL di home (mis. backup.sql atau
                                    backup.sql.gz)
                                </Label>
                                <Input
                                    id={`path-${database.id}`}
                                    name="path"
                                    placeholder="backup.sql"
                                    className="w-80"
                                />
                                <InputError message={errors.path} />
                            </div>
                            <Button
                                type="submit"
                                variant="outline"
                                disabled={processing}
                            >
                                {processing && <Spinner />}
                                Jalankan import
                            </Button>
                        </>
                    )}
                </Form>
            )}
        </li>
    );
}

function RolesPanel({
    roles,
    databases,
    prefix,
    maxLength,
    connection,
}: {
    roles: PgRole[];
    databases: PgDatabase[];
    prefix: string;
    maxLength: number;
    connection: { host: string; port: number };
}) {
    return (
        <Panel
            title="Role"
            description="Role PostgreSQL yang memiliki database. Password tampil di sini untuk dipakai aplikasimu."
        >
            <CreateRoleForm
                databases={databases}
                prefix={prefix}
                maxLength={maxLength}
            />

            {roles.length === 0 ? (
                <p className="mt-6 text-sm text-muted-foreground">
                    Belum ada role.
                </p>
            ) : (
                <ul className="mt-6 divide-y rounded-xl border">
                    {roles.map((role) => (
                        <RoleRow
                            key={role.id}
                            role={role}
                            databases={databases}
                            connection={connection}
                        />
                    ))}
                </ul>
            )}
        </Panel>
    );
}

function CreateRoleForm({
    databases,
    prefix,
    maxLength,
}: {
    databases: PgDatabase[];
    prefix: string;
    maxLength: number;
}) {
    const form = useForm<{ name: string; databases: number[] }>({
        name: '',
        databases: [],
    });

    const toggle = (id: number, checked: boolean) => {
        form.setData(
            'databases',
            checked
                ? [...form.data.databases, id]
                : form.data.databases.filter((value) => value !== id),
        );
    };

    const submit = (event: React.FormEvent) => {
        event.preventDefault();
        form.post(storeRole.url(), {
            preserveScroll: true,
            onSuccess: () => form.reset(),
        });
    };

    return (
        <form onSubmit={submit} className="space-y-4">
            <div className="flex flex-wrap items-end gap-4">
                <div className="space-y-1.5">
                    <Label htmlFor="role_name">Nama role</Label>
                    <AffixInput
                        id="role_name"
                        prefix={prefix}
                        maxLength={maxLength}
                        value={form.data.name}
                        onChange={(event) =>
                            form.setData('name', event.target.value)
                        }
                        className="w-56"
                    />
                    <InputError message={form.errors.name} />
                </div>
                <Button type="submit" disabled={form.processing}>
                    {form.processing && <Spinner />}
                    Buat role
                </Button>
            </div>
            {databases.length > 0 && (
                <div className="space-y-1.5">
                    <Label>Database yang dimiliki</Label>
                    <div className="flex flex-wrap gap-3">
                        {databases.map((database) => (
                            <label
                                key={database.id}
                                className="flex items-center gap-2 text-sm"
                            >
                                <Checkbox
                                    checked={form.data.databases.includes(
                                        database.id,
                                    )}
                                    onCheckedChange={(checked) =>
                                        toggle(database.id, checked === true)
                                    }
                                />
                                {database.name}
                            </label>
                        ))}
                    </div>
                    <InputError message={form.errors.databases} />
                </div>
            )}
        </form>
    );
}

function RoleRow({
    role,
    databases,
    connection,
}: {
    role: PgRole;
    databases: PgDatabase[];
    connection: { host: string; port: number };
}) {
    const [open, setOpen] = useState(false);
    const badge = statusOf(role.status);
    const owned = databases.filter((database) =>
        role.database_ids.includes(database.id),
    );
    const firstDb = owned[0]?.name ?? '';

    const copy = (value: string, what: string) => {
        void navigator.clipboard?.writeText(value);
        toast.success(`${what} disalin.`);
    };

    const envExample = [
        'DB_CONNECTION=pgsql',
        `DB_HOST=${connection.host}`,
        `DB_PORT=${connection.port}`,
        `DB_DATABASE=${firstDb}`,
        `DB_USERNAME=${role.name}`,
        `DB_PASSWORD=${role.password}`,
    ].join('\n');

    return (
        <li className="p-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex items-center gap-2">
                    <KeyRound className="size-4 text-muted-foreground" />
                    <span className="font-medium">{role.name}</span>
                    <StatusBadge status={badge.status} label={badge.label} />
                </div>
                <div className="flex items-center gap-2">
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => setOpen((value) => !value)}
                    >
                        Koneksi
                    </Button>
                    <Form
                        {...rotate.form(role.id)}
                        options={{ preserveScroll: true }}
                    >
                        {({ processing }) => (
                            <Button
                                type="submit"
                                variant="outline"
                                size="sm"
                                disabled={processing}
                            >
                                {processing && <Spinner />}
                                Ganti password
                            </Button>
                        )}
                    </Form>
                    <Form
                        {...destroyRole.form(role.id)}
                        options={{ preserveScroll: true }}
                    >
                        {({ processing }) => (
                            <Button
                                type="submit"
                                variant="destructive"
                                size="sm"
                                disabled={processing}
                            >
                                {processing && <Spinner />}
                                Hapus
                            </Button>
                        )}
                    </Form>
                </div>
            </div>
            {owned.length > 0 && (
                <p className="mt-1 text-xs text-muted-foreground">
                    Memiliki:{' '}
                    {owned.map((database) => database.name).join(', ')}
                </p>
            )}
            {open && (
                <div className="mt-3 space-y-2 border-t pt-3">
                    <div className="grid gap-3 sm:grid-cols-2">
                        <ConnRow
                            label="Host"
                            value={connection.host}
                            onCopy={copy}
                        />
                        <ConnRow
                            label="Port"
                            value={String(connection.port)}
                            onCopy={copy}
                        />
                        <ConnRow label="Role" value={role.name} onCopy={copy} />
                        <ConnRow
                            label="Password"
                            value={role.password}
                            onCopy={copy}
                        />
                    </div>
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
                </div>
            )}
        </li>
    );
}

function ConnRow({
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
