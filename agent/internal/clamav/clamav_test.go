package clamav

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rayxo/agent/internal/runner"
)

type recorder struct {
	calls   [][]string
	scanOut []byte
	scanErr error
}

func (r *recorder) run(_ context.Context, program string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{program}, args...))
	if strings.HasSuffix(program, "clamdscan") {
		return r.scanOut, r.scanErr
	}

	return nil, nil
}

func (r *recorder) ran(substr string) bool {
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), substr) {
			return true
		}
	}

	return false
}

func newManager(t *testing.T) (*Manager, *recorder) {
	t.Helper()
	base := t.TempDir()
	rec := &recorder{}

	clamd := filepath.Join(base, "clamd.conf")
	if err := os.WriteFile(clamd, []byte("LocalSocket /run/clamav/clamd.ctl\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbDir := filepath.Join(base, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A database on disk, so Apply does not run freshclam.
	if err := os.WriteFile(filepath.Join(dbDir, "daily.cld"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	unitDir := filepath.Join(base, "systemd")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	m := New(Config{
		RspamdLocalD:  filepath.Join(base, "rspamd"),
		UnitDir:       unitDir,
		ClamdConf:     clamd,
		DatabaseDir:   dbDir,
		QuarantineDir: filepath.Join(base, "quar"),
		Run:           rec.run,
	})

	return m, rec
}

func TestApplyEnablesEverything(t *testing.T) {
	m, rec := newManager(t)

	if err := m.Apply(context.Background(), Settings{Enabled: true, Email: true, OnAccess: true, Scheduled: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	conf, err := os.ReadFile(m.antivirusPath())
	if err != nil || !strings.Contains(string(conf), "/run/clamav/clamd.ctl") {
		t.Errorf("antivirus.conf missing or wrong: %v\n%s", err, conf)
	}

	clamd, _ := os.ReadFile(m.cfg.ClamdConf)
	if !strings.Contains(string(clamd), onAccessBegin) || !strings.Contains(string(clamd), "OnAccessIncludePath /home") {
		t.Errorf("clamd.conf missing on-access block:\n%s", clamd)
	}

	for _, unit := range []string{scanService, scanTimer, onAccessService} {
		if _, err := os.Stat(filepath.Join(m.cfg.UnitDir, unit)); err != nil {
			t.Errorf("unit %s not written: %v", unit, err)
		}
	}

	for _, want := range []string{
		"enable --now clamav-freshclam.service",
		"enable --now clamav-daemon.service",
		"reload-or-restart rspamd",
		"enable --now rayxo-clamav-scan.timer",
		"enable --now rayxo-clamonacc.service",
	} {
		if !rec.ran(want) {
			t.Errorf("expected systemctl %q", want)
		}
	}

	if rec.ran(freshclamProgram) {
		t.Error("the freshclam one-shot should be skipped when a database is present")
	}
}

func TestApplyDisabledRemovesEverything(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	if err := m.Apply(ctx, Settings{Enabled: true, Email: true, OnAccess: true, Scheduled: true}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	rec.calls = nil

	if err := m.Apply(ctx, Settings{Enabled: false}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if _, err := os.Stat(m.antivirusPath()); !os.IsNotExist(err) {
		t.Error("antivirus.conf should be removed when disabled")
	}

	clamd, _ := os.ReadFile(m.cfg.ClamdConf)
	if strings.Contains(string(clamd), onAccessBegin) {
		t.Errorf("on-access block should be removed:\n%s", clamd)
	}

	for _, want := range []string{
		"disable --now clamav-daemon.service",
		"disable --now rayxo-clamav-scan.timer",
		"disable --now rayxo-clamonacc.service",
	} {
		if !rec.ran(want) {
			t.Errorf("expected systemctl %q", want)
		}
	}
}

func TestOnAccessBlockRoundTrips(t *testing.T) {
	m, _ := newManager(t)
	original, _ := os.ReadFile(m.cfg.ClamdConf)

	if _, err := m.writeOnAccessBlock(true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.writeOnAccessBlock(false); err != nil {
		t.Fatal(err)
	}

	after, _ := os.ReadFile(m.cfg.ClamdConf)
	if string(after) != string(original) {
		t.Errorf("clamd.conf not restored:\nwant %q\ngot  %q", original, after)
	}
}

func TestScanReportsInfected(t *testing.T) {
	m, rec := newManager(t)
	rec.scanOut = []byte("/home/budi/x.php: Eicar-Test-Signature FOUND\n/home/budi/ok.txt: OK\n")
	rec.scanErr = &runner.Error{Program: clamdscanProgram, ExitCode: 1}

	res, err := m.Scan(context.Background(), "/home/budi")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Clean {
		t.Error("expected not clean")
	}
	if len(res.Infected) != 1 || res.Infected[0].File != "/home/budi/x.php" || res.Infected[0].Virus != "Eicar-Test-Signature" {
		t.Errorf("wrong detections: %+v", res.Infected)
	}
}

func TestScanCleanAndError(t *testing.T) {
	m, rec := newManager(t)
	ctx := context.Background()

	// exit 0, no output: clean.
	res, err := m.Scan(ctx, "/home/budi")
	if err != nil || !res.Clean || len(res.Infected) != 0 {
		t.Errorf("expected clean: %+v %v", res, err)
	}

	// exit 2: a real error.
	rec.scanErr = &runner.Error{Program: clamdscanProgram, ExitCode: 2}
	if _, err := m.Scan(ctx, "/home/budi"); err == nil {
		t.Error("expected an error on clamdscan exit 2")
	}
}
