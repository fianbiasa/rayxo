<?php

namespace App\Support;

/**
 * Decides whether an outbound destination host is an internal address, so an
 * admin-supplied URL cannot make the panel reach loopback, link-local
 * (including cloud-metadata 169.254.169.254) or private-range services.
 *
 * Best-effort: it rejects a literal internal IP and a hostname that resolves
 * to one, but fails open on a host it cannot resolve and does not defend
 * against DNS rebinding between this check and the request. That is
 * proportionate here — the only caller is an admin-only migration source, and
 * an admin already controls the server.
 */
class OutboundHost
{
    /**
     * Whether host (an IP literal or a hostname) is, or resolves to, an
     * internal address.
     */
    public static function isInternal(string $host): bool
    {
        $host = trim($host, '[]');

        if (filter_var($host, FILTER_VALIDATE_IP) !== false) {
            return self::ipIsInternal($host);
        }

        foreach (self::resolve($host) as $ip) {
            if (self::ipIsInternal($ip)) {
                return true;
            }
        }

        return false;
    }

    /**
     * An IP is internal when it is in a private or reserved range (which
     * covers loopback, link-local, and the unspecified address).
     */
    private static function ipIsInternal(string $ip): bool
    {
        return filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE) === false;
    }

    /**
     * The IPv4 and IPv6 addresses a hostname resolves to; empty if it cannot
     * be resolved.
     *
     * @return list<string>
     */
    private static function resolve(string $host): array
    {
        $ips = [];

        foreach (@dns_get_record($host, DNS_A | DNS_AAAA) ?: [] as $record) {
            foreach (['ip', 'ipv6'] as $key) {
                if (! empty($record[$key])) {
                    $ips[] = $record[$key];
                }
            }
        }

        return $ips;
    }
}
