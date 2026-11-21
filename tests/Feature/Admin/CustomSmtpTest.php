<?php

namespace Tests\Feature\Admin;

use App\Models\Setting;
use App\Models\User;
use App\Services\ServerSettingsService;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\Mail;
use Tests\TestCase;

class CustomSmtpTest extends TestCase
{
    use RefreshDatabase;

    /**
     * @param  array<string, mixed>  $overrides
     * @return array<string, mixed>
     */
    private function payload(array $overrides = []): array
    {
        return array_merge([
            'enabled' => true,
            'host' => 'smtp.example.com',
            'port' => 587,
            'encryption' => 'tls',
            'username' => 'mailer@example.com',
            'password' => 'secret-pass',
            'from_address' => 'panel@example.com',
            'from_name' => 'RAYXO Panel',
        ], $overrides);
    }

    public function test_an_admin_saves_custom_smtp_and_the_password_is_stored_encrypted()
    {
        $admin = User::factory()->admin()->create();

        $this->actingAs($admin)
            ->put(route('admin.server-settings.custom-smtp'), $this->payload())
            ->assertSessionHasNoErrors();

        $state = app(ServerSettingsService::class)->customSmtp();
        $this->assertTrue($state['enabled']);
        $this->assertSame('smtp.example.com', $state['host']);
        $this->assertTrue($state['password_set']);
        $this->assertArrayNotHasKey('password', $state);

        $raw = Setting::query()->where('key', ServerSettingsService::CUSTOM_SMTP)->value('value');
        $this->assertNotSame('secret-pass', $raw['password']);
        $this->assertSame('secret-pass', Crypt::decryptString($raw['password']));
    }

    public function test_enabling_points_the_mailer_at_the_custom_smtp_server()
    {
        $admin = User::factory()->admin()->create();
        app(ServerSettingsService::class)->setCustomSmtp($this->payload(['encryption' => 'ssl', 'port' => 465]), $admin);

        app(ServerSettingsService::class)->applyMailConfig();

        $this->assertSame('smtp', config('mail.default'));
        $this->assertSame('smtp.example.com', config('mail.mailers.smtp.host'));
        $this->assertSame(465, config('mail.mailers.smtp.port'));
        $this->assertSame('smtps', config('mail.mailers.smtp.scheme'));
        $this->assertSame('secret-pass', config('mail.mailers.smtp.password'));
        $this->assertSame('panel@example.com', config('mail.from.address'));
    }

    public function test_disabled_custom_smtp_leaves_the_built_in_mailer()
    {
        $admin = User::factory()->admin()->create();
        app(ServerSettingsService::class)->setCustomSmtp($this->payload(['enabled' => false]), $admin);

        config(['mail.default' => 'log', 'mail.mailers.smtp.host' => '127.0.0.1']);
        app(ServerSettingsService::class)->applyMailConfig();

        $this->assertSame('log', config('mail.default'));
        $this->assertSame('127.0.0.1', config('mail.mailers.smtp.host'));
    }

    public function test_a_blank_password_keeps_the_stored_one()
    {
        $admin = User::factory()->admin()->create();
        $settings = app(ServerSettingsService::class);
        $settings->setCustomSmtp($this->payload(), $admin);

        $settings->setCustomSmtp($this->payload(['password' => '', 'host' => 'smtp2.example.com']), $admin);

        $raw = Setting::query()->where('key', ServerSettingsService::CUSTOM_SMTP)->value('value');
        $this->assertSame('smtp2.example.com', $raw['host']);
        $this->assertSame('secret-pass', Crypt::decryptString($raw['password']));
    }

    public function test_the_test_send_delivers_without_error()
    {
        Mail::fake();
        $admin = User::factory()->admin()->create();
        app(ServerSettingsService::class)->setCustomSmtp($this->payload(), $admin);

        $this->actingAs($admin)
            ->post(route('admin.server-settings.custom-smtp.test'), ['email' => 'check@example.com'])
            ->assertSessionHasNoErrors();
    }

    public function test_host_is_required_when_enabled()
    {
        $this->actingAs(User::factory()->admin()->create())
            ->put(route('admin.server-settings.custom-smtp'), $this->payload(['host' => '']))
            ->assertSessionHasErrors('host');
    }

    public function test_only_an_admin_changes_custom_smtp()
    {
        $this->actingAs(User::factory()->reseller()->create())
            ->put(route('admin.server-settings.custom-smtp'), $this->payload())
            ->assertForbidden();
    }
}
