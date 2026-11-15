<?php

namespace Tests\Feature\Api;

use App\Enums\AccountStatus;
use App\Enums\Role;
use App\Models\AuditLog;
use App\Models\BandwidthUsage;
use App\Models\LoginKey;
use App\Models\Package;
use App\Models\Task;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Hash;
use Illuminate\Support\Facades\Queue;
use Tests\TestCase;

class ApiAccountTest extends TestCase
{
    use RefreshDatabase;

    private const ADMIN_KEY = 'rxk_admin6789abcdefghijABCDEFGHIJ0123456789';

    private const RESELLER_KEY = 'rxk_resel6789abcdefghijABCDEFGHIJ0123456789';

    private User $admin;

    private User $reseller;

    private Package $package;

    protected function setUp(): void
    {
        parent::setUp();

        Queue::fake();

        $this->admin = User::factory()->admin()->create();
        $this->reseller = User::factory()->reseller()->create();
        $this->package = Package::factory()->for($this->reseller, 'owner')->create(['name' => 'Starter']);

        $all = ['accounts:read', 'accounts:write', 'accounts:login', 'packages:read'];
        LoginKey::factory()->for($this->admin)->withSecret(self::ADMIN_KEY)->create(['permissions' => $all]);
        LoginKey::factory()->for($this->reseller)->withSecret(self::RESELLER_KEY)->create(['permissions' => $all]);
    }

    public function test_hosting_accounts_are_listed_for_whoever_manages_them()
    {
        User::factory()->create(['username' => 'budi', 'owner_id' => $this->reseller->id]);
        User::factory()->create(['username' => 'sari', 'owner_id' => $this->admin->id]);
        User::factory()->create(['username' => 'tono', 'owner_id' => $this->admin->id, 'status' => AccountStatus::Suspended]);

        $this->withToken(self::ADMIN_KEY)->getJson('/api/v1/accounts')
            ->assertOk()
            ->assertJsonPath('meta.total', 3)
            ->assertJsonPath('data.*.username', ['budi', 'sari', 'tono']);

        $this->withToken(self::ADMIN_KEY)->getJson('/api/v1/accounts?status=suspended')->assertJsonPath('data.*.username', ['tono']);

        $this->withToken(self::RESELLER_KEY)->getJson('/api/v1/accounts')->assertJsonPath('data.*.username', ['budi']);
        $this->withToken(self::RESELLER_KEY)->getJson('/api/v1/accounts/sari')->assertNotFound();
        $this->withToken(self::ADMIN_KEY)->getJson('/api/v1/accounts/'.$this->reseller->username)->assertNotFound();
    }

    public function test_a_key_only_ever_makes_hosting_accounts()
    {
        $this->withToken(self::RESELLER_KEY)
            ->postJson('/api/v1/accounts', [
                'username' => 'budi',
                'name' => 'Budi',
                'email' => 'budi@example.com',
                'password' => 'a-long-password-123',
                'domain' => 'budi.example',
                'package_id' => $this->package->id,
                'role' => 'admin',
            ])
            ->assertCreated()
            ->assertJsonPath('data.username', 'budi')
            ->assertJsonPath('data.status', 'pending')
            ->assertJsonPath('data.package.name', 'Starter');

        $account = User::query()->where('username', 'budi')->sole();
        $this->assertSame(Role::User, $account->role);
        $this->assertSame($this->reseller->id, $account->owner_id);
        $this->assertTrue(Task::query()->where('action', 'account.create')->exists());

        $this->withToken(self::RESELLER_KEY)
            ->postJson('/api/v1/accounts', ['username' => 'budi', 'name' => 'B', 'email' => 'b@example.com', 'password' => 'a-long-password-123'])
            ->assertUnprocessable()
            ->assertJsonValidationErrors('username');
    }

    public function test_a_billing_client_with_two_services_gets_two_accounts_with_its_email_address()
    {
        foreach (['budi', 'budi2'] as $username) {
            $this->withToken(self::RESELLER_KEY)
                ->postJson('/api/v1/accounts', ['username' => $username, 'name' => 'Budi', 'email' => 'budi@example.com', 'password' => 'a-long-password-123'])
                ->assertCreated();
        }

        $this->assertSame(2, User::query()->where('email', 'budi@example.com')->count());
    }

    public function test_an_account_is_suspended_moved_and_deleted()
    {
        User::factory()->create(['username' => 'budi', 'owner_id' => $this->reseller->id]);

        $this->withToken(self::RESELLER_KEY)->postJson('/api/v1/accounts/budi/suspend', ['reason' => 'Unpaid'])
            ->assertOk()
            ->assertJsonPath('data.status', 'suspended')
            ->assertJsonPath('data.suspend_reason', 'Unpaid');

        $this->withToken(self::RESELLER_KEY)->postJson('/api/v1/accounts/budi/unsuspend')->assertOk()->assertJsonPath('data.status', 'active');

        $this->withToken(self::RESELLER_KEY)->putJson('/api/v1/accounts/budi/package', ['package_id' => $this->package->id])
            ->assertOk()
            ->assertJsonPath('data.package.id', $this->package->id);

        $this->withToken(self::RESELLER_KEY)->deleteJson('/api/v1/accounts/budi')->assertStatus(202)->assertJsonPath('data.status', 'deleting');

        $this->assertSame(
            ['account.suspend', 'account.unsuspend', 'account.delete'],
            Task::query()->whereIn('action', ['account.suspend', 'account.unsuspend', 'account.delete'])->orderBy('id')->pluck('action')->all(),
        );
    }

