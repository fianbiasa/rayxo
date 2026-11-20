package waf

import (
	"context"
	"os"
	"testing"
)

func TestCheckReportsNoDriftAfterApply(t *testing.T) {
	m, _, _ := newManager(t)

	if err := m.Apply(context.Background(), Settings{Mode: ModeOn}); err != nil {
		t.Fatal(err)
	}

	report, err := m.Check(Settings{Mode: ModeOn})
	if err != nil {
		t.Fatal(err)
	}

	if report.Drifted() {
		t.Fatalf("engine matching the mode reported drift: %+v", report.Targets)
	}
}

func TestCheckDetectsADifferentMode(t *testing.T) {
	m, _, engine := newManager(t)

	if err := m.Apply(context.Background(), Settings{Mode: ModeOff}); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(engine)
	if err != nil {
		t.Fatal(err)
	}

	// On disk is Off, but the configured mode is On → drift.
	report, err := m.Check(Settings{Mode: ModeOn})
	if err != nil {
		t.Fatal(err)
	}

	if !report.Drifted() {
		t.Fatal("a mode difference was not reported as drift")
	}

	after, err := os.ReadFile(engine)
	if err != nil {
		t.Fatal(err)
	}

	if string(after) != string(before) {
		t.Fatal("Check rewrote the engine file instead of only reporting")
	}
}
