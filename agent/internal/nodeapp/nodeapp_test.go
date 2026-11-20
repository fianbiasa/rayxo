package nodeapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rayxo/agent/internal/runner"
)

// recorder captures the systemctl calls a Manager makes and can be told to
// fail a specific command with an exit code (for is-active).
type recorder struct {
	calls [][]string
	fail  func(args []string) error
}

func (r *recorder) run(_ context.Context, program string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{program}, args...))
	if r.fail != nil {
		if err := r.fail(args); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func (r *recorder) ran(want string) bool {
	for _, call := range r.calls {
		if strings.Join(call, " ") == want {
			return true
		}
	}

	return false
}

func newManager(t *testing.T) (*Manager, *recorder) {
	t.Helper()
	base := t.TempDir()
	rec := &recorder{}
	m := New(Config{
		ConfDir:    filepath.Join(base, "etc"),
		UnitDir:    filepath.Join(base, "systemd"),
		NodeBinary: "/usr/bin/node",
		Run:        rec.run,
	})

	return m, rec
}

func selfApp() App {
	return App{
		User:    "budi",
		UID:     os.Getuid(),
		GID:     os.Getgid(),
		Domain:  "example.com",
		WorkDir: "/home/budi/domains/example.com/public_html",
		Entry:   ".output/server/index.mjs",
		Env:     map[string]string{"DATABASE_URL": "mysql://u:p@127.0.0.1/db", "APP_KEY": "secret"},
		Port:    40123,
	}
}

func TestProvisionWritesEnvUnitAndStarts(t *testing.T) {
	m, rec := newManager(t)

	if err := m.Provision(context.Background(), selfApp()); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	env, err := os.ReadFile(filepath.Join(m.cfg.ConfDir, "example.com.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}

	for _, want := range []string{
		"DATABASE_URL=mysql://u:p@127.0.0.1/db",
		"APP_KEY=secret",
		"NODE_ENV=production",
		"HOST=127.0.0.1",
		"PORT=40123",
	} {
		if !strings.Contains(string(env), want) {
			t.Errorf("env missing %q\n%s", want, env)
		}
	}

	// Our PORT/HOST/NODE_ENV must come after the account's own vars so they win.
	if strings.Index(string(env), "PORT=40123") < strings.Index(string(env), "DATABASE_URL=") {
		t.Error("PORT should be written after the account's own variables")
	}

	unit, err := os.ReadFile(filepath.Join(m.cfg.UnitDir, "rayxo-nodeapp-example.com.service"))
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}

	for _, want := range []string{
		"User=budi",
		"Group=budi",
		"Slice=rayxo-budi.slice",
		"WorkingDirectory=/home/budi/domains/example.com/public_html",
		"ExecStart=/usr/bin/node .output/server/index.mjs",
		"EnvironmentFile=" + m.cfg.ConfDir + "/example.com.env",
	} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("unit missing %q\n%s", want, unit)
		}
	}

	if !rec.ran("/usr/bin/systemctl daemon-reload") {
		t.Error("expected daemon-reload after writing the unit")
	}
	if !rec.ran("/usr/bin/systemctl enable --now rayxo-nodeapp-example.com.service") {
		t.Error("expected enable --now of the app service")
	}
	if !rec.ran("/usr/bin/systemctl restart rayxo-nodeapp-example.com.service") {
		t.Error("expected restart after writing a new env/unit")
	}
}

func TestProvisionIsIdempotent(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	if err := m.Provision(ctx, selfApp()); err != nil {
		t.Fatalf("first Provision: %v", err)
	}

	rec.calls = nil
	if err := m.Provision(ctx, selfApp()); err != nil {
		t.Fatalf("second Provision: %v", err)
	}

	if rec.ran("/usr/bin/systemctl daemon-reload") {
		t.Error("unchanged unit should not daemon-reload again")
	}
	if rec.ran("/usr/bin/systemctl restart rayxo-nodeapp-example.com.service") {
		t.Error("unchanged env/unit should not restart again")
	}
	if !rec.ran("/usr/bin/systemctl enable --now rayxo-nodeapp-example.com.service") {
		t.Error("enable --now should still run to keep the app up")
	}
}

func TestProvisionRestartsWhenEnvChanges(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	if err := m.Provision(ctx, selfApp()); err != nil {
		t.Fatalf("first Provision: %v", err)
	}

	rec.calls = nil
	changed := selfApp()
	changed.Env = map[string]string{"DATABASE_URL": "mysql://u:p@127.0.0.1/db", "APP_KEY": "rotated"}
	if err := m.Provision(ctx, changed); err != nil {
		t.Fatalf("second Provision: %v", err)
	}

	if !rec.ran("/usr/bin/systemctl restart rayxo-nodeapp-example.com.service") {
		t.Error("a changed env should restart the app")
	}
}

func TestDeprovisionStopsAndRemoves(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	if err := m.Provision(ctx, selfApp()); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if err := m.Deprovision(ctx, "example.com"); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}

	if !rec.ran("/usr/bin/systemctl disable --now rayxo-nodeapp-example.com.service") {
		t.Error("expected disable --now on deprovision")
	}
	if _, err := os.Stat(filepath.Join(m.cfg.ConfDir, "example.com.env")); !os.IsNotExist(err) {
		t.Error("env should be removed on deprovision")
	}
	if _, err := os.Stat(filepath.Join(m.cfg.UnitDir, "rayxo-nodeapp-example.com.service")); !os.IsNotExist(err) {
		t.Error("unit should be removed on deprovision")
	}

	// A second deprovision (nothing left) still succeeds.
	if err := m.Deprovision(ctx, "example.com"); err != nil {
		t.Fatalf("second Deprovision: %v", err)
	}
}

func TestRunningReflectsIsActive(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	running, err := m.Running(ctx, "example.com")
	if err != nil {
		t.Fatalf("Running (active): %v", err)
	}
	if !running {
		t.Error("expected running when is-active succeeds")
	}

	rec.fail = func(args []string) error {
		if len(args) > 0 && args[0] == "is-active" {
			return &runner.Error{Program: "/usr/bin/systemctl", ExitCode: 3}
		}

		return nil
	}

	running, err = m.Running(ctx, "example.com")
	if err != nil {
		t.Fatalf("Running (inactive): %v", err)
	}
	if running {
		t.Error("expected not running when is-active exits non-zero")
	}
}
