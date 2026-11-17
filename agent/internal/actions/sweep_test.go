package actions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepStaleTemps(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}

		return path
	}

	stale := write(".create-123.tmp", staleTransferAge+time.Hour)
	stalePart := write(".remote-budi.part", staleTransferAge+time.Hour)
	fresh := write(".convert-456.tmp", time.Minute)
	backup := write("budi-2026-11-18-020000.tar.gz", staleTransferAge+time.Hour)

	sweepStaleTemps(dir)

	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Errorf("a stale temp was kept: %v", err)
	}
	if _, err := os.Lstat(stalePart); !os.IsNotExist(err) {
		t.Errorf("a stale .part was kept: %v", err)
	}
	if _, err := os.Lstat(fresh); err != nil {
		t.Errorf("a fresh temp was removed: %v", err)
	}
	if _, err := os.Lstat(backup); err != nil {
		t.Errorf("a real backup was removed: %v", err)
	}
}
