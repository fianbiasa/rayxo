<?php

namespace App\Http\Requests\Admin;

use App\Services\ServerSettingsService;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Foundation\Http\FormRequest;
use Illuminate\Validation\Rule;

class UpdateCustomSmtpRequest extends FormRequest
{
    /**
     * Custom SMTP for the panel's outgoing email. Host and port are required
     * only when it is enabled; a blank password keeps the stored one.
     *
     * @return array<string, ValidationRule|array<mixed>|string>
     */
    public function rules(): array
    {
        return [
            'enabled' => ['required', 'boolean'],
            'host' => [Rule::requiredIf(fn (): bool => $this->boolean('enabled')), 'nullable', 'string', 'max:255'],
            'port' => [Rule::requiredIf(fn (): bool => $this->boolean('enabled')), 'nullable', 'integer', 'between:1,65535'],
            'encryption' => ['required', Rule::in(ServerSettingsService::SMTP_ENCRYPTIONS)],
            'username' => ['nullable', 'string', 'max:255'],
            'password' => ['nullable', 'string', 'max:1024'],
            'from_address' => ['nullable', 'email', 'max:255'],
            'from_name' => ['nullable', 'string', 'max:255'],
        ];
    }
}
