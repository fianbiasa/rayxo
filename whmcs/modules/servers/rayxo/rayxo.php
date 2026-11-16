<?php

/**
 * RAYXO provisioning module for WHMCS.
 *
 * Unzip rayxo-whmcs-<version>.zip (from the RAYXO release of the panel's
 * version, at rayxopanel.com/download) in the WHMCS folder, which puts this
 * folder at <whmcs>/modules/servers/rayxo. In WHMCS, add a server
 * (System Settings > Servers) of module "RAYXO" with:
 *   - Hostname: the panel's hostname, Port: the panel's port (2222 by
 *     default), "Use SSL" on;
 *   - Access Hash: a login key made in RAYXO (Settings > Login key) by an
 *     administrator or reseller, with the permissions accounts:read,
 *     accounts:write, accounts:login and packages:read.
 * Then give a product the module "RAYXO" and the name of a RAYXO package.
 *
 * It creates, suspends, unsuspends and terminates hosting accounts, changes
 * their package and password, signs customers in to the panel (single
 * sign-on, from the admin area and the customer's service page), and reads
 * disk and bandwidth usage for WHMCS.
 */

use WHMCS\Database\Capsule;

if (! defined('WHMCS')) {
    exit('This file cannot be accessed directly');
}

/**
 * @return array<string, mixed>
 */
function rayxo_MetaData()
{
    return [
        'DisplayName' => 'RAYXO',
        'APIVersion' => '1.1',
        'RequiresServer' => true,
        'DefaultNonSSLPort' => '2222',
        'DefaultSSLPort' => '2222',
        'ServiceSingleSignOnLabel' => 'Login to RAYXO',
    ];
}

/**
 * @return array<string, array<string, mixed>>
 */
function rayxo_ConfigOptions()
{
    return [
        'package' => [
            'FriendlyName' => 'RAYXO package',
            'Type' => 'text',
            'Size' => '30',
            'Description' => 'The name of the package in RAYXO; empty for no package',
            'Loader' => 'rayxo_PackageLoader',
            'SimpleMode' => true,
        ],
    ];
}

/**
 * The packages of the server, for the product's package field.
 *
 * @param  array<string, mixed>  $params
 * @return array<string, string>
 */
function rayxo_PackageLoader(array $params)
{
    $packages = [];

    foreach (rayxo_request($params, 'GET', '/packages')['data'] ?? [] as $package) {
        $packages[$package['name']] = $package['name'];
    }

    return $packages;
}

/**
 * @param  array<string, mixed>  $params
 * @return array<string, mixed>
 */
function rayxo_TestConnection(array $params)
{
    try {
        $me = rayxo_request($params, 'GET', '/me')['data'] ?? [];
        $missing = array_diff(['accounts:read', 'accounts:write', 'accounts:login', 'packages:read'], $me['login_key']['permissions'] ?? []);

        if (! in_array($me['role'] ?? '', ['admin', 'reseller'], true)) {
            return ['success' => false, 'error' => 'The login key belongs to the hosting account "'.($me['username'] ?? '?').'". Make one while signed in to RAYXO as an administrator or reseller.'];
        }

        if ($missing !== []) {
            return ['success' => false, 'error' => 'The login key lacks: '.implode(', ', $missing)];
        }

        return ['success' => true, 'error' => ''];
    } catch (Exception $exception) {
        return ['success' => false, 'error' => $exception->getMessage()];
    }
}

/**
 * @param  array<string, mixed>  $params
 * @return string
 */
function rayxo_CreateAccount(array $params)
{
    return rayxo_run(function () use ($params): void {
        $client = $params['clientsdetails'] ?? [];
        $name = trim(($client['firstname'] ?? '').' '.($client['lastname'] ?? ''));

        rayxo_request($params, 'POST', '/accounts', [
            'username' => $params['username'],
            'name' => $name !== '' ? $name : $params['username'],
            'email' => $client['email'] ?? '',
            'password' => $params['password'],
            'domain' => $params['domain'] !== '' ? $params['domain'] : null,
            'package_id' => rayxo_packageId($params),
        ]);
    });
}

/**
 * @param  array<string, mixed>  $params
 * @return string
 */
function rayxo_SuspendAccount(array $params)
{
    return rayxo_run(function () use ($params): void {
        rayxo_request($params, 'POST', '/accounts/'.rawurlencode($params['username']).'/suspend', [
            'reason' => $params['suspendreason'] !== '' ? $params['suspendreason'] : null,
        ]);
    });
}

/**
 * @param  array<string, mixed>  $params
 * @return string
 */
function rayxo_UnsuspendAccount(array $params)
{
    return rayxo_run(function () use ($params): void {
        rayxo_request($params, 'POST', '/accounts/'.rawurlencode($params['username']).'/unsuspend');
    });
}

/**
 * @param  array<string, mixed>  $params
 * @return string
 */
function rayxo_TerminateAccount(array $params)
{
    return rayxo_run(function () use ($params): void {
        rayxo_request($params, 'DELETE', '/accounts/'.rawurlencode($params['username']));
    });
}

/**
 * @param  array<string, mixed>  $params
 * @return string
 */
function rayxo_ChangePackage(array $params)
{
    return rayxo_run(function () use ($params): void {
        rayxo_request($params, 'PUT', '/accounts/'.rawurlencode($params['username']).'/package', [
            'package_id' => rayxo_packageId($params),
        ]);
    });
}

/**
 * @param  array<string, mixed>  $params
 * @return string
 */
function rayxo_ChangePassword(array $params)
{
    return rayxo_run(function () use ($params): void {
        rayxo_request($params, 'PUT', '/accounts/'.rawurlencode($params['username']).'/password', [
            'password' => $params['password'],
        ]);
    });
}

