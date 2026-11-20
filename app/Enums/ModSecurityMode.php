<?php

namespace App\Enums;

/**
 * The per-website ModSecurity (WAF) override: whether the OWASP Core Rule Set
 * runs for a website, on top of the server-wide default.
 */
enum ModSecurityMode: string
{
    /** Follow the server-wide ModSecurity mode. */
    case Inherit = 'inherit';

    /** Turn the WAF off for this website. */
    case Off = 'off';

    /** Run the rules but only log, never block. */
    case Detection = 'detection';

    /** Run the rules and block offending requests. */
    case On = 'on';
}
