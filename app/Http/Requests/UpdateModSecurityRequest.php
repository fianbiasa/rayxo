<?php

namespace App\Http\Requests;

use App\Enums\ModSecurityMode;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Validation\Rule;

class UpdateModSecurityRequest extends WebsiteSettingsRequest
{
    /**
     * Get the validation rules that apply to the request: the website's
     * ModSecurity (WAF) override — inherit, off, detection or on.
     *
     * @return array<string, ValidationRule|array<mixed>|string>
     */
    public function rules(): array
    {
        return [
            'mode' => ['required', Rule::enum(ModSecurityMode::class)],
        ];
    }
}
