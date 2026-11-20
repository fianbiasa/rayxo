<?php

namespace Tests\Feature\Postgres;

use App\Enums\DatabaseStatus;
use App\Models\PostgresDatabase;
use App\Models\PostgresUser;
use App\Models\Task;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Queue;
use Inertia\Testing\AssertableInertia as Assert;
use Tests\TestCase;

class PostgresManagementTest extends TestCase
{
    use RefreshDatabase;

    protected function setUp(): void
    {
        parent::setUp();

        Queue::fake();
    }

    private function database(User $account, string $name, DatabaseStatus $status = DatabaseStatus::Active): PostgresDatabase
    {
        $database = $account->postgresDatabases()->make(['name' => $name]);
        $database->forceFill(['status' => $status])->save();

        return $database;
    }

    public function test_admins_cannot_reach_postgres()
    {
        $this->actingAs(User::factory()->admin()->create())
            ->get(route('postgres.index'))
            ->assertForbidden();
    }

    public function test_the_page_lists_the_accounts_databases_and_roles()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $shop = $this->database($account, 'budi_shop');
        $shop->forceFill(['size_bytes' => 2048])->save();

        $role = $account->postgresUsers()->make(['name' => 'budi_app']);
        $role->forceFill(['password' => 'Abcd1234Efgh5678ijkl9012', 'status' => DatabaseStatus::Active])->save();
        $role->databases()->attach($shop);

        $this->database(User::factory()->create(), 'other_db');

        $this->actingAs($account)
            ->get(route('postgres.index'))
            ->assertOk()
            ->assertInertia(fn (Assert $page) => $page
                ->component('postgres/index')
                ->has('databases', 1)
                ->where('databases.0.name', 'budi_shop')
                ->where('databases.0.size_bytes', 2048)
                ->where('databases.0.users', ['budi_app'])
                ->has('postgres_users', 1)
                ->where('postgres_users.0.name', 'budi_app')
                ->where('postgres_users.0.password', 'Abcd1234Efgh5678ijkl9012')
                ->where('postgres_users.0.database_ids', [$shop->id])
                ->where('prefix', 'budi_')
                ->where('connection', ['host' => '127.0.0.1', 'port' => 5432]));
    }

    public function test_accounts_can_create_a_database_with_a_role()
    {
        $account = User::factory()->create(['username' => 'budi']);

        $this->actingAs($account)
            ->post(route('postgres.store'), [
                'name' => ' Budi_Shop ',
                'create_user' => '1',
                'user_name' => 'app',
            ])
            ->assertSessionHasNoErrors()
            ->assertRedirect(route('postgres.index'));

        $database = PostgresDatabase::query()->sole();
        $role = PostgresUser::query()->sole();

        $this->assertSame('budi_shop', $database->name);
        $this->assertSame(DatabaseStatus::Pending, $database->status);
        $this->assertSame('budi_app', $role->name);
        $this->assertMatchesRegularExpression('/^[A-Za-z0-9]{24}$/', $role->password);
        $this->assertTrue($role->databases->first()->is($database));

        $tasks = Task::query()->orderBy('id')->get();
        $this->assertSame(['pg.create_database', 'pg.apply_user'], $tasks->pluck('action')->all());
        $this->assertSame([
            'username' => 'budi',
            'user' => 'budi_app',
            'password' => $role->password,
            'databases' => ['budi_shop'],
        ], $tasks->last()->payload);
    }

    public function test_a_database_can_be_created_without_a_role()
    {
        $account = User::factory()->create(['username' => 'budi']);

        $this->actingAs($account)
            ->post(route('postgres.store'), ['name' => 'blog', 'create_user' => '0'])
            ->assertSessionHasNoErrors();

        $this->assertSame('budi_blog', PostgresDatabase::query()->sole()->name);
        $this->assertDatabaseCount('postgres_users', 0);
        $this->assertSame(['pg.create_database'], Task::query()->pluck('action')->all());
    }

    public function test_an_invalid_database_name_is_rejected()
    {
        $account = User::factory()->create(['username' => 'budi']);

        $this->actingAs($account)
            ->post(route('postgres.store'), ['name' => 'my-shop', 'create_user' => '0'])
            ->assertSessionHasErrors('name');

        $this->assertDatabaseCount('postgres_databases', 0);
    }

    public function test_a_roles_password_can_be_rotated()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $shop = $this->database($account, 'budi_shop');
        $role = $account->postgresUsers()->make(['name' => 'budi_app']);
        $role->forceFill(['password' => 'OldPassword0123456789abcd', 'status' => DatabaseStatus::Active])->save();
        $role->databases()->attach($shop);

        $this->actingAs($account)
            ->post(route('postgres-users.rotate', $role))
            ->assertSessionHasNoErrors();

        $role->refresh();
        $this->assertNotSame('OldPassword0123456789abcd', $role->password);
        $this->assertSame(DatabaseStatus::Pending, $role->status);

        $task = Task::query()->where('action', 'pg.apply_user')->sole();
        $this->assertSame($role->password, $task->payload['password']);
        $this->assertSame(['budi_shop'], $task->payload['databases']);
    }

    public function test_a_database_can_be_deleted()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $database = $this->database($account, 'budi_shop');

        $this->actingAs($account)
            ->delete(route('postgres.destroy', $database))
            ->assertSessionHasNoErrors();

        $this->assertSame(DatabaseStatus::Deleting, $database->fresh()->status);
        $this->assertSame([
            'username' => 'budi',
            'database' => 'budi_shop',
        ], Task::query()->where('action', 'pg.delete_database')->sole()->payload);
    }

    public function test_another_accounts_database_cannot_be_deleted()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $database = $this->database(User::factory()->create(['username' => 'sari']), 'sari_shop');

        $this->actingAs($account)
            ->delete(route('postgres.destroy', $database))
            ->assertNotFound();

        $this->assertSame(DatabaseStatus::Active, $database->fresh()->status);
        $this->assertDatabaseCount('tasks', 0);
    }

    public function test_an_import_is_queued()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $database = $this->database($account, 'budi_shop');
        $role = PostgresUser::factory()->create(['user_id' => $account->id]);
        $database->users()->attach($role);

        $this->actingAs($account)
            ->post(route('postgres.import', $database), ['path' => 'tmp/rayxo-import-abcd1234.sql'])
            ->assertSessionHasNoErrors();

        $this->assertSame('running', $database->fresh()->import_status);
        $task = Task::query()->where('action', 'pg.import')->sole();
        $this->assertSame('budi_shop', $task->payload['database']);
        $this->assertTrue($task->payload['remove']);

        // The owning roles and their passwords are sent so the agent connects
        // as the database's owner (a NOSUPERUSER role), never the superuser.
        $this->assertSame([['name' => $role->name, 'password' => $role->password]], $task->payload['owners']);
    }

    public function test_an_import_into_a_database_without_a_user_is_refused()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $database = $this->database($account, 'budi_shop');

        // With no account role owning the database, the agent would have to load
        // the dump as the superuser; refuse rather than do that.
        $this->actingAs($account)
            ->post(route('postgres.import', $database), ['path' => 'tmp/rayxo-import-abcd1234.sql'])
            ->assertSessionHasErrors('file');

        $this->assertNull($database->fresh()->import_status);
        $this->assertDatabaseCount('tasks', 0);
    }
}
