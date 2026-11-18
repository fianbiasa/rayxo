<?php

namespace Tests\Unit\Rules;

use Closure;

/**
 * Builds the $fail closure a Laravel ValidationRule is handed. It records each
 * message and returns a stand-in for the PotentiallyTranslatedString the real
 * validator returns, so a rule's chained ->translate() call works under a
 * plain PHPUnit\Framework\TestCase, with no application container.
 */
trait FakesFailClosure
{
    /**
     * @param  list<string>  $failures  Collected, in order, as each is reported.
     */
    protected function failInto(array &$failures): Closure
    {
        return function ($message) use (&$failures): object {
            $failures[] = (string) $message;

            return new class
            {
                public function translate(): void {}
            };
        };
    }
}
