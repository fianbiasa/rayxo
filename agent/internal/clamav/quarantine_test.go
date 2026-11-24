package clamav

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rayxo/agent/internal/runner"
)

// quarantineOne scans a freshly written infected file and returns its id.
func quarantineOne(t *testing.T, m *Manager, rec *recorder) (string, string) {
	t.Helper()
	home := t.TempDir()
	victim := filepath.Join(home, "x.php")
	if err := os.WriteFile(victim, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec.scanOut = []byte(victim + ": Eicar-Test-Signature FOUND\n")
	rec.scanErr = &runner.Error{Program: clamdscanProgram, ExitCode: 1}

	if _, err := m.ScanAndQuarantine(context.Background(), home, sourceOnDemand); err != nil {
		t.Fatalf("ScanAndQuarantine: %v", err)
	}

	entries, err := m.QuarantineList()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 quarantined, got %d", len(entries))
	}

	return entries[0].ID, victim
}

func TestScanAndQuarantineMovesInfected(t *testing.T) {
	m, rec := newManager(t)
	id, victim := quarantineOne(t, m, rec)

	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Error("infected file should have been moved out of its home")
	}

	entries, _ := m.QuarantineList()
	e := entries[0]
	if e.OriginalPath != victim || e.Virus != "Eicar-Test-Signature" || e.Source != sourceOnDemand {
		t.Errorf("wrong entry: %+v", e)
	}
	if _, err := os.Stat(m.filePath(id)); err != nil {
		t.Errorf("quarantined file missing from the store: %v", err)
	}
}

func TestQuarantineRestorePutsFileBack(t *testing.T) {
	m, rec := newManager(t)
	id, victim := quarantineOne(t, m, rec)

	if err := m.QuarantineRestore(id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("file not restored to %s: %v", victim, err)
	}
	if _, err := os.Stat(m.sidecarPath(id)); !os.IsNotExist(err) {
		t.Error("sidecar should be gone after restore")
	}

	if entries, _ := m.QuarantineList(); len(entries) != 0 {
		t.Errorf("quarantine should be empty after restore, got %d", len(entries))
	}
}

func TestQuarantineRestoreRefusesOverwrite(t *testing.T) {
	m, rec := newManager(t)
	id, victim := quarantineOne(t, m, rec)

	// Something new is already at the original path.
	if err := os.WriteFile(victim, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.QuarantineRestore(id); err == nil {
		t.Error("restore should refuse to overwrite an existing file")
	}
	// The quarantined copy stays put.
	if _, err := os.Stat(m.filePath(id)); err != nil {
		t.Errorf("quarantined file should remain after a refused restore: %v", err)
	}
}

func TestQuarantineDeleteRemovesEverything(t *testing.T) {
	m, rec := newManager(t)
	id, _ := quarantineOne(t, m, rec)

	if err := m.QuarantineDelete(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(m.filePath(id)); !os.IsNotExist(err) {
		t.Error("quarantined file should be gone")
	}
	if _, err := os.Stat(m.sidecarPath(id)); !os.IsNotExist(err) {
		t.Error("sidecar should be gone")
	}
	if entries, _ := m.QuarantineList(); len(entries) != 0 {
		t.Errorf("quarantine should be empty, got %d", len(entries))
	}
}

func TestReconcileImportsStagedWithProvenance(t *testing.T) {
	m, _ := newManager(t)

	// Simulate the scheduled scanner: a moved file in staging and a log that
	// records where it came from.
	if err := os.MkdirAll(m.stagingDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(m.stagingDir(), "x.php")
	if err := os.WriteFile(staged, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(m.scanLogPath()), 0o750); err != nil {
		t.Fatal(err)
	}
	log := "/home/budi/x.php: Eicar-Test-Signature FOUND\n" +
		"/home/budi/x.php: moved to '" + staged + "'\n"
	if err := os.WriteFile(m.scanLogPath(), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := m.QuarantineList()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 reconciled, got %d", len(entries))
	}
	e := entries[0]
	if e.OriginalPath != "/home/budi/x.php" || e.Owner != "budi" || e.Virus != "Eicar-Test-Signature" || e.Source != sourceScheduled {
		t.Errorf("wrong reconciled entry: %+v", e)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("staged file should have been moved into the store")
	}

	// A second list must not import it again.
	if entries, _ := m.QuarantineList(); len(entries) != 1 {
		t.Errorf("reconcile double-imported: got %d", len(entries))
	}
}

func TestReconcileKeepsUnknownOrigin(t *testing.T) {
	m, _ := newManager(t)
	if err := os.MkdirAll(m.stagingDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	// A staged file with no matching log line.
	if err := os.WriteFile(filepath.Join(m.stagingDir(), "orphan"), []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := m.QuarantineList()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1, got %d", len(entries))
	}
	if entries[0].OriginalPath != "" || entries[0].Virus != "unknown" {
		t.Errorf("unknown-origin entry wrong: %+v", entries[0])
	}
}

func TestOwnerFromPath(t *testing.T) {
	cases := map[string]string{
		"/home/budi/x.php":  "budi",
		"/home/budi":        "budi",
		"/home/budi/a/b/c":  "budi",
		"/etc/passwd":       "",
		"relative/path/x":   "",
		"/var/www/site.com": "",
	}
	for in, want := range cases {
		if got := ownerFromPath(in); got != want {
			t.Errorf("ownerFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateIDRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"", "../etc/passwd", "abc/def", "ABCZ", "a.json", strings.Repeat("a", 65)} {
		if validateID(bad) == nil {
			t.Errorf("validateID(%q) should fail", bad)
		}
	}
	if err := validateID("a1b2c3d4e5f6"); err != nil {
		t.Errorf("a valid hex id was rejected: %v", err)
	}
}
