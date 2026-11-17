<?php

namespace Tests\Unit\Rules;

use App\Rules\FilePath;
use PHPUnit\Framework\TestCase;

class FilePathTest extends TestCase
{
    public function test_valid_names_are_accepted()
    {
        $this->assertTrue(FilePath::isValidName('notes.txt'));
        $this->assertTrue(FilePath::isValidName('.hidden'));
        $this->assertTrue(FilePath::isValidName('файл.txt'));
        $this->assertTrue(FilePath::isValidName('My Folder'));
    }

    public function test_dots_as_names_are_rejected()
    {
        $this->assertFalse(FilePath::isValidName('.'));
        $this->assertFalse(FilePath::isValidName('..'));
    }

    public function test_the_255_byte_name_limit_is_enforced()
    {
        $this->assertTrue(FilePath::isValidName(str_repeat('a', 255)));
        $this->assertFalse(FilePath::isValidName(str_repeat('a', 256)));
    }

    public function test_control_characters_and_slashes_are_rejected_in_names()
    {
        $this->assertFalse(FilePath::isValidName("a\x00b"));
        $this->assertFalse(FilePath::isValidName("a\x1fb"));
        $this->assertFalse(FilePath::isValidName("a\x7fb"));
        $this->assertFalse(FilePath::isValidName('a/b'));
    }

    public function test_names_must_be_valid_utf8()
    {
        $this->assertFalse(FilePath::isValidName("\xC3\x28"));
    }

    public function test_the_empty_path_is_the_home_itself()
    {
        $this->assertTrue(FilePath::isValidPath(''));
    }

    public function test_valid_paths_are_accepted()
    {
        $this->assertTrue(FilePath::isValidPath('docs'));
        $this->assertTrue(FilePath::isValidPath('docs/notes.txt'));
        $this->assertTrue(FilePath::isValidPath('Documents/My Folder/file.pdf'));
    }

    public function test_relative_hops_and_empty_components_are_rejected()
    {
        $this->assertFalse(FilePath::isValidPath('a/./b'));
        $this->assertFalse(FilePath::isValidPath('a/../b'));
        $this->assertFalse(FilePath::isValidPath('docs//notes.txt'));
        $this->assertFalse(FilePath::isValidPath('/etc'));
        $this->assertFalse(FilePath::isValidPath('docs/'));
    }

    public function test_the_4096_byte_path_limit_is_enforced()
    {
        $this->assertTrue(FilePath::isValidPath(str_repeat('a/', 2047).'b'));
        $this->assertFalse(FilePath::isValidPath(str_repeat('a/', 2050).'b'));
    }

    public function test_the_rule_rejects_an_empty_name_only_when_name_only_mode_is_on()
    {
        $failures = [];

        (new FilePath(true))->validate('name', '', function ($message) use (&$failures): void {
            $failures[] = (string) $message;
        });

        $this->assertCount(1, $failures);

        (new FilePath(false))->validate('path', '', function ($message) use (&$failures): void {
            $failures[] = (string) $message;
        });

        $this->assertCount(1, $failures);
    }

    public function test_the_rule_reports_a_name_error_in_name_only_mode()
    {
        $failures = [];

        (new FilePath(true))->validate('name', 'a/b', function ($message) use (&$failures): void {
            $failures[] = (string) $message;
        });

        $this->assertCount(1, $failures);
        $this->assertStringContainsString('may not contain a slash', $failures[0]);
    }
}
