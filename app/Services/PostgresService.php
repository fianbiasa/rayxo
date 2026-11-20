<?php

namespace App\Services;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Enums\AccountStatus;
use App\Enums\DatabaseStatus;
use App\Enums\Role;
use App\Listeners\ApplyPostgresTaskResult;
use App\Models\PostgresDatabase;
use App\Models\PostgresUser;
use App\Models\User;
use Illuminate\Database\UniqueConstraintViolationException;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use Illuminate\Validation\ValidationException;

/**
 * Adds and removes the PostgreSQL databases and roles of hosting accounts.
 * The panel record is updated right away and the change on the server is
 * queued for the agent; {@see ApplyPostgresTaskResult} applies the results.
 *
 * Databases and roles are named <username>_<name>. A role's password is kept
 * encrypted (not hashed), because PostgreSQL makes its own SCRAM verifier
 * from the plaintext; every change of a role sends its whole state (password
 * and databases), so applying it again is always safe.
 */
class PostgresService
{
    /**
     * How long a dump may take, in seconds.
     */
    private const LONG_TIMEOUT = 600;

    public function __construct(
        private TaskDispatcher $tasks,
        private AuditLogger $audit,
        private AgentClient $agent,
    ) {}

    /**
     * Add a database to the account, and optionally a new role that owns it.
     *
     * @param  string  $name  The part after "<username>_", already validated.
     * @param  array{name: string, password: string}|null  $newUser  A role to create along with it.
     *
     * @throws ValidationException
     */
    public function createDatabase(User $account, string $name, User $actor, ?array $newUser = null): PostgresDatabase
    {
        $this->ensureAccountIsActive($account, 'name');

        try {
            return DB::transaction(function () use ($account, $name, $actor, $newUser): PostgresDatabase {
                $database = $account->postgresDatabases()->make(['name' => $account->username.'_'.$name]);
                $database->forceFill(['status' => DatabaseStatus::Pending])->save();

                $this->tasks->dispatch('pg.create_database', [
                    'username' => $account->username,
                    'database' => $database->name,
                ], $account, $actor);

                $this->audit->record('postgres.created', $database, [
                    'database' => $database->name,
                    'username' => $account->username,
                ], $actor);

                if ($newUser !== null) {
                    $this->addUser($account, $newUser['name'], $newUser['password'], [$database], $actor);
                }

                return $database;
            });
        } catch (UniqueConstraintViolationException) {
            throw ValidationException::withMessages(['name' => __('This name is already in use.')]);
        }
    }

    /**
     * Drop a database. The panel record goes once the agent reports success.
     *
     * @throws ValidationException
     */
    public function deleteDatabase(PostgresDatabase $database, User $actor): void
    {
        $this->ensureSettled($database->status, 'database');

        $account = $database->account;

        DB::transaction(function () use ($database, $account, $actor): void {
            $database->forceFill(['status' => DatabaseStatus::Deleting])->save();

            $this->tasks->dispatch('pg.delete_database', [
                'username' => $account->username,
                'database' => $database->name,
            ], $account, $actor);

            $this->audit->record('postgres.deleted', $database, [
                'database' => $database->name,
                'username' => $account->username,
            ], $actor);
        });
    }

    /**
     * Add a role to the account that owns the given databases of it.
     *
     * @param  string  $name  The part after "<username>_", already validated.
     * @param  list<int>  $databaseIds
     *
     * @throws ValidationException
     */
    public function createUser(User $account, string $name, string $password, array $databaseIds, User $actor): PostgresUser
    {
        $this->ensureAccountIsActive($account, 'name');

        $databases = $this->accountDatabases($account, $databaseIds);

        try {
            return DB::transaction(fn (): PostgresUser => $this->addUser($account, $name, $password, $databases, $actor));
        } catch (UniqueConstraintViolationException) {
            throw ValidationException::withMessages(['name' => __('This name is already in use.')]);
        }
    }

    /**
     * Give a role a new password (when one is given) and ownership of exactly
     * the given databases of its account.
     *
     * @param  list<int>  $databaseIds
     *
     * @throws ValidationException
     */
    public function updateUser(PostgresUser $user, ?string $password, array $databaseIds, User $actor): void
    {
        $this->ensureSettled($user->status, 'databases');

        $account = $user->account;
        $this->ensureAccountIsActive($account, 'databases');

        $databases = $this->accountDatabases($account, $databaseIds);

        DB::transaction(function () use ($user, $password, $databases, $account, $actor): void {
            $before = $user->databases()->orderBy('name')->pluck('name')->all();

            if ($password !== null) {
                $user->forceFill(['password' => $password]);
            }

            $user->forceFill(['status' => DatabaseStatus::Pending])->save();
            $user->databases()->sync(array_map(fn (PostgresDatabase $database): int => $database->id, $databases));

            $this->dispatchApplyUser($user, $account, $actor);

            $this->audit->record('postgres_user.updated', $user, [
                'user' => $user->name,
                'password_changed' => $password !== null,
                'databases' => ['from' => $before, 'to' => array_map(fn (PostgresDatabase $database): string => $database->name, $databases)],
                'username' => $account->username,
            ], $actor);
        });
    }

