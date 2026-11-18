<?php

namespace Tests\Unit\Rules;

use App\Rules\ValidDomain;
use PHPUnit\Framework\TestCase;

class ValidDomainTest extends TestCase
{
    use FakesFailClosure;

    public function test_well_formed_domains_pass_the_shape_check()
    {
        $this->assertTrue(ValidDomain::isWellFormed('example.com'));
        $this->assertTrue(ValidDomain::isWellFormed('a-b.co'));
        $this->assertTrue(ValidDomain::isWellFormed('x9.io'));
        $this->assertTrue(ValidDomain::isWellFormed('xn--mnchen-3ya.de'));
        $this->assertTrue(ValidDomain::isWellFormed('deep.sub.domain.example.org'));
    }

    public function test_a_single_label_is_rejected()
    {
        $this->assertFalse(ValidDomain::isWellFormed('localhost'));
    }

    public function test_the_total_length_is_capped_at_253_characters()
    {
        $valid = implode('.', [
            str_repeat('a', 63),
            str_repeat('b', 63),
            str_repeat('c', 63),
            str_repeat('d', 61),
        ]);

        $this->assertSame(253, strlen($valid));
        $this->assertTrue(ValidDomain::isWellFormed($valid));
        $this->assertFalse(ValidDomain::isWellFormed($valid.'a'));
    }

    public function test_malformed_labels_are_rejected()
    {
        $this->assertFalse(ValidDomain::isWellFormed('-example.com'));
        $this->assertFalse(ValidDomain::isWellFormed('example-.com'));
        $this->assertFalse(ValidDomain::isWellFormed('example..com'));
        $this->assertFalse(ValidDomain::isWellFormed('exa mple.com'));
        $this->assertFalse(ValidDomain::isWellFormed('Example.com'));
        $this->assertFalse(ValidDomain::isWellFormed('münchen.de'));
    }

    public function test_a_trailing_dot_breaks_the_shape_check_before_normalization()
    {
        $this->assertFalse(ValidDomain::isWellFormed('example.com.'));
    }

    public function test_a_numeric_last_label_is_rejected()
    {
        $this->assertFalse(ValidDomain::isWellFormed('192.168.1.1'));
    }

    public function test_normalize_trims_lowercases_and_strips_one_trailing_dot()
    {
        $this->assertSame('example.com', ValidDomain::normalize(' EXAMPLE.com '));
        $this->assertSame('example.com', ValidDomain::normalize('Example.COM.'));
        $this->assertSame('example.com', ValidDomain::normalize('example.com'));
    }

    public function test_normalize_converts_non_ascii_names_to_punycode()
    {
        $this->assertSame('xn--mnchen-3ya.de', ValidDomain::normalize('münchen.de'));
        $this->assertSame('xn--mnchen-3ya.de', ValidDomain::normalize('MÜNCHEN.de'));
    }

    public function test_normalize_passes_non_strings_and_blanks_through_for_validation_to_reject()
    {
        $this->assertNull(ValidDomain::normalize(''));
        $this->assertSame(123, ValidDomain::normalize(123));
        $this->assertNull(ValidDomain::normalize(null));
    }

    public function test_validate_rejects_www_names_without_the_www_prefix()
    {
        $failures = [];

        (new ValidDomain)->validate('domain', 'www.example.com', $this->failInto($failures));

        $this->assertCount(1, $failures);
        $this->assertStringContainsString('without "www."', $failures[0]);
    }

    public function test_validate_reports_a_shape_problem_before_touching_the_server_hostname()
    {
        $failures = [];

        (new ValidDomain)->validate('domain', '-bad.com', $this->failInto($failures));

        $this->assertCount(1, $failures);
        $this->assertStringContainsString('must be a valid domain name', $failures[0]);
    }
}
