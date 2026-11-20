<?php

namespace Database\Factories;

use App\Enums\DatabaseStatus;
use App\Models\PostgresUser;
use App\Models\User;
use Illuminate\Database\Eloquent\Factories\Factory;

/**
 * @extends Factory<PostgresUser>
 */
class PostgresUserFactory extends Factory
{
    /**
     * Define the model's default state: a generated password.
     *
     * @return array<string, mixed>
     */
    public function definition(): array
    {
        return [
            'user_id' => User::factory(),
            'name' => fn (array $attributes) => User::query()->whereKey($attributes['user_id'])->firstOrFail()->username.'_'.fake()->unique()->lexify('r????'),
            'password' => PostgresUser::newPassword(),
            'status' => DatabaseStatus::Active,
        ];
    }

    /**
     * Indicate that the agent has not set the role up yet.
     */
    public function pending(): static
    {
        return $this->state(fn (array $attributes) => ['status' => DatabaseStatus::Pending]);
    }
}
