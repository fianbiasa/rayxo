<?php

namespace Tests\Feature\Backups;

use App\Agent\AgentClient;
use App\Agent\AgentException;
use App\Enums\RemoteCopyStatus;
use App\Jobs\CopyBackupToRemote;
use App\Jobs\DeliverAlert;
use App\Jobs\FetchBackupFromRemote;
use App\Models\AuditLog;
use App\Models\NotificationChannel;
use App\Models\ServerBackup;
use App\Models\Setting;
use App\Models\SystemBackup;
use App\Models\User;
use App\Services\BackupService;
use App\Services\RemoteBackupService;
use App\Services\ServerBackupService;
use App\Services\SystemBackupService;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Queue;
use Inertia\Testing\AssertableInertia as Assert;
use Mockery\MockInterface;
use Tests\TestCase;

/**
 * Backups copied to a remote destination (FTP, SFTP or S3) and taken back
 * from it.
 */
class RemoteBackupTest extends TestCase
{
    use RefreshDatabase;

    private const HOST_KEY = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINpOPvVMXqP1ax8y6tKpUgt0+wywrxTez3mjT6wuickk';

    private User $admin;

    /**
     * @var list<array{0: string, 1: array<string, mixed>}>
     */
    private array $agentCalls = [];

    /**
     * @var array<string, mixed>
     */
    private array $answers = [];

    protected function setUp(): void
    {
        parent::setUp();

        Queue::fake();
        config(['app.url' => 'https://panel.example.com:2222']);

        $this->admin = User::factory()->admin()->create();

        $this->mock(AgentClient::class, function (MockInterface $mock): void {
            $mock->shouldReceive('call')->andReturnUsing(function (string $method, array $params = []): array {
                $this->agentCalls[] = [$method, $params];
                $answer = $this->answers[$method] ?? [];

                if ($answer instanceof AgentException) {
                    throw $answer;
                }

                return is_callable($answer) ? $answer($params) : $answer;
            });
        });
    }

    /**
     * @param  array<string, mixed>  $overrides
     */
    private function enableRemote(array $overrides = []): void
    {
        app(RemoteBackupService::class)->save([
            'enabled' => true,
            'type' => 'sftp',
            'host' => 'backup.example.com',
            'port' => null,
            'user' => 'rayxo',
            'password' => 'Remote-Secret-1',
            'host_key' => self::HOST_KEY,
            'path' => 'rayxo',
            'keep' => 2,
            'keep_local' => true,
            ...$overrides,
        ], $this->admin);
    }

    /**
     * @return list<array{0: string, 1: array<string, mixed>}>
     */
    private function calls(string $method): array
    {
        return array_values(array_filter($this->agentCalls, fn (array $call): bool => $call[0] === $method));
    }

    public function test_the_destination_is_saved_with_its_secrets_encrypted_and_never_shown()
    {
        $form = ['enabled' => true, 'type' => 'sftp', 'host' => 'backup.example.com', 'user' => 'rayxo', 'password' => 'Remote-Secret-1', 'host_key' => self::HOST_KEY, 'path' => 'rayxo', 'keep' => 7, 'keep_local' => true];

        $this->actingAs($this->admin)
            ->put(route('admin.backups.remote.update'), $form)
            ->assertSessionHasNoErrors()
            ->assertSessionHas('inertia.flash_data.toast.type', 'success');

        $stored = Setting::query()->where('key', RemoteBackupService::SETTING)->value('value');
        $this->assertNotSame('Remote-Secret-1', $stored['password']);
        $this->assertStringNotContainsString('Remote-Secret-1', json_encode($stored));

        [, $test] = $this->calls('backup.remote_test')[0];
        $this->assertSame(['sftp', 'Remote-Secret-1', self::HOST_KEY, 'panel.example.com'], [$test['destination']['type'], $test['destination']['password'], $test['destination']['host_key'], $test['server']]);

        // Left empty, the password stays; the page never gets it back.
        $this->actingAs($this->admin)->put(route('admin.backups.remote.update'), [...$form, 'password' => '', 'keep' => 9])->assertSessionHasNoErrors();
        $this->assertSame('Remote-Secret-1', $this->calls('backup.remote_test')[1][1]['destination']['password']);

        $this->actingAs($this->admin)
            ->get(route('admin.backups.index'))
            ->assertInertia(fn (Assert $page) => $page
                ->where('remote.has_password', true)
                ->where('remote.keep', 9)
                ->where('remote.host_key_fingerprint', 'SHA256:mZDgZZnet2aZmN6lh5lZwX1rB6zlXHjz6180HOuXBOc')
                ->missing('remote.password')
                ->missing('remote.secret_key'));

        $this->assertTrue(AuditLog::query()->where('action', 'backup.remote_changed')->exists());
    }

