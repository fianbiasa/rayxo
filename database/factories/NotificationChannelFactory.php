<?php

namespace Database\Factories;

use App\Models\NotificationChannel;
use App\Models\User;
use App\Support\NotificationEvents;
use Illuminate\Database\Eloquent\Factories\Factory;

/**
 * @extends Factory<NotificationChannel>
 */
class NotificationChannelFactory extends Factory
{
    /**
     * Define the model's default state: a verified email channel for every
     * alert. A channel is created verified so alert tests deliver; use
     * unverified() for the challenge flow.
     *
     * @return array<string, mixed>
     */
    public function definition(): array
    {
        return [
            'user_id' => User::factory(),
            'type' => 'email',
            'target' => fake()->safeEmail(),
            'events' => array_keys(NotificationEvents::ALL),
            'enabled' => true,
            'verified_at' => now(),
        ];
    }

    public function telegram(string $chatId = '123456789'): static
    {
        return $this->state(fn (): array => ['type' => 'telegram', 'target' => $chatId]);
    }

    /**
     * A channel whose target has not confirmed its code yet.
     */
    public function unverified(): static
    {
        return $this->state(fn (): array => ['verified_at' => null]);
    }
}
