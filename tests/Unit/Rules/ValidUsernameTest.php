<?php

namespace Tests\Unit\Rules;

use App\Rules\ValidUsername;
use PHPUnit\Framework\TestCase;

class ValidUsernameTest extends TestCase
{
    use FakesFailClosure;

    private ValidUsername $rule;

    private array $failures = [];

    protected function setUp(): void
    {
        $this->rule = new ValidUsername;
        $this->failures = [];
    }

    private function validate(mixed $value): void
    {
        $this->failures = [];
        $this->rule->validate('username', $value, $this->failInto($this->failures));
    }

    private function assertRejected(mixed $value): void
    {
        $this->validate($value);
        $this->assertCount(1, $this->failures, 'Expected '.var_export($value, true).' to be rejected.');
    }

    private function assertAccepted(mixed $value): void
    {
        $this->validate($value);
        $this->assertSame([], $this->failures, 'Expected '.var_export($value, true).' to be accepted.');
    }

    public function test_accepts_usernames_inside_the_pattern()
    {
        $this->assertAccepted('abc');
        $this->assertAccepted('x9z');
        $this->assertAccepted('user01');
        $this->assertAccepted('abcdefghijklmnop');
    }

    public function test_rejects_usernames_outside_the_pattern()
    {
        $this->assertRejected('ab');
        $this->assertRejected('abcdefghijklmnopq');
        $this->assertRejected('Abc');
        $this->assertRejected('1abc');
        $this->assertRejected('a-bc');
        $this->assertRejected('a bc');
        $this->assertRejected('user_name');
        $this->assertRejected('example.com');
    }

    public function test_the_dollar_delimiter_stops_a_trailing_newline_from_slipping_through()
    {
        $this->assertRejected("abc\n");
    }

    public function test_rejects_values_that_are_not_strings()
    {
        $this->assertRejected(123);
        $this->assertRejected(null);
        $this->assertRejected(['abc']);
    }

    public function test_rejects_names_reserved_by_the_system_or_services()
    {
        foreach (['root', 'rayxo', 'rayxod', 'mysql', 'nginx', 'postfix', 'www', 'information', 'performance', 'docker', 'crontab'] as $reserved) {
            $this->failures = [];
            $this->validate($reserved);
            $this->assertCount(1, $this->failures, "Expected [{$reserved}] to be rejected as reserved.");
            $this->assertStringContainsString('reserved', $this->failures[0]);
        }
    }

    public function test_a_pattern_failure_is_reported_before_the_reserved_check()
    {
        $this->validate('ROOT');

        $this->assertCount(1, $this->failures);
        $this->assertStringContainsString('must be 3 to 16 characters long', $this->failures[0]);
    }
}