    public function test_a_failed_save_does_not_keep_the_secret_key_in_the_session()
    {
        $this->actingAs($this->admin)
            ->from(route('admin.backups.index'))
            ->put(route('admin.backups.remote.update'), [
                'enabled' => true, 'type' => 's3', 'bucket' => '',
                'access_key' => 'AKIA', 'secret_key' => 'top-secret-value', 'keep' => 7, 'keep_local' => true,
            ])
            ->assertSessionHasErrors();

        $this->assertArrayNotHasKey('secret_key', session('_old_input', []));
    }

    public function test_a_destination_is_checked_before_it_is_saved()
    {
        $sftp = ['enabled' => true, 'type' => 'sftp', 'host' => 'backup.example.com', 'user' => 'rayxo', 'password' => 'x', 'keep' => 7, 'keep_local' => true];

        $this->actingAs($this->admin)->put(route('admin.backups.remote.update'), $sftp)->assertSessionHasErrors('host_key');
        // Every key the server offered is accepted together.
        $this->actingAs($this->admin)->put(route('admin.backups.remote.update'), [...$sftp, 'host_key' => self::HOST_KEY."\nssh-rsa AAAAB3NzaC1yc2E="])->assertSessionHasNoErrors();
        $this->assertSame('SHA256:mZDgZZnet2aZmN6lh5lZwX1rB6zlXHjz6180HOuXBOc', app(RemoteBackupService::class)->settings()['host_key_fingerprint']);
        $this->actingAs($this->admin)->put(route('admin.backups.remote.update'), [...$sftp, 'host_key' => self::HOST_KEY, 'path' => '../etc'])->assertSessionHasErrors('path');

        $s3 = ['enabled' => true, 'type' => 's3', 'bucket' => 'my-backups', 'access_key' => 'AKIA', 'secret_key' => 'secret', 'keep' => 7, 'keep_local' => true];

        $this->actingAs($this->admin)->put(route('admin.backups.remote.update'), [...$s3, 'endpoint' => 'http://s3.example.com'])->assertSessionHasErrors('endpoint');
        $this->actingAs($this->admin)->put(route('admin.backups.remote.update'), [...$s3, 'endpoint' => 'http://127.0.0.1:9000'])->assertSessionHasNoErrors();
        $this->actingAs($this->admin)->put(route('admin.backups.remote.update'), [...$s3, 'secret_key' => '', 'bucket' => 'Bad_Bucket'])->assertSessionHasErrors('bucket');

    }

    public function test_moving_the_destination_elsewhere_forgets_the_copies_on_the_old_one()
    {
        $this->enableRemote();
        $both = ServerBackup::factory()->create(['remote_status' => RemoteCopyStatus::Uploaded]);
        $remoteOnly = ServerBackup::factory()->create(['on_server' => false, 'remote_status' => RemoteCopyStatus::Uploaded]);
        $system = SystemBackup::factory()->create(['remote_status' => RemoteCopyStatus::Failed, 'remote_error' => 'down']);

        $this->enableRemote(['keep' => 5]);
        $this->assertSame(RemoteCopyStatus::Uploaded, $both->refresh()->remote_status);

        $this->enableRemote(['path' => 'elsewhere']);

        $this->assertNull($both->refresh()->remote_status);
        $this->assertTrue($both->on_server);
        $this->assertModelMissing($remoteOnly);
        $this->assertSame([null, null], [$system->refresh()->remote_status, $system->remote_error]);
    }

    public function test_the_host_key_of_an_sftp_server_comes_from_the_server_for_the_admin_to_accept()
    {
        $this->answers['backup.remote_hostkey'] = ['key' => self::HOST_KEY, 'fingerprint' => 'SHA256:abc'];

        $this->actingAs($this->admin)
            ->post(route('admin.backups.remote.host-key'), ['host' => 'backup.example.com', 'port' => 2200])
            ->assertSessionHasNoErrors()
            ->assertSessionHas('inertia.flash_data.remote_host_key', ['key' => self::HOST_KEY, 'fingerprint' => 'SHA256:abc']);

        $this->assertSame(['host' => 'backup.example.com', 'port' => 2200], $this->calls('backup.remote_hostkey')[0][1]);
    }

