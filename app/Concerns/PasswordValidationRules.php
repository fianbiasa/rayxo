<?php

namespace App\Concerns;

use App\Enums\Role;
use App\Services\ServerSettingsService;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Validation\Rules\Password;

trait PasswordValidationRules
{
    /**
     * Get the validation rules used to validate passwords for an account of
     * role: the standard complexity rules, plus the shortest length the admin
     * requires of that role (never below the production default).
     *
     * @return array<int, Password|ValidationRule|array<mixed>|string>
     */
    protected function passwordRules(Role $role): array
    {
        $min = app(ServerSettingsService::class)->passwordMinLength($role);

        // Only when the admin set a minimum stricter than the default, so the
        // standard rules (and relaxed dev/test rules) are otherwise untouched.
        $longer = $min > ServerSettingsService::DEFAULT_PASSWORD_MIN_LENGTH ? ['min:'.$min] : [];

        return ['required', 'string', Password::default(), ...$longer, 'confirmed'];
    }

    /**
     * Get the validation rules used to validate the current password.
     *
     * @return array<int, Password|ValidationRule|array<mixed>|string>
     */
    protected function currentPasswordRules(): array
    {
        return ['required', 'string', 'current_password'];
    }
}
