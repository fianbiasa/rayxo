<?php

namespace App\Http\Requests;

use Illuminate\Contracts\Validation\ValidationRule;

/**
 * Turn a domain's Node.js app on, or change its entry and environment. The
 * agent validates these again before it writes anything; this keeps obvious
 * mistakes out of the UI.
 */
class NodeAppRequest extends WebsiteSettingsRequest
{
    /**
     * The most environment variables one app may set.
     */
    public const int MAX_ENV = 100;

    /**
     * Get the validation rules that apply to the request.
     *
     * @return array<string, ValidationRule|array<mixed>|string>
     */
    public function rules(): array
    {
        return [
            // A path relative to the document root: no leading slash and no
            // ".." segment, so it cannot point outside the app.
            'entry' => ['required', 'string', 'max:255', 'regex:/^(?!\/)(?!.*(?:^|\/)\.\.(?:\/|$))[^\x00\r\n]+$/'],
            'env' => ['present', 'array', 'max:'.self::MAX_ENV],
            'env.*' => ['array:name,value'],
            'env.*.name' => ['required', 'string', 'distinct', 'regex:/^[A-Za-z_][A-Za-z0-9_]*$/'],
            'env.*.value' => ['present', 'string', 'max:4096', 'regex:/^[^\r\n]*$/'],
        ];
    }

    /**
     * @return array<string, string>
     */
    public function messages(): array
    {
        return [
            'entry.regex' => __('The entry is a path inside the app folder, like .output/server/index.mjs.'),
            'env.*.name.regex' => __('A variable name has letters, digits and underscores, and does not start with a digit.'),
            'env.*.name.distinct' => __('Each variable can be set once.'),
            'env.*.value.regex' => __('A variable value is a single line.'),
        ];
    }

    /**
     * The entry script, trimmed.
     */
    public function entry(): string
    {
        return trim((string) $this->validated('entry'));
    }

    /**
     * The environment as a name => value map.
     *
     * @return array<string, string>
     */
    public function env(): array
    {
        $env = [];

        /** @var array<int, array{name: string, value: string}> $pairs */
        $pairs = (array) $this->validated('env');

        foreach ($pairs as $pair) {
            $env[$pair['name']] = $pair['value'];
        }

        return $env;
    }

    protected function prepareForValidation(): void
    {
        $this->merge([
            'entry' => is_string($this->input('entry')) ? trim($this->input('entry')) : $this->input('entry'),
            'env' => is_array($this->input('env')) ? array_values($this->input('env')) : [],
        ]);
    }
}