/**
 * Sign the customer in to the panel with a one-time link.
 *
 * @param  array<string, mixed>  $params
 * @return array<string, mixed>
 */
function rayxo_ServiceSingleSignOn(array $params)
{
    try {
        $link = rayxo_request($params, 'POST', '/accounts/'.rawurlencode($params['username']).'/login-link')['data'] ?? [];

        return ['success' => true, 'redirectTo' => $link['url'] ?? ''];
    } catch (Exception $exception) {
        return ['success' => false, 'errorMsg' => $exception->getMessage()];
    }
}

/**
 * The "Log in to RAYXO" button on the customer's service page. WHMCS shows
 * the single sign-on button only in its admin area by itself; customers get
 * this one while the service is active.
 *
 * @param  array<string, mixed>  $params
 * @return array<string, mixed>|string
 */
function rayxo_ClientArea(array $params)
{
    if (($params['status'] ?? '') !== 'Active') {
        return '';
    }

    return ['templatefile' => 'templates/clientarea', 'vars' => []];
}

/**
 * Disk and this month's bandwidth of every account of the server, in MB.
 *
 * @param  array<string, mixed>  $params
 * @return string
 */
function rayxo_UsageUpdate(array $params)
{
    return rayxo_run(function () use ($params): void {
        foreach (rayxo_request($params, 'GET', '/usage')['data'] ?? [] as $usage) {
            Capsule::table('tblhosting')
                ->where('server', $params['serverid'])
                ->where('username', $usage['username'])
                ->update([
                    'diskusage' => (int) $usage['disk_mb'],
                    'disklimit' => (int) ($usage['disk_limit_mb'] ?? 0),
                    'bwusage' => (int) $usage['bandwidth_mb'],
                    'bwlimit' => (int) ($usage['bandwidth_limit_mb'] ?? 0),
                    'lastupdate' => date('Y-m-d H:i:s'),
                ]);
        }
    });
}

/**
 * Run an action, answering WHMCS with "success" or the error.
 *
 * @return string
 */
function rayxo_run(callable $action)
{
    try {
        $action();

        return 'success';
    } catch (Exception $exception) {
        return $exception->getMessage();
    }
}

/**
 * The id of the product's RAYXO package, by its name; null for none.
 *
 * @param  array<string, mixed>  $params
 * @return int|null
 */
function rayxo_packageId(array $params)
{
    $name = trim((string) ($params['configoption1'] ?? ''));

    if ($name === '') {
        return null;
    }

    foreach (rayxo_request($params, 'GET', '/packages')['data'] ?? [] as $package) {
        if (strcasecmp($package['name'], $name) === 0) {
            return (int) $package['id'];
        }
    }

    throw new Exception('RAYXO has no package named '.$name.'.');
}

/**
 * Call the panel's API with the server's login key.
 *
 * @param  array<string, mixed>  $params
 * @param  array<string, mixed>|null  $body
 * @return array<string, mixed>
 *
 * @throws Exception The panel's message when it refuses.
 */
function rayxo_request(array $params, $method, $path, $body = null)
{
    $host = $params['serverhostname'] !== '' ? $params['serverhostname'] : $params['serverip'];
    $port = (int) ($params['serverport'] ?: 2222);
    $scheme = ! empty($params['serversecure']) ? 'https' : 'http';
    $url = $scheme.'://'.$host.':'.$port.'/api/v1'.$path;
    $key = (string) $params['serveraccesshash'];

    if ($key === '') {
        throw new Exception('The server has no login key: put a RAYXO login key in its Access Hash.');
    }

    // The login key goes in the Authorization header; never send it over plain
    // HTTP. Turn on "Secure" (SSL Mode) for the server in WHMCS.
    if ($scheme !== 'https') {
        throw new Exception('Turn on SSL Mode for this server: the RAYXO login key must not be sent over plain HTTP.');
    }

    $curl = curl_init($url);
    curl_setopt_array($curl, [
        CURLOPT_CUSTOMREQUEST => $method,
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT => 120,
        CURLOPT_CONNECTTIMEOUT => 15,
        CURLOPT_HTTPHEADER => array_merge(
            ['Authorization: Bearer '.$key, 'Accept: application/json'],
            $body !== null ? ['Content-Type: application/json'] : []
        ),
    ]);

    if ($body !== null) {
        curl_setopt($curl, CURLOPT_POSTFIELDS, json_encode($body));
    }

    $raw = curl_exec($curl);
    $status = (int) curl_getinfo($curl, CURLINFO_HTTP_CODE);
    $failure = curl_error($curl);
    curl_close($curl);

    $answer = is_string($raw) ? json_decode($raw, true) : null;
    // Redact the login key and any password in the body from WHMCS's module log.
    $redact = array_values(array_filter([$key, isset($body['password']) ? (string) $body['password'] : null], 'strlen'));
    logModuleCall('rayxo', $method.' '.$path, $body, $raw, $answer, $redact);

    if ($raw === false) {
        throw new Exception('RAYXO could not be reached: '.$failure);
    }

    if ($status < 400 && ! is_array($answer)) {
        throw new Exception('RAYXO did not answer as its API (HTTP '.$status.'): check the hostname, port and SSL of the server.');
    }

    if ($status >= 400) {
        $errors = is_array($answer['errors'] ?? null) ? $answer['errors'] : [];
        $first = $errors !== [] ? reset($errors) : null;
        $message = is_array($first) ? (string) reset($first) : (string) ($answer['message'] ?? 'HTTP '.$status);

        throw new Exception('RAYXO: '.$message);
    }

    return is_array($answer) ? $answer : [];
}
