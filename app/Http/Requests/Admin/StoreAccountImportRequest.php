<?php

namespace App\Http\Requests\Admin;

use App\Enums\Role;
use App\Models\Package;
use App\Models\User;
use App\Rules\ValidUsername;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Foundation\Http\FormRequest;
use Illuminate\Validation\Rule;
use Illuminate\Validation\Rules\Password;

/**
 * The import of a backup of cPanel or DirectAdmin in the backups folder:
 * the account made from it may get another username and email address
 * than it had there, and always gets a new password (the panel's cannot
 * move).
 */
class StoreAccountImportRequest extends FormRequest
{
    /**
     * Get the validation rules that apply to the request.
     *
     * @return array<string, ValidationRule|array<mixed>|string>
     */
    public function rules(): array
    {
        /** @var User $actor */
        $actor = $this->user();

        return [
            'file' => ['required', 'string', 'max:160', 'regex:/^[A-Za-z0-9][A-Za-z0-9._-]{0,150}\.(tar\.gz|tgz|tar\.zst|tar)$/D'],
            'username' => ['nullable', 'string', new ValidUsername, Rule::unique(User::class)],
            // The account is a hosting account: it may share its address with
            // other hosting accounts, not with an administrator or reseller.
            'email' => ['nullable', 'string', 'lowercase', 'email', 'max:255', Rule::unique(User::class)->whereIn('role', [Role::Admin->value, Role::Reseller->value])],
            'password' => ['required', 'string', Password::default()],
            'package_id' => ['nullable', 'integer', Rule::in(Package::query()->handedOutBy($actor)->pluck('id')->all())],
        ];
    }

    /**
     * @return array<string, string>
     */
    public function messages(): array
    {
        return [
            'package_id.in' => __('Choose one of your packages.'),
        ];
    }
}
