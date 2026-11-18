<?php

namespace Tests\Feature;

use App\Models\User;
use App\Services\ImpersonationService;
use App\Services\ServerSettingsService;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Tests\TestCase;

class RequireTwoFactorTest extends TestCase
{
    use RefreshDatabase;

    private function require2fa(array $roles): void
    {
        app(ServerSettingsService::class)->setSecurityPolicy($roles, [], User::factory()->admin()->create());
    }

    private function withTwoFactor(User $user): User
    {
        $user->forceFill(['two_factor_secret' => 'secret', 'two_factor_confirmed_at' => now()])->save();

        return $user;
    }

    public function test_a_required_role_without_2fa_is_sent_to_set_it_up()
    {
        $this->require2fa(['reseller']);

        $this->actingAs(User::factory()->reseller()->create())
            ->get(route('dashboard'))
            ->assertRedirect(route('security.edit'));
    }

    public function test_the_security_page_and_sign_out_stay_reachable()
    {
        $this->require2fa(['reseller']);
        $reseller = User::factory()->reseller()->create();

        // The security page sits behind Fortify's password gate, so it goes to
        // password.confirm — not a RequireTwoFactor loop back to itself, which
        // proves the page is exempt.
        $this->actingAs($reseller)
            ->get(route('security.edit'))
            ->assertRedirect(route('password.confirm'));

        // With the password just confirmed, the security page itself opens.
        $this->actingAs($reseller)
            ->withSession(['auth.password_confirmed_at' => now()->timestamp])
            ->get(route('security.edit'))
            ->assertOk();

        $this->actingAs($reseller)->post(route('logout'))->assertRedirect();
    }

    public function test_a_required_role_with_2fa_passes()
    {
        $this->require2fa(['reseller']);
        $reseller = $this->withTwoFactor(User::factory()->reseller()->create());

        $this->actingAs($reseller)->get(route('dashboard'))->assertOk();
    }

    public function test_a_role_that_is_not_required_passes_without_2fa()
    {
        $this->require2fa(['admin']);

        $this->actingAs(User::factory()->reseller()->create())
            ->get(route('dashboard'))
            ->assertOk();
    }

    public function test_nobody_is_affected_when_no_role_is_required()
    {
        $this->actingAs(User::factory()->admin()->create())
            ->get(route('dashboard'))
            ->assertOk();
    }

    public function test_an_impersonated_account_is_not_forced_to_set_up_2fa()
    {
        $this->require2fa(['reseller']);
        $admin = $this->withTwoFactor(User::factory()->admin()->create());
        $reseller = User::factory()->reseller()->create();

        $this->actingAs($reseller)
            ->withSession([ImpersonationService::SESSION_KEY => $admin->id])
            ->get(route('dashboard'))
            ->assertOk();
    }
}
