<?php

namespace App\Http\Requests;

use App\Support\DatabaseNames;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Foundation\Http\FormRequest;

class StorePostgresUserRequest extends FormRequest
{
    /**
     * The longest PostgreSQL role name, including the prefix.
     */
    private const MAX_NAME = 63;

    /**
     * Get the validation rules: the name after "<username>_", and the
     * databases it owns. The password is generated. Whether the databases
     * are the account's is checked by the PostgresService.
     *
     * @return array<string, ValidationRule|array<mixed>|string>
     */
    public function rules(): array
    {
        return [
            'name' => ['required', ...DatabaseNames::rules((string) $this->user()?->username, self::MAX_NAME)],
            'databases' => ['present', 'array', 'max:100'],
            'databases.*' => ['integer', 'distinct'],
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
        ];
    }

    /**
     * Prepare the data for validation.
     */
    protected function prepareForValidation(): void
    {
        $this->merge([
            'name' => DatabaseNames::normalize($this->input('name'), (string) $this->user()?->username),
            'databases' => $this->input('databases', []),
        ]);
    }
}