    public function test_reading_needs_only_the_read_permission()
    {
        LoginKey::query()->where('user_id', $this->reseller->id)->update(['permissions' => json_encode(['accounts:read'])]);
        User::factory()->create(['username' => 'budi', 'owner_id' => $this->reseller->id]);

        $this->withToken(self::RESELLER_KEY)->getJson('/api/v1/accounts/budi')->assertOk();
        $this->withToken(self::RESELLER_KEY)->postJson('/api/v1/accounts/budi/suspend')->assertForbidden();
        $this->withToken(self::RESELLER_KEY)->getJson('/api/v1/packages')->assertForbidden();
    }

    public function test_packages_are_the_ones_the_key_can_hand_out()
    {
        Package::factory()->for($this->admin, 'owner')->create(['name' => 'Admin only']);

        $this->withToken(self::RESELLER_KEY)->getJson('/api/v1/packages')
            ->assertOk()
            ->assertJsonPath('data.*.name', ['Starter'])
            ->assertJsonPath('data.0.ssl', true)
            ->assertJsonPath('data.0.resources', ['cpu_percent' => null, 'memory_mb' => null, 'max_processes' => null, 'io_weight' => null]);
    }

    public function test_a_billing_system_gives_an_account_a_new_password()
    {
        $budi = User::factory()->create(['username' => 'budi', 'owner_id' => $this->reseller->id]);

        $this->withToken(self::RESELLER_KEY)->putJson('/api/v1/accounts/budi/password', ['password' => 'short'])->assertUnprocessable()->assertJsonValidationErrors('password');
        $this->withToken(self::RESELLER_KEY)->putJson('/api/v1/accounts/budi/password', ['password' => 'Kata-Sandi-Baru-2026'])->assertOk()->assertJsonPath('data.username', 'budi');

        $this->assertTrue(Hash::check('Kata-Sandi-Baru-2026', $budi->refresh()->password));
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'account.password_changed', 'actor_id' => $this->reseller->id]);

        User::factory()->create(['username' => 'sari', 'owner_id' => $this->admin->id]);
        $this->withToken(self::RESELLER_KEY)->putJson('/api/v1/accounts/sari/password', ['password' => 'Kata-Sandi-Baru-2026'])->assertNotFound();
    }

    public function test_a_login_link_signs_the_account_in_once_within_a_minute()
    {
        $budi = User::factory()->create(['username' => 'budi', 'owner_id' => $this->reseller->id]);

        $url = $this->withToken(self::RESELLER_KEY)->postJson('/api/v1/accounts/budi/login-link')
            ->assertCreated()
            ->json('data.url');

        $this->assertMatchesRegularExpression('#/login-link/[A-Za-z0-9]{64}$#', $url);
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'account.login_link_created', 'actor_id' => $this->reseller->id]);

        $this->get($url)->assertRedirect(route('dashboard'));
        $this->assertAuthenticatedAs($budi, 'web');

        // It works once.
        auth()->guard('web')->logout();
        $this->get($url)->assertRedirect(route('login'));
        $this->assertGuest('web');

        // And within a minute.
        $late = $this->withToken(self::RESELLER_KEY)->postJson('/api/v1/accounts/budi/login-link')->json('data.url');
        $this->travel(61)->seconds();
        $this->get($late)->assertRedirect(route('login'));
        $this->assertGuest('web');
    }

    public function test_a_login_link_needs_its_own_permission_and_an_account_of_the_key()
    {
        User::factory()->create(['username' => 'sari', 'owner_id' => $this->admin->id]);
        $this->withToken(self::RESELLER_KEY)->postJson('/api/v1/accounts/sari/login-link')->assertNotFound();
        $this->withToken(self::ADMIN_KEY)->postJson('/api/v1/accounts/'.$this->reseller->username.'/login-link')->assertNotFound();

        $limited = 'rxk_limit6789abcdefghijABCDEFGHIJ0123456789';
        LoginKey::factory()->for($this->admin)->withSecret($limited)->create(['permissions' => ['accounts:read', 'accounts:write']]);
        $this->withToken($limited)->postJson('/api/v1/accounts/sari/login-link')->assertForbidden();
    }

    public function test_usage_lists_disk_and_this_months_bandwidth_of_every_account_of_the_key()
    {
        $budi = User::factory()->create(['username' => 'budi', 'owner_id' => $this->reseller->id, 'package_id' => $this->package->id]);
        $this->package->forceFill(['disk_mb' => 1000, 'bandwidth_mb' => 5000])->save();
        $budi->diskUsage()->create(['disk_kb' => 300 * 1024, 'inodes' => 10, 'measured_at' => now()]);
        BandwidthUsage::query()->forceCreate(['user_id' => $budi->id, 'date' => now()->toDateString(), 'web_bytes' => 100 * 1048576, 'mail_bytes' => 20 * 1048576, 'ftp_bytes' => 0]);
        BandwidthUsage::query()->forceCreate(['user_id' => $budi->id, 'date' => now()->subMonthNoOverflow()->startOfMonth()->toDateString(), 'web_bytes' => 999 * 1048576, 'mail_bytes' => 0, 'ftp_bytes' => 0]);
        User::factory()->create(['username' => 'sari', 'owner_id' => $this->admin->id]);

        $this->withToken(self::RESELLER_KEY)->getJson('/api/v1/usage')
            ->assertOk()
            ->assertExactJson(['data' => [[
                'username' => 'budi',
                'status' => 'active',
                'disk_mb' => 300,
                'disk_limit_mb' => 1000,
                'bandwidth_mb' => 120,
                'bandwidth_limit_mb' => 5000,
            ]]]);

        $this->withToken(self::ADMIN_KEY)->getJson('/api/v1/usage')->assertJsonPath('data.*.username', ['budi', 'sari']);
    }
}
