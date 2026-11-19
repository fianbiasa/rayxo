package redis

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
		ConfDir: filepath.Join(base, "etc"),
		UnitDir: filepath.Join(base, "systemd"),
		Run:     rec.run,
	})

	return m, rec
}

func selfInstance() Instance {
	return Instance{
		User:        "budi",
		UID:         os.Getuid(),
		GID:         os.Getgid(),
		Password:    "abcdEFGH12345678wxyz",
		MaxMemoryMB: 128,
	}
}

func TestProvisionWritesConfUnitAndStarts(t *testing.T) {
	m, rec := newManager(t)

	if err := m.Provision(context.Background(), selfInstance()); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	conf, err := os.ReadFile(filepath.Join(m.cfg.ConfDir, "budi.conf"))
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}

	for _, want := range []string{
		"port 0",
		"unixsocket /run/rayxo-redis/budi/redis.sock",
		"unixsocketperm 700",
		`requirepass "abcdEFGH12345678wxyz"`,
		"maxmemory 128mb",
		"maxmemory-policy allkeys-lru",
	} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("conf missing %q\n%s", want, conf)
		}
	}

	unit, err := os.ReadFile(filepath.Join(m.cfg.UnitDir, "rayxo-redis@.service"))
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	if !strings.Contains(string(unit), "User=%i") || !strings.Contains(string(unit), "Slice=rayxo-%i.slice") {
		t.Errorf("unit missing per-account fields:\n%s", unit)
	}
	// The unit must read the conf from the same directory the manager writes
	// it to, or redis-server cannot find (or read) its config.
	if !strings.Contains(string(unit), m.cfg.ConfDir+"/%i.conf") {
		t.Errorf("unit should point at the conf dir %q:\n%s", m.cfg.ConfDir, unit)
	}

	if !rec.ran("/usr/bin/systemctl daemon-reload") {
		t.Error("expected daemon-reload after writing the unit")
	}
	if !rec.ran("/usr/bin/systemctl enable --now rayxo-redis@budi") {
		t.Error("expected enable --now of the account instance")
	}
	if !rec.ran("/usr/bin/systemctl restart rayxo-redis@budi") {
		t.Error("expected restart after writing a new conf")
	}
}

func TestProvisionIsIdempotent(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	if err := m.Provision(ctx, selfInstance()); err != nil {
		t.Fatalf("first Provision: %v", err)
	}

	rec.calls = nil
	if err := m.Provision(ctx, selfInstance()); err != nil {
		t.Fatalf("second Provision: %v", err)
	}

	if rec.ran("/usr/bin/systemctl daemon-reload") {
		t.Error("unchanged unit should not daemon-reload again")
	}
	if rec.ran("/usr/bin/systemctl restart rayxo-redis@budi") {
		t.Error("unchanged conf should not restart again")
	}
	if !rec.ran("/usr/bin/systemctl enable --now rayxo-redis@budi") {
		t.Error("enable --now should still run to keep the instance up")
	}
}

func TestProvisionRestartsWhenPasswordChanges(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	if err := m.Provision(ctx, selfInstance()); err != nil {
		t.Fatalf("first Provision: %v", err)
	}

	rec.calls = nil
	changed := selfInstance()
	changed.Password = "ZZZZyyyy99998888abcd"
	if err := m.Provision(ctx, changed); err != nil {
		t.Fatalf("second Provision: %v", err)
	}

	if !rec.ran("/usr/bin/systemctl restart rayxo-redis@budi") {
		t.Error("a changed password should restart the instance")
	}
}

func TestDeprovisionStopsAndRemovesConf(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	if err := m.Provision(ctx, selfInstance()); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if err := m.Deprovision(ctx, "budi"); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}

	if !rec.ran("/usr/bin/systemctl disable --now rayxo-redis@budi") {
		t.Error("expected disable --now on deprovision")
	}
	if _, err := os.Stat(filepath.Join(m.cfg.ConfDir, "budi.conf")); !os.IsNotExist(err) {
		t.Error("conf should be removed on deprovision")
	}

	// A second deprovision (nothing left) still succeeds.
	if err := m.Deprovision(ctx, "budi"); err != nil {
		t.Fatalf("second Deprovision: %v", err)
	}
}

func TestRunningReflectsIsActive(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	running, err := m.Running(ctx, "budi")
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

	running, err = m.Running(ctx, "budi")
	if err != nil {
		t.Fatalf("Running (inactive): %v", err)
	}
	if running {
		t.Error("expected not running when is-active exits non-zero")
	}
}