    public function test_only_automatic_and_panel_backups_are_queued_for_the_destination()
    {
        $automatic = ServerBackup::factory()->create();
        $manual = ServerBackup::factory()->create(['backup_run_id' => null]);
        $remote = app(RemoteBackupService::class);

        $remote->queueCopy($automatic);
        Queue::assertNotPushed(CopyBackupToRemote::class);

        $this->enableRemote();

        foreach ([$automatic, $manual] as $backup) {
            $backup->forceFill(['status' => 'queued'])->save();
            $this->answers['backup.server_create'] = ['size' => 1];
            app(ServerBackupService::class)->create($backup, false);
        }

        $system = SystemBackup::factory()->create(['status' => 'queued']);
        $this->answers['backup.system_create'] = ['size' => 1];
        app(SystemBackupService::class)->create($system);

        Queue::assertPushed(CopyBackupToRemote::class, 2);
        Queue::assertPushed(CopyBackupToRemote::class, fn (CopyBackupToRemote $job): bool => $job->backup->is($automatic));
        Queue::assertPushed(CopyBackupToRemote::class, fn (CopyBackupToRemote $job): bool => $job->backup->is($system));
    }

    public function test_a_copy_goes_to_the_servers_folder_and_the_destination_keeps_the_newest()
    {
        $this->enableRemote();
        $account = User::factory()->create(['username' => 'budi']);
        $backups = ServerBackup::factory()->count(3)->for($account, 'account')->sequence(fn ($sequence) => ['name' => "budi-2026-11-1{$sequence->index}-020000.tar.gz"])->create();

        foreach ($backups as $backup) {
            app(RemoteBackupService::class)->copy($backup);
        }

        $upload = $this->calls('backup.remote_upload')[0][1];
        $this->assertSame(['panel.example.com', 'account', 'budi', 'budi-2026-11-10-020000.tar.gz', 'rayxo'], [$upload['server'], $upload['kind'], $upload['username'], $upload['name'], $upload['destination']['path']]);

        // Two kept there; the oldest copy went, and its backup is still on the server.
        $this->assertSame(['budi-2026-11-10-020000.tar.gz'], array_map(fn (array $call): string => $call[1]['name'], $this->calls('backup.remote_delete')));
        $this->assertSame([null, RemoteCopyStatus::Uploaded, RemoteCopyStatus::Uploaded], $backups->map(fn (ServerBackup $backup) => $backup->refresh()->remote_status)->all());
        $this->assertTrue($backups[0]->on_server);
    }

    public function test_a_copy_remembers_its_server_folder_for_after_a_restore_elsewhere()
    {
        $this->enableRemote();
        $account = User::factory()->create(['username' => 'budi']);
        $backup = ServerBackup::factory()->for($account, 'account')->create(['name' => 'budi-1.tar.gz']);

        app(RemoteBackupService::class)->copy($backup);
        $this->assertSame('panel.example.com', $backup->fresh()->remote_server);

        // The panel is restored on a server with a different hostname.
        config(['app.url' => 'https://newhost.example.net:2222']);

        // Deleting the copy still targets the folder it was uploaded under.
        app(RemoteBackupService::class)->deleteCopy($backup->fresh());
        $delete = $this->calls('backup.remote_delete');
        $this->assertSame('panel.example.com', end($delete)[1]['server']);
    }

    public function test_a_new_account_reusing_a_deleted_ones_name_does_not_rotate_its_remote_copies()
    {
        $this->enableRemote(['keep' => 1]);
        // A deleted account's copy: its row has no user_id but keeps the username.
        $deleted = ServerBackup::factory()->create([
            'user_id' => null, 'username' => 'budi', 'on_server' => false,
            'remote_status' => RemoteCopyStatus::Uploaded, 'name' => 'budi-2026-11-01-020000.tar.gz',
        ]);
        // A new account takes the same name; its backups are copied and rotated.
        $account = User::factory()->create(['username' => 'budi']);
        $backups = ServerBackup::factory()->count(2)->for($account, 'account')
            ->sequence(fn ($s) => ['name' => "budi-2026-11-1{$s->index}-020000.tar.gz"])->create();

        foreach ($backups as $backup) {
            app(RemoteBackupService::class)->copy($backup);
        }

        // keep=1 rotates the new account's older copy, but never the deleted
        // account's copy, which must stay until it is deleted by hand.
        $deletedNames = array_map(fn (array $call): string => $call[1]['name'], $this->calls('backup.remote_delete'));
        $this->assertNotContains('budi-2026-11-01-020000.tar.gz', $deletedNames);
        $this->assertSame(RemoteCopyStatus::Uploaded, $deleted->refresh()->remote_status);
    }

