<?php

namespace Tests\Feature;

use App\Models\User;
use App\Services\LoginLinkService;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Inertia\Testing\AssertableInertia as Assert;
use Tests\TestCase;

class LoginLinkTest extends TestCase
{
    use RefreshDatabase;

    public function test_a_login_link_signs_an_account_in_when_nobody_is_signed_in()
    {
        $reseller = User::factory()->reseller()->create();
        $account = User::factory()->create(['owner_id' => $reseller->id]);
        $url = app(LoginLinkService::class)->create($account, $reseller)['url'];

        $this->get($url)->assertRedirect(route('dashboard'));
        $this->assertAuthenticatedAs($account, 'web');
    }

    public function test_a_login_link_asks_to_confirm_when_a_session_is_already_open()
    {
        $reseller = User::factory()->reseller()->create();
        $account = User::factory()->create(['owner_id' => $reseller->id]);
        $other = User::factory()->create();
        $url = app(LoginLinkService::class)->create($account, $reseller)['url'];

        // Already signed in: the GET must not swap the session on its own (that
        // would be a login CSRF); it shows a confirm page and leaves the link
        // unused.
        $this->actingAs($other, 'web')
            ->get($url)
            ->assertOk()
            ->assertInertia(fn (Assert $page) => $page->component('auth/login-link'));
        $this->assertAuthenticatedAs($other, 'web');

        // Confirming (a POST, which carries the session's CSRF token) swaps to
        // the account.
        $this->actingAs($other, 'web')
            ->post($url)
            ->assertRedirect(route('dashboard'));
        $this->assertAuthenticatedAs($account, 'web');
    }

    public function test_a_used_login_link_cannot_be_confirmed_again()
    {
        $reseller = User::factory()->reseller()->create();
        $account = User::factory()->create(['owner_id' => $reseller->id]);
        $other = User::factory()->create();
        $url = app(LoginLinkService::class)->create($account, $reseller)['url'];

        $this->get($url)->assertRedirect(route('dashboard'));

        $this->actingAs($other, 'web')
            ->post($url)
            ->assertRedirect(route('login'));
        $this->assertAuthenticatedAs($other, 'web');
    }
}