    /**
     * Give a role a fresh password, keeping its databases.
     *
     * @throws ValidationException
     */
    public function rotatePassword(PostgresUser $user, User $actor): string
    {
        $this->ensureSettled($user->status, 'user');

        $account = $user->account;
        $this->ensureAccountIsActive($account, 'user');

        $password = PostgresUser::newPassword();

        DB::transaction(function () use ($user, $password, $account, $actor): void {
            $user->forceFill(['password' => $password, 'status' => DatabaseStatus::Pending])->save();

            $this->dispatchApplyUser($user, $account, $actor);

            $this->audit->record('postgres_user.password_rotated', $user, [
                'user' => $user->name,
                'username' => $account->username,
            ], $actor);
        });

        return $password;
    }

    /**
     * Drop a role. The panel record goes once the agent reports success.
     *
     * @throws ValidationException
     */
    public function deleteUser(PostgresUser $user, User $actor): void
    {
        $this->ensureSettled($user->status, 'user');

        $account = $user->account;

        DB::transaction(function () use ($user, $account, $actor): void {
            $user->forceFill(['status' => DatabaseStatus::Deleting])->save();

            $this->tasks->dispatch('pg.delete_user', [
                'username' => $account->username,
                'user' => $user->name,
            ], $account, $actor);

            $this->audit->record('postgres_user.deleted', $user, [
                'user' => $user->name,
                'username' => $account->username,
            ], $actor);
        });
    }

    /**
     * Have the agent write a gzipped dump of the database to the transfer
     * folder, for the browser to download.
     *
     * @return array{file: string, name: string}
     *
     * @throws ValidationException
     */
    public function export(PostgresDatabase $database, User $actor): array
    {
        $this->ensureReady($database, 'database');

        $transfer = Str::random(48);

        try {
            $this->agent->call('pg.export', [
                'username' => $database->account->username,
                'database' => $database->name,
                'transfer' => $transfer,
            ], self::LONG_TIMEOUT);
        } catch (AgentException $exception) {
            report($exception);

            throw ValidationException::withMessages(['database' => __('The database could not be exported. Try again in a moment.')]);
        }

        $this->audit->record('postgres.exported', $database, [
            'database' => $database->name,
            'username' => $database->account->username,
        ], $actor);

        return [
            'file' => FileManagerService::transferPath($transfer.'.download'),
            'name' => $database->name.'-'.now()->format('Ymd-His').'.sql.gz',
        ];
    }

    /**
     * Queue running the SQL file at path (in the account's home, plain or
     * gzip) in the database; with remove, the file goes afterwards.
     *
     * @throws ValidationException
     */
    public function import(PostgresDatabase $database, string $path, User $actor, bool $remove = false): void
    {
        $this->ensureReady($database, 'file');

        if ($database->import_status === 'running') {
            throw ValidationException::withMessages(['file' => __('An import into this database is still running.')]);
        }

        $account = $database->account;

        $owners = $database->users()->get()
            ->map(fn (PostgresUser $user): array => ['name' => $user->name, 'password' => $user->password])
            ->all();

        if ($owners === []) {
            throw ValidationException::withMessages(['file' => __('Give this database a user before importing into it.')]);
        }

        DB::transaction(function () use ($database, $path, $remove, $account, $owners, $actor): void {
            $database->forceFill(['import_status' => 'running', 'import_error' => null])->save();

            $this->tasks->dispatch('pg.import', [
                'username' => $account->username,
                'database' => $database->name,
                'path' => $path,
                'owners' => $owners,
                'remove' => $remove,
            ], $account, $actor);

            $this->audit->record('postgres.imported', $database, [
                'database' => $database->name,
                'username' => $account->username,
            ], $actor);
        });
    }