    public function test_without_a_copy_on_the_server_a_backup_lives_on_the_destination_until_it_rotates_out()
    {
        $this->enableRemote(['keep' => 1, 'keep_local' => false]);
        $account = User::factory()->create(['username' => 'budi']);
        [$older, $newer] = ServerBackup::factory()->count(2)->for($account, 'account')->create()->all();

        app(RemoteBackupService::class)->copy($older);

        $this->assertSame([[$older->name]], array_map(fn (array $call): array => $call[1]['names'], $this->calls('backup.server_delete')));
        $this->assertFalse($older->refresh()->on_server);

        // The server's rotation leaves it alone; the destination's drops it.
        app(ServerBackupService::class)->rotate();
        $this->assertModelExists($older);

        app(RemoteBackupService::class)->copy($newer);
        $this->assertModelMissing($older);
    }

    public function test_the_servers_rotation_keeps_backups_that_have_a_copy_on_the_destination()
    {
        $this->enableRemote();
        Setting::query()->create(['key' => ServerBackupService::SETTING, 'value' => [...ServerBackupService::DEFAULTS, 'keep' => 1]]);
        $account = User::factory()->create();
        [$plain, $older, $newer] = ServerBackup::factory()->count(3)->for($account, 'account')->create()->all();
        $older->forceFill(['remote_status' => RemoteCopyStatus::Uploaded])->save();

        app(ServerBackupService::class)->rotate();

        $this->assertFalse($older->refresh()->on_server);
        $this->assertTrue($newer->refresh()->on_server);
        $this->assertModelMissing($plain);
    }

    public function test_a_backup_only_on_the_destination_is_taken_back_before_it_is_restored()
    {
        $this->enableRemote();
        $backup = ServerBackup::factory()->create(['on_server' => false, 'remote_status' => RemoteCopyStatus::Uploaded]);

        $this->actingAs($this->admin)
            ->post(route('admin.backups.restore', $backup), ['files' => true])
            ->assertSessionHasErrors(['backup' => 'This backup is only on the remote destination; take it back to the server first.']);

        $this->actingAs($this->admin)->post(route('admin.backups.fetch', $backup))->assertSessionHasNoErrors();
        $this->assertSame(RemoteCopyStatus::Fetching, $backup->refresh()->remote_status);
        Queue::assertPushed(FetchBackupFromRemote::class, fn (FetchBackupFromRemote $job): bool => $job->backup?->is($backup) === true);

        app(RemoteBackupService::class)->fetch($backup);

        $this->assertSame([$backup->username, $backup->name], [$this->calls('backup.remote_download')[0][1]['username'], $this->calls('backup.remote_download')[0][1]['name']]);
        $this->assertTrue($backup->refresh()->on_server);
        $this->assertSame(RemoteCopyStatus::Uploaded, $backup->remote_status);
    }

    public function test_after_a_move_the_newest_backup_of_every_account_is_taken_back_at_once()
    {
        $this->enableRemote();
        $moved = User::factory()->create();
        $older = ServerBackup::factory()->for($moved, 'account')->create(['on_server' => false, 'remote_status' => RemoteCopyStatus::Uploaded]);
        $newest = ServerBackup::factory()->for($moved, 'account')->create(['on_server' => false, 'remote_status' => RemoteCopyStatus::Uploaded]);
        $here = User::factory()->create();
        ServerBackup::factory()->for($here, 'account')->create(['remote_status' => RemoteCopyStatus::Uploaded]);

        $this->actingAs($this->admin)
            ->post(route('admin.backups.remote.fetch-newest'))
            ->assertSessionHasNoErrors()
            ->assertSessionHas('inertia.flash_data.toast.type', 'success');

        Queue::assertPushed(FetchBackupFromRemote::class, 1);
        Queue::assertPushed(FetchBackupFromRemote::class, fn (FetchBackupFromRemote $job): bool => $job->backup?->is($newest) === true);
        $this->assertSame([RemoteCopyStatus::Uploaded, RemoteCopyStatus::Fetching], [$older->fresh()->remote_status, $newest->fresh()->remote_status]);
    }

    public function test_a_reseller_takes_back_only_backups_of_its_own_accounts_and_never_sets_the_destination()
    {
        $this->enableRemote();
        $reseller = User::factory()->reseller()->create();
        $own = ServerBackup::factory()->for(User::factory()->create(['owner_id' => $reseller->id]), 'account')->create(['on_server' => false, 'remote_status' => RemoteCopyStatus::Uploaded]);
        $other = ServerBackup::factory()->create(['on_server' => false, 'remote_status' => RemoteCopyStatus::Uploaded]);

        $this->actingAs($reseller)->post(route('reseller.backups.fetch', $own))->assertSessionHasNoErrors();
        $this->actingAs($reseller)->post(route('reseller.backups.fetch', $other))->assertNotFound();
        $this->actingAs($reseller)->put(route('admin.backups.remote.update'), ['enabled' => false])->assertForbidden();
    }

