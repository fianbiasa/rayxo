package redis

import (
	"context"
	"os"
	"testing"
)

func TestCheckReportsNoDriftAfterProvision(t *testing.T) {
	m, _ := newManager(t)
	in := selfInstance()

	if err := m.Provision(context.Background(), in); err != nil {
		t.Fatal(err)
	}

	report, err := m.Check(in)
	if err != nil {
		t.Fatal(err)
	}

	if report.Drifted() {
		t.Fatalf("a freshly provisioned instance reported drift: %+v", report.Targets)
	}
}

func TestCheckDetectsAnEditedConf(t *testing.T) {
	m, _ := newManager(t)
	in := selfInstance()

	if err := m.Provision(context.Background(), in); err != nil {
		t.Fatal(err)
	}

	path := m.confPath(in.User)

	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	edited := string(original) + "\n# hand edit\n"

	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := m.Check(in)
	if err != nil {
		t.Fatal(err)
	}

	if !report.Drifted() {
		t.Fatal("an edited Redis config was not reported as drifted")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(after) != edited {
		t.Fatal("Check rewrote the config instead of only reporting")
	}
}
