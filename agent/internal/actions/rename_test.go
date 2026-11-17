package actions

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRenameNoReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	if err := os.WriteFile(src, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Into a free name it moves the file.
	if err := renameNoReplace(src, dst); err != nil {
		t.Fatalf("renameNoReplace into a free name: %v", err)
	}
	if _, err := os.Lstat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old name is still there: %v", err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "a" {
		t.Errorf("destination = %q, %v; want \"a\"", b, err)
	}

	// Onto a taken name it refuses (fs.ErrExist) and does not overwrite it.
	if err := os.WriteFile(src, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameNoReplace(src, dst); !errors.Is(err, os.ErrExist) {
		t.Fatalf("renameNoReplace onto a taken name = %v; want fs.ErrExist", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "a" {
		t.Errorf("destination was overwritten: %q", b)
	}
}