    public function test_a_failed_copy_tells_the_admins_and_keeps_the_server_copy()
    {
        $this->enableRemote(['keep_local' => false]);
        NotificationChannel::factory()->for($this->admin)->create();
        $this->answers['backup.remote_upload'] = new AgentException('Failed to copy: permission denied', reason: 'remote');
        $backup = ServerBackup::factory()->create();

        app(RemoteBackupService::class)->copy($backup);

        $this->assertSame([RemoteCopyStatus::Failed, 'Failed to copy: permission denied', true], [$backup->refresh()->remote_status, $backup->remote_error, $backup->on_server]);
        $this->assertSame([], $this->calls('backup.server_delete'));
        Queue::assertPushed(DeliverAlert::class, fn (DeliverAlert $job): bool => str_contains($job->subject, 'could not be copied'));
    }

    public function test_deleting_a_backup_deletes_its_copy_on_the_destination_first()
    {
        $this->enableRemote();
        $backup = ServerBackup::factory()->create(['remote_status' => RemoteCopyStatus::Uploaded]);

        $this->answers['backup.remote_delete'] = new AgentException('the destination is down', reason: 'remote');
        $this->actingAs($this->admin)->delete(route('admin.backups.destroy', $backup))->assertSessionHasErrors('backup');
        $this->assertModelExists($backup);

        $this->answers['backup.remote_delete'] = [];
        $this->agentCalls = [];
        $this->actingAs($this->admin)->delete(route('admin.backups.destroy', $backup))->assertSessionHasNoErrors();
        $this->assertModelMissing($backup);
        $this->assertSame(['backup.remote_delete', 'backup.server_delete'], array_column($this->agentCalls, 0));
    }

    public function test_backups_another_server_left_on_the_destination_can_be_browsed_and_taken()
    {
        $this->enableRemote();
        $this->answers['backup.remote_list'] = fn (array $params): array => $params['server'] === ''
            ? ['servers' => ['panel.example.com', 'old.example.com']]
            : ['accounts' => [['username' => 'budi', 'name' => 'budi-2026-11-01-020000.tar.gz', 'size' => 10, 'modified' => 1793500000]], 'system' => [['name' => 'rayxo-system-old.example.com-2026-11-01-030000.tar.gz', 'size' => 5, 'modified' => 1793500100]]];

        $this->actingAs($this->admin)
            ->get(route('admin.backups.index', ['browse' => 'old.example.com']))
            ->assertInertia(fn (Assert $page) => $page
                ->reloadOnly('remote_servers', fn (Assert $reload) => $reload->where('remote_servers', ['panel.example.com', 'old.example.com']))
                ->reloadOnly('remote_listing', fn (Assert $reload) => $reload
                    ->where('remote_listing.server', 'old.example.com')
                    ->where('remote_listing.accounts.0.username', 'budi')
                    ->where('remote_listing.accounts.0.known', false)
                    ->where('remote_listing.system.0.name', 'rayxo-system-old.example.com-2026-11-01-030000.tar.gz')));

        $file = ['server' => 'old.example.com', 'kind' => 'account', 'username' => 'budi', 'name' => 'budi-2026-11-01-020000.tar.gz'];
        $this->actingAs($this->admin)->post(route('admin.backups.remote.take'), $file)->assertSessionHasNoErrors();
        Queue::assertPushed(FetchBackupFromRemote::class, fn (FetchBackupFromRemote $job): bool => $job->file === $file);

        $this->answers['backup.remote_download'] = ['size' => 10, 'manifest' => json_encode(['format' => BackupService::FORMAT, 'username' => 'budi', 'files' => true, 'email' => false, 'databases' => []]), 'settings' => '{}'];
        app(RemoteBackupService::class)->take($file, $this->admin);

        // From another server's folder, it is a backup on this server only.
        $taken = ServerBackup::query()->where('name', 'budi-2026-11-01-020000.tar.gz')->sole();
        $this->assertSame([true, null, 'old.example.com'], [$taken->on_server, $taken->remote_status, $this->calls('backup.remote_download')[0][1]['server']]);

        $this->actingAs($this->admin)->post(route('admin.backups.remote.take'), $file)->assertSessionHasErrors(['backup' => 'This backup is known here already.']);
    }
}
