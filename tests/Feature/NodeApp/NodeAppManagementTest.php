<?php

namespace Tests\Feature\NodeApp;

use App\Enums\NodeAppStatus;
use App\Enums\TaskStatus;
use App\Events\AgentTaskFinished;
use App\Models\Domain;
use App\Models\NodeApp;
use App\Models\Task;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Queue;
use Inertia\Testing\AssertableInertia as Assert;
use Tests\TestCase;

class NodeAppManagementTest extends TestCase
{
    use RefreshDatabase;

    private User $account;

    private Domain $domain;

    protected function setUp(): void
    {
        parent::setUp();

        Queue::fake();

        $this->account = User::factory()->create(['username' => 'budi']);
        $this->domain = Domain::factory()->for($this->account, 'account')->create(['name' => 'example.com']);
    }

    private function enableApp(): NodeApp
    {
        $app = new NodeApp;
        $app->domain()->associate($this->domain);
        $app->forceFill([
            'entry' => '.output/server/index.mjs',
            'env' => ['DATABASE_URL' => 'mysql://u:p@127.0.0.1/db'],
            'port' => 40000,
            'enabled' => true,
            'status' => NodeAppStatus::Active,
        ])->save();

        return $app;
    }

    public function test_the_web_page_shows_the_node_app_off_by_default()
    {
        $this->actingAs($this->account)
            ->get(route('domains.web.show', $this->domain))
            ->assertOk()
            ->assertInertia(fn (Assert $page) => $page
                ->component('domains/web')
                ->where('node_app.enabled', false)
                ->where('node_app.status', null)
                ->where('node_app.env', []));
    }

    public function test_enabling_stores_the_app_allocates_a_port_and_queues_provision_and_reapply()
    {
        $this->actingAs($this->account)
            ->post(route('domains.node-app.store', $this->domain), [
                'entry' => '.output/server/index.mjs',
                'env' => [['name' => 'APP_KEY', 'value' => 'secret']],
            ])
            ->assertRedirect();

        $app = $this->domain->nodeApp()->sole();
        $this->assertTrue($app->enabled);
        $this->assertSame('.output/server/index.mjs', $app->entry);
        $this->assertSame(['APP_KEY' => 'secret'], $app->env);
        $this->assertGreaterThanOrEqual(NodeApp::PORT_MIN, $app->port);
        $this->assertLessThanOrEqual(NodeApp::PORT_MAX, $app->port);
        $this->assertSame(NodeAppStatus::Pending, $app->status);

        $provision = Task::query()->where('action', 'nodeapp.provision')->sole();
        $this->assertSame('example.com', $provision->payload['domain']);
        $this->assertSame($app->port, $provision->payload['port']);
        $this->assertSame(['APP_KEY' => 'secret'], $provision->payload['env']);

        $this->assertDatabaseHas('tasks', ['action' => 'web.apply_domain', 'user_id' => $this->account->id]);
    }

    public function test_enabling_gives_each_app_a_distinct_port()
    {
        $this->enableApp();
        $other = Domain::factory()->for($this->account, 'account')->create(['name' => 'second.com']);

        $this->actingAs($this->account)
            ->post(route('domains.node-app.store', $other), [
                'entry' => 'index.mjs',
                'env' => [],
            ])
            ->assertRedirect();

        $this->assertNotSame(40000, $other->nodeApp()->sole()->port);
    }

    public function test_enabling_rejects_an_entry_outside_the_app_folder()
    {
        $this->actingAs($this->account)
            ->post(route('domains.node-app.store', $this->domain), [
                'entry' => '../../etc/passwd',
                'env' => [],
            ])
            ->assertSessionHasErrors('entry');

        $this->assertDatabaseCount('node_apps', 0);
    }

