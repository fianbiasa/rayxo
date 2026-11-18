<?php

namespace App\Http\Requests\Admin;

use App\Enums\Role;
use App\Services\ServerSettingsService;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Foundation\Http\FormRequest;
use Illuminate\Validation\Rule;

class UpdateSecurityPolicyRequest extends FormRequest
{
    /**
     * Which roles must have two-factor authentication (only administrators and
     * resellers may be required), and the shortest password each role may set
     * (never below the production default, never absurdly long).
     *
     * @return array<string, ValidationRule|array<mixed>|string>
     */
    public function rules(): array
    {
        return [
            'require_2fa' => ['array'],
            'require_2fa.*' => ['string', Rule::in([Role::Admin->value, Role::Reseller->value])],
            'password_min_length' => ['array'],
            'password_min_length.admin' => $this->minRule(),
            'password_min_length.reseller' => $this->minRule(),
            'password_min_length.user' => $this->minRule(),
        ];
    }

    /**
     * @return list<string>
     */
    private function minRule(): array
    {
        return [
            'sometimes',
            'integer',
            'min:'.ServerSettingsService::DEFAULT_PASSWORD_MIN_LENGTH,
            'max:'.ServerSettingsService::MAX_PASSWORD_MIN_LENGTH,
        ];
    }
}
