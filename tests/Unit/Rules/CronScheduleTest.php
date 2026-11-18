<?php

namespace Tests\Unit\Rules;

use App\Rules\CronSchedule;
use PHPUnit\Framework\TestCase;

class CronScheduleTest extends TestCase
{
    public function test_valid_schedules_pass()
    {
        foreach ([
            '@hourly',
            '@daily',
            '@midnight',
            '@weekly',
            '@monthly',
            '@yearly',
            '@annually',
            '*/5 * * * *',
            '5,10,15 * * * *',
            '0 12 * jan sun',
            '1-10/2 0-23 1-31 jan-dec mon-fri',
            '*/15 */6 * * mon',
            '5 2 * feb-mar *',
            '30 2 1 1 0',
            '0 0 * * 7',
        ] as $schedule) {
            $this->assertTrue(CronSchedule::isValid($schedule), "Expected [{$schedule}] to be valid.");
        }
    }

    public function test_unknown_special_schedules_are_rejected()
    {
        $this->assertFalse(CronSchedule::isValid('@reboot'));
        $this->assertFalse(CronSchedule::isValid('@everyhour'));
    }

    public function test_the_field_count_is_exactly_five()
    {
        $this->assertFalse(CronSchedule::isValid('*/5 * * *'));
        $this->assertFalse(CronSchedule::isValid('*/5 * * * * *'));
    }

    public function test_values_outside_a_field_range_are_rejected()
    {
        $this->assertFalse(CronSchedule::isValid('60 * * * *'));
        $this->assertFalse(CronSchedule::isValid('* 24 * * *'));
        $this->assertFalse(CronSchedule::isValid('* * 32 * *'));
        $this->assertFalse(CronSchedule::isValid('* * 0 * *'));
        $this->assertFalse(CronSchedule::isValid('* * * 13 *'));
        $this->assertFalse(CronSchedule::isValid('* * * * 8'));
    }

    public function test_steps_are_only_allowed_on_a_range_or_a_star()
    {
        $this->assertFalse(CronSchedule::isValid('5/2 * * * *'));
        $this->assertTrue(CronSchedule::isValid('*/2 * * * *'));
        $this->assertTrue(CronSchedule::isValid('1-10/2 * * * *'));
    }

    public function test_a_step_outside_the_field_maximum_is_rejected()
    {
        $this->assertFalse(CronSchedule::isValid('*/0 * * * *'));
        $this->assertFalse(CronSchedule::isValid('*/60 * * * *'));
        $this->assertTrue(CronSchedule::isValid('*/59 * * * *'));
    }

    public function test_ranges_must_go_forward_and_hold_two_bounds()
    {
        $this->assertFalse(CronSchedule::isValid('10-5 * * * *'));
        $this->assertFalse(CronSchedule::isValid('feb-jan * * * *'));
        $this->assertFalse(CronSchedule::isValid('1--5 * * * *'));
        $this->assertFalse(CronSchedule::isValid('1-2-3 * * * *'));
    }

    public function test_names_are_only_accepted_in_the_month_and_weekday_fields()
    {
        $this->assertFalse(CronSchedule::isValid('janx * * * *'));
        $this->assertFalse(CronSchedule::isValid('* * * * janx'));
        $this->assertTrue(CronSchedule::isValid('* * * jan *'));
        $this->assertTrue(CronSchedule::isValid('* * * * mon'));
    }

    public function test_is_valid_does_not_normalize_so_surrounding_space_is_rejected()
    {
        $this->assertFalse(CronSchedule::isValid('  * * * * *'));
        $this->assertFalse(CronSchedule::isValid('* * * * * '));
    }

    public function test_normalize_lowercases_and_collapses_whitespace()
    {
        $this->assertSame('*/5 * * * *', CronSchedule::normalize('  */5   *  * * *  '));
        $this->assertSame('@hourly', CronSchedule::normalize(' @HOURLY '));
    }

    public function test_normalize_passes_non_strings_through()
    {
        $this->assertSame(5, CronSchedule::normalize(5));
        $this->assertNull(CronSchedule::normalize(null));
    }
}
