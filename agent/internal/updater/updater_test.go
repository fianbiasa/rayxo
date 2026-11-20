package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeSystemd records calls; active says whether the unit runs.
type fakeSystemd struct {
	calls  [][]string
	active bool
}

func (f *fakeSystemd) run(_ context.Context, program string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{program}, args...))

	if program == systemctlProgram && !f.active {
		return nil, errors.New("exit status 3")
	}

	return nil, nil
}

func newManager(t *testing.T) (*Manager, *fakeSystemd, *[]string, Config) {
	t.Helper()

	dir := t.TempDir()
	systemd := &fakeSystemd{}
	downloaded := []string{}
	cfg := Config{
		Unit:      "rayxo-update",
		LogFile:   filepath.Join(dir, "update.log"),
		StateFile: filepath.Join(dir, "update.json"),
		SetupPath: filepath.Join(dir, "update-setup.sh"),
		Run:       systemd.run,
		Download: func(_ context.Context, url, dest string) error {
			downloaded = append(downloaded, url)

			return os.WriteFile(dest, []byte("#!/bin/sh\n"), 0o700)
		},
		Now: func() time.Time { return time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC) },
	}

	return New(cfg), systemd, &downloaded, cfg
}

const goodURL = "https://github.com/fianbiasa/rayxo/releases/download/v0.9.0/setup.sh"

func TestStartDownloadsAndRunsSetupInItsOwnUnit(t *testing.T) {
	manager, systemd, downloaded, cfg := newManager(t)

	if err := os.WriteFile(cfg.LogFile, []byte("old output\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := manager.Start(context.Background(), Target{Version: "0.9.0", SetupURL: goodURL}); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(*downloaded, []string{goodURL}) {
		t.Fatalf("downloaded = %q, want one fetch of the release setup.sh", *downloaded)
	}

	want := []string{
		systemdRunProgram, "--unit=rayxo-update", "--collect", "--quiet", "--description=RAYXO: update the panel",
		"/bin/sh", "-c", script, "rayxo-update", cfg.LogFile, cfg.StateFile + ".exit",
		"/bin/bash", cfg.SetupPath, "--yes",
	}
	if got := systemd.calls[len(systemd.calls)-1]; !slices.Equal(got, want) {
		t.Fatalf("systemd-run call =\n  %q\nwant\n  %q", got, want)
	}

	if _, err := os.Stat(cfg.LogFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the previous run's output is still there: %v", err)
	}

	if info, err := os.Stat(cfg.StateFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v %v, want mode 0600", info, err)
	}

	systemd.active = true

	state, err := manager.Status(context.Background())
	if err != nil || state == nil || !state.Running || state.Version != "0.9.0" ||
		!state.StartedAt.Equal(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)) || state.ExitCode != nil {
		t.Fatalf("status while running = %+v, %v", state, err)
	}
}

func TestStartRefusesWhileUpdateRuns(t *testing.T) {
	manager, systemd, downloaded, _ := newManager(t)
	systemd.active = true

	if err := manager.Start(context.Background(), Target{Version: "0.9.0", SetupURL: goodURL}); !errors.Is(err, ErrRunning) {
		t.Fatalf("err = %v, want ErrRunning", err)
	}

	if len(*downloaded) != 0 {
		t.Fatal("setup.sh was downloaded while an update was running")
	}

	for _, call := range systemd.calls {
		if call[0] == systemdRunProgram {
			t.Fatal("systemd-run ran while an update was running")
		}
	}
}

func TestStartRejectsForeignURL(t *testing.T) {
	manager, systemd, downloaded, _ := newManager(t)

	bad := []string{
		"http://github.com/fianbiasa/rayxo/releases/download/v0.9.0/setup.sh", // not https
		"https://evil.com/fianbiasa/rayxo/releases/download/v0.9.0/setup.sh",  // wrong host
		"https://github.com/someone/else/releases/download/v0.9.0/setup.sh",   // wrong repo path
		"https://github.com/fianbiasa/rayxo/releases/download/v0.9.0/evil.sh", // not setup.sh
		"https://github.com/fianbiasa/rayxo/archive/setup.sh",                 // wrong path prefix
		"not a url at all",
	}

	for _, url := range bad {
		if err := manager.Start(context.Background(), Target{Version: "0.9.0", SetupURL: url}); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("Start(%q) err = %v, want ErrInvalidURL", url, err)
		}
	}

	if len(*downloaded) != 0 || len(systemd.calls) != 0 {
		t.Fatal("a bad URL downloaded or ran something")
	}
}

func TestStatusReportsWhyUpdateStopped(t *testing.T) {
	manager, _, _, cfg := newManager(t)

	if state, err := manager.Status(context.Background()); state != nil || err != nil {
		t.Fatalf("status before any run = %+v, %v; want nil", state, err)
	}

	if err := manager.Start(context.Background(), Target{Version: "0.9.0", SetupURL: goodURL}); err != nil {
		t.Fatal(err)
	}

	output := "==> Mengunduh paket rilis\n\n" +
		"GAGAL: checksum paket tidak cocok\n"
	if err := os.WriteFile(cfg.LogFile, []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(cfg.StateFile+".exit", []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if state.Running || state.ExitCode == nil || *state.ExitCode != 1 {
		t.Fatalf("unexpected state %+v", state)
	}

	if state.Error != "checksum paket tidak cocok" {
		t.Fatalf("Error = %q", state.Error)
	}
}

func TestValidateSetupURL(t *testing.T) {
	if err := validateSetupURL(goodURL); err != nil {
		t.Fatalf("good URL rejected: %v", err)
	}

	other := "https://github.com/fianbiasa/rayxo/releases/download/v1.0.0-rc.1/setup.sh"
	if err := validateSetupURL(other); err != nil {
		t.Fatalf("pre-release URL rejected: %v", err)
	}

	if !strings.Contains(goodURL, releasePathPrefix) {
		t.Fatal("test URL does not use the release path prefix")
	}
}
