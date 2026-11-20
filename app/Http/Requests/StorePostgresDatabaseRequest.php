<?php

namespace App\Http\Requests;

use App\Support\DatabaseNames;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Foundation\Http\FormRequest;

class StorePostgresDatabaseRequest extends FormRequest
{
    /**
     * The longest PostgreSQL database or role name, including the prefix.
     */
    private const MAX_NAME = 63;

    /**
     * Get the validation rules that apply to the request: the name after
     * "<username>_", and optionally a new role that owns it. The role's
     * password is generated, so none is asked for.
     *
     * @return array<string, ValidationRule|array<mixed>|string>
     */
    public function rules(): array
    {
        $username = (string) $this->user()?->username;

        return [
            'name' => ['required', ...DatabaseNames::rules($username, self::MAX_NAME)],
            'create_user' => ['required', 'boolean'],
            'user_name' => ['exclude_unless:create_user,true,1', 'required', ...DatabaseNames::rules($username, self::MAX_NAME)],
        ];
    }

    /**
     * Get custom messages for validator errors.
     *
     * @return array<string, string>
     */
    public function messages(): array
    {
        return [
            'name.regex' => __('Use only lowercase letters, digits and underscores.'),
            'user_name.regex' => __('Use only lowercase letters, digits and underscores.'),
        ];
    }

    /**
     * Prepare the data for validation.
     */
    protected function prepareForValidation(): void
    {
        $username = (string) $this->user()?->username;

        $this->merge([
            'name' => DatabaseNames::normalize($this->input('name'), $username),
            'user_name' => DatabaseNames::normalize($this->input('user_name'), $username),
        ]);
    }
}
