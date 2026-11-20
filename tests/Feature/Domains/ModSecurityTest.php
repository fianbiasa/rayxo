<?php

namespace Tests\Feature\Domains;

use App\Enums\ModSecurityMode;
use App\Models\AuditLog;
use App\Models\Domain;
use App\Models\Package;
use App\Models\Task;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Queue;
use Inertia\Testing\AssertableInertia as Assert;
use Tests\TestCase;

class ModSecurityTest extends TestCase
{
    use RefreshDatabase;

    private User $admin;

    private User $account;

    private Domain $domain;

    protected function setUp(): void
    {
        parent::setUp();

        Queue::fake();

        $this->admin = User::factory()->admin()->create();
        $package = Package::factory()->for($this->admin, 'owner')->create();
        $this->account = User::factory()->create(['username' => 'budi', 'owner_id' => $this->admin->id, 'package_id' => $package->id]);
        $this->domain = Domain::factory()->for($this->account, 'account')->create(['name' => 'example.com']);
    }

    public function test_a_website_inherits_the_server_mode_by_default()
    {
        $this->assertSame(ModSecurityMode::Inherit, $this->domain->modsecurity_mode);

        $this->actingAs($this->account)
            ->get(route('domains.web.show', $this->domain))
            ->assertInertia(fn (Assert $page) => $page->where('modsecurity', ['mode' => 'inherit', 'global' => 'off']));
    }

    public function test_an_override_is_applied_to_the_website_and_its_aliases()
    {
        Domain::factory()->pointerOf($this->domain)->create(['name' => 'example.net']);

        $this->actingAs($this->account)
            ->put(route('domains.modsecurity.update', $this->domain), ['mode' => 'on'])
            ->assertSessionHasNoErrors();

        $this->assertSame(ModSecurityMode::On, $this->domain->fresh()->modsecurity_mode);

        $payloads = Task::query()->where('action', 'web.apply_domain')->orderBy('id')->pluck('payload');
        $this->assertSame(['example.com', 'example.net'], $payloads->pluck('domain')->all());
        $this->assertSame('on', $payloads[0]['modsecurity']);
        $this->assertSame('on', $payloads[1]['modsecurity']);
        $this->assertDatabaseHas(AuditLog::class, ['action' => 'domain.modsecurity_changed']);
    }

    public function test_going_back_to_inherit_sends_no_override()
    {
        $this->domain->forceFill(['modsecurity_mode' => ModSecurityMode::Detection])->save();

        $this->actingAs($this->account)
            ->put(route('domains.modsecurity.update', $this->domain), ['mode' => 'inherit'])
            ->assertSessionHasNoErrors();

        $this->assertSame(ModSecurityMode::Inherit, $this->domain->fresh()->modsecurity_mode);
        $this->assertArrayNotHasKey('modsecurity', Task::query()->where('action', 'web.apply_domain')->latest('id')->first()->payload);
    }

    public function test_an_unknown_mode_is_rejected()
    {
        $this->actingAs($this->account)
            ->put(route('domains.modsecurity.update', $this->domain), ['mode' => 'paranoid'])
            ->assertSessionHasErrors(['mode']);

        $this->assertSame(ModSecurityMode::Inherit, $this->domain->fresh()->modsecurity_mode);
        $this->assertDatabaseCount('tasks', 0);
    }

    public function test_another_account_cannot_change_a_domains_waf()
    {
        $other = User::factory()->create(['username' => 'sari', 'owner_id' => $this->admin->id]);

        $this->actingAs($other)
            ->put(route('domains.modsecurity.update', $this->domain), ['mode' => 'off'])
            ->assertNotFound();

        $this->assertSame(ModSecurityMode::Inherit, $this->domain->fresh()->modsecurity_mode);
        $this->assertDatabaseCount('tasks', 0);
    }
}