    /**
     * Store the size of every database as the agent measures it now.
     *
     * @return int The number of databases measured.
     *
     * @throws AgentException When the agent is unreachable or reports an error; nothing is stored then.
     */
    public function collectSizes(): int
    {
        $report = $this->agent->call('pg.report', [], (int) config('agent.timeout'));
        $measuredAt = now();
        $measured = 0;

        foreach ($report['databases'] ?? [] as $row) {
            if (! is_array($row) || ! is_string($row['name'] ?? null) || ! is_int($row['size_bytes'] ?? null)) {
                continue;
            }

            $measured += PostgresDatabase::query()
                ->where('name', $row['name'])
                ->update(['size_bytes' => max(0, $row['size_bytes']), 'measured_at' => $measuredAt]);
        }

        return $measured;
    }

    /**
     * Queue every database and role of the accounts on the server again: on a
     * new server the panel was restored on. Returns how many were queued.
     */
    public function reapplyAll(?User $actor = null): int
    {
        $accounts = fn ($query) => $query->where('role', Role::User)->whereIn('status', [AccountStatus::Active, AccountStatus::Suspended, AccountStatus::Pending]);
        $count = 0;

        PostgresDatabase::query()
            ->with('account')
            ->whereIn('status', [DatabaseStatus::Active, DatabaseStatus::Pending])
            ->whereHas('account', $accounts)
            ->orderBy('id')
            ->each(function (PostgresDatabase $database) use ($actor, &$count): void {
                $this->tasks->dispatch('pg.create_database', [
                    'username' => $database->account->username,
                    'database' => $database->name,
                ], $database->account, $actor);

                $count++;
            });

        PostgresUser::query()
            ->with('account')
            ->whereHas('account', $accounts)
            ->orderBy('id')
            ->each(fn (PostgresUser $user) => $this->dispatchApplyUser($user, $user->account, $actor));

        return $count;
    }

    /**
     * Save a new role and queue it.
     *
     * @param  list<PostgresDatabase>  $databases
     */
    private function addUser(User $account, string $name, string $password, array $databases, User $actor): PostgresUser
    {
        $user = $account->postgresUsers()->make(['name' => $account->username.'_'.$name]);
        $user->forceFill([
            'password' => $password,
            'status' => DatabaseStatus::Pending,
        ])->save();

        $user->databases()->attach(array_map(fn (PostgresDatabase $database): int => $database->id, $databases));

        $this->dispatchApplyUser($user, $account, $actor);

        $this->audit->record('postgres_user.created', $user, [
            'user' => $user->name,
            'databases' => array_map(fn (PostgresDatabase $database): string => $database->name, $databases),
            'username' => $account->username,
        ], $actor);

        return $user;
    }

    /**
     * Queue the whole state of a role: its password and the databases it
     * owns, leaving out those being dropped.
     */
    private function dispatchApplyUser(PostgresUser $user, User $account, ?User $actor): void
    {
        $databases = $user->databases()
            ->where('status', '!=', DatabaseStatus::Deleting)
            ->orderBy('name')
            ->pluck('name')
            ->all();

        $this->tasks->dispatch('pg.apply_user', [
            'username' => $account->username,
            'user' => $user->name,
            'password' => $user->password,
            'databases' => $databases,
        ], $account, $actor);
    }

    /**
     * The given databases of the account, refusing any other.
     *
     * @param  list<int>  $ids
     * @return list<PostgresDatabase>
     *
     * @throws ValidationException
     */
    private function accountDatabases(User $account, array $ids): array
    {
        $databases = $account->postgresDatabases()
            ->whereIn('id', $ids)
            ->where('status', '!=', DatabaseStatus::Deleting)
            ->orderBy('name')
            ->get();

        if ($databases->count() !== count(array_unique($ids))) {
            throw ValidationException::withMessages(['databases' => __('Choose databases of this account.')]);
        }

        return array_values($databases->all());
    }

    /**
     * @throws ValidationException
     */
    private function ensureAccountIsActive(User $account, string $errorKey): void
    {
        if (! $account->hasRole(Role::User) || $account->status !== AccountStatus::Active) {
            throw ValidationException::withMessages([
                $errorKey => __('Databases can only be changed while the account is active.'),
            ]);
        }
    }

    /**
     * @throws ValidationException
     */
    private function ensureSettled(DatabaseStatus $status, string $errorKey): void
    {
        if (! $status->isSettled()) {
            throw ValidationException::withMessages([
                $errorKey => __('Wait until the server has finished the previous change.'),
            ]);
        }
    }

    /**
     * @throws ValidationException
     */
    private function ensureReady(PostgresDatabase $database, string $errorKey): void
    {
        $this->ensureSettled($database->status, $errorKey);
        $this->ensureAccountIsActive($database->account, $errorKey);
    }
}
