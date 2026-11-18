<?php

namespace Tests\Feature\Admin;

use App\Enums\Role;
use App\Models\User;
use App\Services\ServerSettingsService;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Tests\TestCase;

class SecurityPolicyTest extends TestCase
{
    use RefreshDatabase;

    public function test_an_admin_saves_the_security_policy()
    {
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->put(route('admin.server-settings.security-policy'), [
                'require_2fa' => ['reseller'],
                'password_min_length' => ['reseller' => 16],
            ])
            ->assertSessionHasNoErrors();

        $policy = app(ServerSettingsService::class)->securityPolicy();
        $this->assertSame(['reseller'], $policy['require_2fa']);
        $this->assertSame(16, $policy['password_min_length'][Role::Reseller->value]);
    }

    public function test_only_an_admin_sets_it_and_only_privileged_roles_can_be_required()
    {
        $this->actingAs(User::factory()->reseller()->create())
            ->put(route('admin.server-settings.security-policy'), ['require_2fa' => ['reseller']])
            ->assertForbidden();

        $this->actingAs(User::factory()->admin()->create())
            ->put(route('admin.server-settings.security-policy'), ['require_2fa' => ['user']])
            ->assertSessionHasErrors('require_2fa.0');
    }

    public function test_a_longer_minimum_is_enforced_when_creating_that_role()
    {
        $admin = User::factory()->admin()->create();
        app(ServerSettingsService::class)->setSecurityPolicy([], ['reseller' => 20], $admin);

        $this->actingAs($admin)
            ->post(route('admin.accounts.store'), $this->reseller('Satu-Dua-2026'))
            ->assertSessionHasErrors('password');

        $this->actingAs($admin)
            ->post(route('admin.accounts.store'), $this->reseller('Satu-Dua-Tiga-Empat-2026'))
            ->assertSessionHasNoErrors();
    }

    /**
     * @return array<string, string>
     */
    private function reseller(string $password): array
    {
        return [
            'username' => 'jual',
            'name' => 'Reseller Satu',
            'email' => 'jual@example.com',
            'role' => Role::Reseller->value,
            'password' => $password,
            'password_confirmation' => $password,
        ];
    }
}
