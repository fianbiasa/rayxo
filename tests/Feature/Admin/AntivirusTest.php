<?php

namespace Tests\Feature\Admin;

use App\Agent\AgentClient;
use App\Agent\FakeAgentClient;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Tests\TestCase;

class AntivirusTest extends TestCase
{
    use RefreshDatabase;

    protected function setUp(): void
    {
        parent::setUp();

        $this->app->instance(AgentClient::class, new FakeAgentClient);
    }

    public function test_a_non_admin_cannot_view_the_antivirus_page(): void
    {
        $user = User::factory()->create();

        $this->actingAs($user)
            ->get(route('admin.antivirus.index'))
            ->assertForbidden();
    }

    public function test_admin_can_restore_a_quarantined_file(): void
    {
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->from(route('admin.antivirus.index'))
            ->post(route('admin.antivirus.quarantine.restore'), ['id' => 'a1b2c3d4'])
            ->assertRedirect(route('admin.antivirus.index'));
    }

    public function test_restore_requires_an_id(): void
    {
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->post(route('admin.antivirus.quarantine.restore'), [])
            ->assertSessionHasErrors('id');
    }

    public function test_a_non_admin_cannot_delete_from_quarantine(): void
    {
        $user = User::factory()->create();

        $this->actingAs($user)
            ->post(route('admin.antivirus.quarantine.delete'), ['id' => 'a1b2c3d4'])
            ->assertForbidden();
    }
}
