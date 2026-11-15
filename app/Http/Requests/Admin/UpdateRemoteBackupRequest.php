<?php

namespace App\Http\Requests\Admin;

use App\Services\RemoteBackupService;
use Closure;
use Illuminate\Contracts\Validation\ValidationRule;
use Illuminate\Foundation\Http\FormRequest;
use Illuminate\Validation\Rule;

/**
 * The remote destination of backups: an FTP or FTPS server, an SFTP server
 * with the host key the administrator accepted, or an S3 bucket. A password
 * or secret key left empty keeps the saved one. The agent checks every
 * field again before rclone sees it.
 */
class UpdateRemoteBackupRequest extends FormRequest
{
    private const CONTROL = '/[\x00-\x1F\x7F]/';

    private const FOLDER = '[A-Za-z0-9_][A-Za-z0-9._ -]{0,99}';

    /**
     * Get the validation rules that apply to the request.
     *
     * @return array<string, ValidationRule|array<mixed>|string>
     */
    public function rules(): array
    {
        $folders = self::FOLDER.'(\/'.self::FOLDER.'){0,7}';

        return [
            'enabled' => ['required', 'boolean'],
            'type' => ['required', Rule::in(RemoteBackupService::TYPES)],
            'host' => ['exclude_if:type,s3', 'required', 'string', 'max:253', 'regex:/^[A-Za-z0-9]([A-Za-z0-9.:-]{0,251}[A-Za-z0-9])?$/'],
            'port' => ['exclude_if:type,s3', 'nullable', 'integer', 'between:1,65535'],
            'user' => ['exclude_if:type,s3', 'required', 'string', 'max:128', 'not_regex:'.self::CONTROL],
            'password' => ['exclude_if:type,s3', 'nullable', 'string', 'max:256', 'not_regex:'.self::CONTROL],
            'tls' => ['exclude_unless:type,ftp', 'required', 'boolean'],
            // Every key the server offered, one per line: the client may
            // settle on any of them.
            'host_key' => ['exclude_unless:type,sftp', 'required', 'string', 'max:8192', 'regex:/\A(ssh-ed25519|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521|ssh-rsa) [A-Za-z0-9+\/]+={0,2}(\n(ssh-ed25519|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521|ssh-rsa) [A-Za-z0-9+\/]+={0,2}){0,4}\z/'],
            'endpoint' => ['exclude_unless:type,s3', 'nullable', 'string', 'max:255', $this->endpoint(...)],
            'region' => ['exclude_unless:type,s3', 'nullable', 'string', 'regex:/^[a-z0-9-]{0,40}$/'],
            'bucket' => ['exclude_unless:type,s3', 'required', 'string', 'regex:/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/'],
            'access_key' => ['exclude_unless:type,s3', 'required', 'string', 'max:256', 'regex:/^\S+$/'],
            'secret_key' => ['exclude_unless:type,s3', 'nullable', 'string', 'max:256', 'not_regex:'.self::CONTROL],
            'path' => ['nullable', 'string', 'max:255', $this->input('type') === 's3' ? 'regex:/^'.$folders.'$/' : 'regex:/^\/?'.$folders.'$/'],
            'keep' => ['required', 'integer', 'between:1,'.RemoteBackupService::MAX_KEEP],
            'keep_local' => ['required', 'boolean'],
        ];
    }

    /**
     * An S3 address must use https, but on this machine itself (a local S3
     * service), so keys never cross a network in the clear.
     */
    private function endpoint(string $attribute, mixed $value, Closure $fail): void
    {
        $parts = parse_url((string) $value);
        $host = is_array($parts) ? (string) ($parts['host'] ?? '') : '';
        $scheme = is_array($parts) ? (string) ($parts['scheme'] ?? '') : '';
        $local = in_array($host, ['localhost', '127.0.0.1', '[::1]'], true);

        if (! is_array($parts) || $host === '' || isset($parts['user']) || isset($parts['query']) || ! in_array($parts['path'] ?? '/', ['', '/'], true)
            || ! ($scheme === 'https' || ($scheme === 'http' && $local))) {
            $fail(__('Enter an https address, such as https://s3.example.com.'));
        }
    }

    /**
     * @return array<string, string>
     */
    public function messages(): array
    {
        return [
            'host.regex' => __('Enter a host name or IP address.'),
            'host_key.required' => __('Fetch the host key of the SFTP server and accept it first.'),
            'host_key.regex' => __('Fetch the host key of the SFTP server and accept it first.'),
            'bucket.regex' => __('Enter the name of a bucket, such as my-backups.'),
            'region.regex' => __('Enter a region such as us-east-1, or leave it empty.'),
            'access_key.regex' => __('The access key holds no spaces.'),
            'path.regex' => __('The folder may only hold folders of letters, digits, spaces, dots, dashes and underscores.'),
        ];
    }
}
