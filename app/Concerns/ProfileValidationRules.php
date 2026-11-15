<?php

namespace App\Concerns;

use App\Enums\Role;
use App\Models\User;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Validation\Rule;
use Illuminate\Validation\Rules\Unique;

trait ProfileValidationRules
{
    /**
     * Get the validation rules used to validate user profiles.
     *
     * @return array<string, array<int, Unique|ValidationRule|array<mixed>|string>>
     */
    protected function profileRules(?int $userId = null, bool $hostingAccount = false): array
    {
        return [
            'name' => $this->nameRules(),
            'email' => $this->emailRules($userId, $hostingAccount),
        ];
    }

    /**
     * Get the validation rules used to validate user names.
     *
     * @return array<int, Unique|ValidationRule|array<mixed>|string>
     */
    protected function nameRules(): array
    {
        return ['required', 'string', 'max:255'];
    }

    /**
     * Get the validation rules used to validate user emails. Hosting
     * accounts may share an address with each other, as in cPanel and
     * DirectAdmin (a WHMCS client with several hosting services), but never
     * with an administrator or reseller, whose addresses stay unique so
     * their sign-in and password resets never have to choose an account.
     *
     * @return array<int, Unique|ValidationRule|array<mixed>|string>
     */
    protected function emailRules(?int $userId = null, bool $hostingAccount = false): array
    {
        $unique = Rule::unique(User::class);

        if ($hostingAccount) {
            $unique = $unique->whereIn('role', [Role::Admin->value, Role::Reseller->value]);
        }

        return [
            'required',
            'string',
            'email',
            'max:255',
            $userId === null ? $unique : $unique->ignore($userId),
        ];
    }
}
