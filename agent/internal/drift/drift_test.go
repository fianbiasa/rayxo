package drift

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareFileMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf")
	content := []byte("a\nb\nc\n")

	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	target := CompareFile("test", path, content)
	if target.Drifted {
		t.Fatalf("identical file reported as drifted: %+v", target)
	}
}

func TestCompareFileDiffers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf")

	if err := os.WriteFile(path, []byte("a\nCHANGED\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	target := CompareFile("test", path, []byte("a\nb\nc\n"))
	if !target.Drifted || target.Missing {
		t.Fatalf("changed file not reported as drifted: %+v", target)
	}

	if !strings.Contains(target.Diff, "-CHANGED") || !strings.Contains(target.Diff, "+b") {
		t.Fatalf("diff did not show the change: %q", target.Diff)
	}
}

func TestCompareFileMissing(t *testing.T) {
	target := CompareFile("test", filepath.Join(t.TempDir(), "absent"), []byte("x"))
	if !target.Drifted || !target.Missing {
		t.Fatalf("missing file not reported: %+v", target)
	}
}

func TestCompareFileNeverWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf")
	onDisk := []byte("on disk\n")

	if err := os.WriteFile(path, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}

	_ = CompareFile("test", path, []byte("expected\n"))

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(after) != string(onDisk) {
		t.Fatalf("CompareFile changed the file: %q", after)
	}
}

func TestDiffTruncates(t *testing.T) {
	var big strings.Builder
	for i := 0; i < 1000; i++ {
		big.WriteString("line\n")
	}

	out := Diff([]byte(big.String()), []byte("different\n"))
	if !strings.Contains(out, "truncated") {
		t.Fatalf("large diff was not truncated: %d bytes", len(out))
	}

	if len(out) > maxDiffBytes+64 {
		t.Fatalf("diff exceeded cap: %d bytes", len(out))
	}
}

func TestReportDrifted(t *testing.T) {
	r := Report{}
	r.Add(Target{Kind: "a", Path: "x"})
	if r.Drifted() {
		t.Fatal("clean report reported drift")
	}

	r.Add(Target{Kind: "b", Path: "y", Drifted: true})
	if !r.Drifted() {
		t.Fatal("report with a drifted target reported clean")
	}
}
