// Package drift compares what a config file should contain with what is on
// disk, so the panel can report configuration that changed outside RAYXO
// without rewriting it. It never writes anything.
package drift

import (
	"bytes"
	"os"
	"strings"
)

const (
	// maxDiffLines caps a reported diff so one large file cannot bloat the
	// result sent back to the panel.
	maxDiffLines = 200
	// maxDiffBytes caps a reported diff by size for the same reason.
	maxDiffBytes = 8 << 10
)

// Target is one config file (or other config target) checked for drift.
type Target struct {
	// Kind groups targets (e.g. "nginx-vhost", "php-pool", "dns-zone").
	Kind string `json:"kind"`
	// Path identifies the target: a file path, or a logical name for
	// non-file targets like a DNS zone.
	Path string `json:"path"`
	// Drifted is true when the target differs from what RAYXO would write.
	Drifted bool `json:"drifted"`
	// Missing is true when the target does not exist on disk at all.
	Missing bool `json:"missing"`
	// Diff is a short, bounded description of the difference, empty when not
	// drifted.
	Diff string `json:"diff"`
}

// Report is the set of targets a check looked at.
type Report struct {
	Targets []Target `json:"targets"`
}

// Add appends a target to the report.
func (r *Report) Add(t Target) { r.Targets = append(r.Targets, t) }

// Drifted reports whether any target drifted.
func (r Report) Drifted() bool {
	for _, t := range r.Targets {
		if t.Drifted {
			return true
		}
	}

	return false
}

// CompareFile checks the file at path against the content RAYXO would write
// there. It reads the file but never writes.
func CompareFile(kind, path string, expected []byte) Target {
	actual, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Target{Kind: kind, Path: path, Drifted: true, Missing: true, Diff: "file is missing"}
		}

		// An unreadable file is reported as drifted with the reason, not as
		// an error that would abort the whole scan.
		return Target{Kind: kind, Path: path, Drifted: true, Diff: "could not read file: " + err.Error()}
	}

	if bytes.Equal(actual, expected) {
		return Target{Kind: kind, Path: path}
	}

	return Target{Kind: kind, Path: path, Drifted: true, Diff: Diff(expected, actual)}
}

// CompareAbsent checks that path does NOT exist, for config RAYXO would have
// removed (an account without limits, a disabled service). A file still there
// is drift; a missing file is clean. It never writes.
func CompareAbsent(kind, path string) Target {
	if _, err := os.Stat(path); err == nil {
		return Target{Kind: kind, Path: path, Drifted: true, Diff: "file is present but should have been removed"}
	}

	return Target{Kind: kind, Path: path}
}

// Compare checks arbitrary expected vs. actual content (for non-file targets,
// e.g. a rendered DNS zone), returning a target named by kind and name.
func Compare(kind, name string, expected, actual []byte) Target {
	if bytes.Equal(actual, expected) {
		return Target{Kind: kind, Path: name}
	}

	return Target{Kind: kind, Path: name, Drifted: true, Diff: Diff(expected, actual)}
}

// Diff returns a short line-based diff of actual vs. expected: lines only on
// disk are prefixed "-", lines only in the expected content "+". It trims the
// common prefix and suffix so the output shows just the change, and is bounded
// by maxDiffLines and maxDiffBytes.
func Diff(expected, actual []byte) string {
	want := strings.Split(string(expected), "\n")
	have := strings.Split(string(actual), "\n")

	// Trim the common prefix.
	start := 0
	for start < len(want) && start < len(have) && want[start] == have[start] {
		start++
	}

	// Trim the common suffix (not past the prefix already trimmed).
	we, he := len(want), len(have)
	for we > start && he > start && want[we-1] == have[he-1] {
		we--
		he--
	}

	var b strings.Builder
	lines := 0

	add := func(prefix, line string) bool {
		if lines >= maxDiffLines || b.Len()+len(line)+2 > maxDiffBytes {
			b.WriteString("… (diff truncated)\n")

			return false
		}

		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteByte('\n')
		lines++

		return true
	}

	for i := start; i < he; i++ {
		if !add("-", have[i]) {
			return b.String()
		}
	}

	for i := start; i < we; i++ {
		if !add("+", want[i]) {
			return b.String()
		}
	}

	return b.String()
}
