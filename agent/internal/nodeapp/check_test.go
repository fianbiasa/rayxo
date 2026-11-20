package nodeapp

import (
	"context"
	"os"
	"testing"
)

func TestCheckReportsNoDriftAfterProvision(t *testing.T) {
	m, _ := newManager(t)
	app := selfApp()

	if err := m.Provision(context.Background(), app); err != nil {
		t.Fatal(err)
	}

	report, err := m.Check(app)
	if err != nil {
		t.Fatal(err)
	}

	if report.Drifted() {
		t.Fatalf("a freshly provisioned app reported drift: %+v", report.Targets)
	}
}

func TestCheckDetectsAnEditedEnvFile(t *testing.T) {
	m, _ := newManager(t)
	app := selfApp()

	if err := m.Provision(context.Background(), app); err != nil {
		t.Fatal(err)
	}

	path := m.envPath(app.Domain)

	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	edited := string(original) + "EXTRA=1\n"

	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := m.Check(app)
	if err != nil {
		t.Fatal(err)
	}

	if !report.Drifted() {
		t.Fatal("an edited environment file was not reported as drifted")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(after) != edited {
		t.Fatal("Check rewrote the environment file instead of only reporting")
	}
}
