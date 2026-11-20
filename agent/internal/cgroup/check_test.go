package cgroup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckReportsNoDriftAfterApply(t *testing.T) {
	m, _, _ := newManager(t)
	limits := Limits{CPUPercent: 150, MemoryMB: 512, Tasks: 100, IOWeight: 50}

	if err := m.Apply(context.Background(), "budi", 1001, limits); err != nil {
		t.Fatal(err)
	}

	report, err := m.Check("budi", 1001, limits)
	if err != nil {
		t.Fatal(err)
	}

	if report.Drifted() {
		t.Fatalf("freshly applied limits reported drift: %+v", report.Targets)
	}
}

func TestCheckDetectsAnEditedSlice(t *testing.T) {
	m, _, dir := newManager(t)
	limits := Limits{CPUPercent: 150, MemoryMB: 512, Tasks: 100, IOWeight: 50}

	if err := m.Apply(context.Background(), "budi", 1001, limits); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, sliceName("budi"))
	edited := read(t, path) + "# hand edit\n"

	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := m.Check("budi", 1001, limits)
	if err != nil {
		t.Fatal(err)
	}

	if !report.Drifted() {
		t.Fatal("an edited slice unit was not reported as drifted")
	}

	if read(t, path) != edited {
		t.Fatal("Check rewrote the slice instead of only reporting")
	}
}

func TestCheckReportsAbsentFilesForAnAccountWithoutLimits(t *testing.T) {
	m, _, _ := newManager(t)

	report, err := m.Check("budi", 1001, Limits{})
	if err != nil {
		t.Fatal(err)
	}

	if report.Drifted() {
		t.Fatalf("an account with no limits and no slice files reported drift: %+v", report.Targets)
	}
}
