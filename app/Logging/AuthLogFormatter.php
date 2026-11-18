<?php

namespace App\Logging;

use Illuminate\Log\Logger;
use Monolog\Formatter\LineFormatter;
use Monolog\Handler\FormattableHandlerInterface;
use Monolog\Logger as Monolog;

/**
 * Formats the auth channel as one predictable line per entry,
 * "[Y-m-d H:i:s] <message>", for the fail2ban rayxo-panel jail to parse. Line
 * breaks inside a message are not allowed, so a crafted username can never
 * forge a second log line (and so a banned address).
 */
class AuthLogFormatter
{
    public function __invoke(Logger $logger): void
    {
        $monolog = $logger->getLogger();
        if (! $monolog instanceof Monolog) {
            return;
        }

        $formatter = new LineFormatter("[%datetime%] %message%\n", 'Y-m-d H:i:s', false, true);

        foreach ($monolog->getHandlers() as $handler) {
            if ($handler instanceof FormattableHandlerInterface) {
                $handler->setFormatter($formatter);
            }
        }
    }
}