    public function test_enabling_rejects_an_entry_with_whitespace_or_a_leading_dash()
    {
        // A space, a tab or a leading dash would let the entry smuggle extra
        // node options (e.g. "-e <code>") into the systemd ExecStart; the agent
        // rejects the same, this keeps it out of the UI.
        foreach (['-e console.log(1)', 'server.mjs --inspect', "a\tb.mjs"] as $entry) {
            $this->actingAs($this->account)
                ->post(route('domains.node-app.store', $this->domain), [
                    'entry' => $entry,
                    'env' => [],
                ])
                ->assertSessionHasErrors('entry');
        }

        $this->assertDatabaseCount('node_apps', 0);
    }

    public function test_enabling_rejects_a_bad_environment_name()
    {
        $this->actingAs($this->account)
            ->post(route('domains.node-app.store', $this->domain), [
                'entry' => 'index.mjs',
                'env' => [['name' => '1BAD NAME', 'value' => 'x']],
            ])
            ->assertSessionHasErrors('env.0.name');

        $this->assertDatabaseCount('node_apps', 0);
    }

    public function test_updating_changes_the_entry_and_queues_provision()
    {
        $this->enableApp();

        $this->actingAs($this->account)
            ->put(route('domains.node-app.update', $this->domain), [
                'entry' => 'server.mjs',
                'env' => [['name' => 'FOO', 'value' => 'bar']],
            ])
            ->assertRedirect();

        $app = $this->domain->nodeApp()->sole();
        $this->assertSame('server.mjs', $app->entry);
        $this->assertSame(['FOO' => 'bar'], $app->env);
        $this->assertSame(NodeAppStatus::Pending, $app->status);
        $this->assertDatabaseHas('tasks', ['action' => 'nodeapp.provision']);
    }

    public function test_disabling_queues_deprovision_and_reapply_and_removes_the_record()
    {
        $this->enableApp();

        $this->actingAs($this->account)
            ->delete(route('domains.node-app.destroy', $this->domain))
            ->assertRedirect();

        $this->assertDatabaseCount('node_apps', 0);
        $this->assertDatabaseHas('tasks', ['action' => 'nodeapp.deprovision', 'user_id' => $this->account->id]);
        $this->assertDatabaseHas('tasks', ['action' => 'web.apply_domain', 'user_id' => $this->account->id]);
    }

    public function test_restarting_queues_a_restart()
    {
        $this->enableApp();

        $this->actingAs($this->account)
            ->post(route('domains.node-app.restart', $this->domain))
            ->assertRedirect();

        $this->assertDatabaseHas('tasks', ['action' => 'nodeapp.restart', 'user_id' => $this->account->id]);
    }

    public function test_another_account_cannot_touch_the_app()
    {
        $this->enableApp();
        $other = User::factory()->create(['username' => 'siti']);

        $this->actingAs($other)
            ->post(route('domains.node-app.restart', $this->domain))
            ->assertNotFound();
    }

    public function test_a_successful_provision_marks_the_app_active()
    {
        $app = $this->enableApp();
        $app->forceFill(['status' => NodeAppStatus::Pending])->save();

        event(new AgentTaskFinished($this->provisionTask(TaskStatus::Success)));

        $this->assertSame(NodeAppStatus::Active, $this->domain->nodeApp()->sole()->status);
    }

    public function test_a_failed_provision_marks_the_app_failed()
    {
        $app = $this->enableApp();
        $app->forceFill(['status' => NodeAppStatus::Pending])->save();

        event(new AgentTaskFinished($this->provisionTask(TaskStatus::Failed)));

        $this->assertSame(NodeAppStatus::Failed, $this->domain->nodeApp()->sole()->status);
    }

    private function provisionTask(TaskStatus $status): Task
    {
        return Task::create([
            'user_id' => $this->account->id,
            'actor_id' => $this->account->id,
            'action' => 'nodeapp.provision',
            'payload' => ['username' => 'budi', 'domain' => 'example.com'],
            'status' => $status,
        ]);
    }
}
