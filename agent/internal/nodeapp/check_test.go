package nodeapp

import (
	"context"
	"os"
	"strings"
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

// The env file holds the account's own secrets, and its drift diff is shown in
// the panel, so a changed secret value must be reported without the diff ever
// revealing the old or new value.
func TestCheckMasksAccountSecretsInTheEnvDiff(t *testing.T) {
	m, _ := newManager(t)
	app := selfApp() // APP_KEY=secret among the account's own variables

	if err := m.Provision(context.Background(), app); err != nil {
		t.Fatal(err)
	}

	path := m.envPath(app.Domain)

	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	tampered := "TAMPERED-key-7777"
	edited := strings.Replace(string(original), "APP_KEY=secret", "APP_KEY="+tampered, 1)

	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := m.Check(app)
	if err != nil {
		t.Fatal(err)
	}

	if !report.Drifted() {
		t.Fatal("an edited secret was not reported as drift")
	}

	for _, target := range report.Targets {
		if strings.Contains(target.Diff, "secret") || strings.Contains(target.Diff, tampered) {
			t.Fatalf("drift diff leaked an account secret: %q", target.Diff)
		}
	}
}
