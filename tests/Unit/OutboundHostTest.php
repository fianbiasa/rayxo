<?php

namespace Tests\Unit;

use App\Support\OutboundHost;
use PHPUnit\Framework\TestCase;

class OutboundHostTest extends TestCase
{
    public function test_internal_ip_literals_are_rejected()
    {
        foreach (['127.0.0.1', '::1', '169.254.169.254', '10.0.0.1', '172.16.0.1', '192.168.1.1', '0.0.0.0', 'fc00::1'] as $ip) {
            $this->assertTrue(OutboundHost::isInternal($ip), "$ip should be internal");
        }
    }

    public function test_public_ip_literals_are_allowed()
    {
        foreach (['8.8.8.8', '1.1.1.1', '2606:4700:4700::1111'] as $ip) {
            $this->assertFalse(OutboundHost::isInternal($ip), "$ip should be public");
        }
    }

    public function test_bracketed_ipv6_is_handled()
    {
        $this->assertTrue(OutboundHost::isInternal('[::1]'));
    }
}
