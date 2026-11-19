<?php

namespace Tests\Feature\Redis;

use App\Models\RedisInstance;
use App\Models\User;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Queue;
use Inertia\Testing\AssertableInertia as Assert;
use Tests\TestCase;

class RedisManagementTest extends TestCase
{
    use RefreshDatabase;

    protected function setUp(): void
    {
        parent::setUp();

        Queue::fake();
    }

    private function enableFor(User $account): RedisInstance
    {
        $instance = $account->redisInstance()->make(['max_memory_mb' => 128]);
        $instance->password = 'abcdEFGH12345678wxyz';
        $instance->enabled = true;
        $instance->save();

        return $instance;
    }

    public function test_admins_cannot_reach_redis()
    {
        $this->actingAs(User::factory()->admin()->create())
            ->get(route('redis.index'))
            ->assertForbidden();
    }

    public function test_the_page_shows_redis_off_by_default()
    {
        $account = User::factory()->create();

        $this->actingAs($account)
            ->get(route('redis.index'))
            ->assertOk()
            ->assertInertia(fn (Assert $page) => $page
                ->component('redis/index')
                ->where('enabled', false)
                ->where('connection', null));
    }

    public function test_enabling_redis_stores_it_and_queues_provision()
    {
        $account = User::factory()->create(['username' => 'budi']);

        $this->actingAs($account)
            ->post(route('redis.store'), ['max_memory_mb' => 512])
            ->assertRedirect(route('redis.index'));

        $this->assertDatabaseHas('redis_instances', [
            'user_id' => $account->id,
            'enabled' => true,
            'max_memory_mb' => 512,
        ]);
        $this->assertDatabaseHas('tasks', [
            'action' => 'redis.provision',
            'user_id' => $account->id,
        ]);
    }

    public function test_the_page_shows_connection_details_when_enabled()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $this->enableFor($account);

        $this->actingAs($account)
            ->get(route('redis.index'))
            ->assertOk()
            ->assertInertia(fn (Assert $page) => $page
                ->component('redis/index')
                ->where('enabled', true)
                ->where('connection.host', '/run/rayxo-redis/budi/redis.sock')
                ->where('connection.port', 0)
                ->where('connection.prefix', 'budi:')
                ->where('connection.client', 'phpredis')
                ->where('connection.password', 'abcdEFGH12345678wxyz'));
    }

    public function test_disabling_redis_queues_deprovision()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $this->enableFor($account);

        $this->actingAs($account)
            ->delete(route('redis.destroy'))
            ->assertRedirect();

        $this->assertDatabaseHas('redis_instances', [
            'user_id' => $account->id,
            'enabled' => false,
        ]);
        $this->assertDatabaseHas('tasks', [
            'action' => 'redis.deprovision',
            'user_id' => $account->id,
        ]);
    }

    public function test_rotating_the_password_changes_it_and_queues_provision()
    {
        $account = User::factory()->create(['username' => 'budi']);
        $old = $this->enableFor($account)->password;

        $this->actingAs($account)
            ->post(route('redis.rotate'))
            ->assertRedirect();

        $this->assertNotSame($old, $account->redisInstance()->first()->password);
        $this->assertDatabaseHas('tasks', [
            'action' => 'redis.provision',
            'user_id' => $account->id,
        ]);
    }
}
