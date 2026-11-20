// Package updater updates the panel by running a newer release's setup.sh.
// setup.sh restarts the agent on the way, so it runs in a systemd unit of its
// own (systemd-run), never as the agent's child; its output and exit code go
// to files the agent reads back for the panel. The new setup.sh is downloaded
// from a GitHub release first, so the new release's own install steps run (the
// installed setup.sh would miss steps a newer release added).
package updater

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rayxo/agent/internal/runner"
)

const (
	systemdRunProgram = "/usr/bin/systemd-run"
	systemctlProgram  = "/usr/bin/systemctl"

	// logLines is how much of setup.sh's output Status returns.
	logLines = 40
	// failurePrefix starts the line setup.sh's die() prints.
	failurePrefix = "GAGAL: "

	// downloadTimeout bounds fetching the release's setup.sh.
	downloadTimeout = 2 * time.Minute
	// maxSetupBytes caps the downloaded setup.sh (it is ~180 KB).
	maxSetupBytes = 8 << 20

	// releasePathPrefix is the only path a setup.sh may be fetched from.
	releaseHost       = "github.com"
	releasePathPrefix = "/fianbiasa/rayxo/releases/download/"
)

// script runs setup.sh (the arguments after the log and exit file), its output
// to the log and its exit code to the exit file, both readable by root only.
// The values arrive as positional parameters, never inside the script text.
const script = `umask 077; log=$1; exit_file=$2; shift 2; "$@" >"$log" 2>&1; echo $? >"$exit_file"`

// ErrRunning means an update is already running from an earlier request.
var ErrRunning = errors.New("an update is already running")

// ErrInvalidURL means the setup.sh URL is not a RAYXO GitHub release URL.
var ErrInvalidURL = errors.New("the setup.sh URL is not a RAYXO release URL")

// Config holds where the update is recorded and how it runs.
type Config struct {
	// Unit names the transient systemd service setup.sh runs in.
	Unit string
	// LogFile receives setup.sh's output; StateFile records what was asked and
	// when, and StateFile+".exit" setup.sh's exit code.
	LogFile   string
	StateFile string
	// SetupPath is where the downloaded setup.sh is written before it runs.
	SetupPath string
	// Run runs a program without a shell, like runner.Run.
	Run func(ctx context.Context, program string, args ...string) ([]byte, error)
	// Download fetches url into dest (root-only); nil uses an HTTPS download.
	Download func(ctx context.Context, url, dest string) error
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
}

// DefaultConfig returns the locations and operations used on a server.
func DefaultConfig() Config {
	return Config{
		Unit:      "rayxo-update",
		LogFile:   "/var/log/rayxo/update.log",
		StateFile: "/var/lib/rayxo/update.json",
		SetupPath: "/var/lib/rayxo/update-setup.sh",
		Run:       runner.Run,
		Download:  httpDownload,
	}
}

// Target is the release to update to: its version and the setup.sh to run.
type Target struct {
	Version  string `json:"version"`
	SetupURL string `json:"setup_url"`
}

// State is the latest update run.
type State struct {
	// Version is the release this run is updating to.
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
	Running   bool      `json:"running"`
	// ExitCode is nil while setup.sh runs, or when its end was not recorded.
	ExitCode *int `json:"exit_code"`
	// Error is why setup.sh stopped, from its last "GAGAL:" line.
	Error string `json:"error"`
	// Log is the end of setup.sh's output.
	Log []string `json:"log"`
}

// Manager downloads a release's setup.sh, runs it, and reports on it.
type Manager struct {
	cfg Config
}

// New returns a Manager for cfg. It does not touch the system.
func New(cfg Config) *Manager {
	if cfg.Run == nil {
		cfg.Run = runner.Run
	}

	if cfg.Download == nil {
		cfg.Download = httpDownload
	}

	return &Manager{cfg: cfg}
}

// Start updates the panel to target in the background: it downloads the
// release's setup.sh and runs it in a transient unit. It refuses while an
// earlier update is still going (ErrRunning) or when the URL is not a RAYXO
// release URL (ErrInvalidURL).
func (m *Manager) Start(ctx context.Context, target Target) error {
	if err := validateSetupURL(target.SetupURL); err != nil {
		return err
	}

	if m.running(ctx) {
		return ErrRunning
	}

	if err := m.cfg.Download(ctx, target.SetupURL, m.cfg.SetupPath); err != nil {
		return fmt.Errorf("download setup.sh: %w", err)
	}

	for _, path := range []string{m.cfg.LogFile, m.exitFile()} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}

	state, err := json.Marshal(State{Version: target.Version, StartedAt: m.now().UTC()})
	if err != nil {
		return err
	}

	if err := os.WriteFile(m.cfg.StateFile, state, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", m.cfg.StateFile, err)
	}

	// setup.sh needs bash (it uses "set -o pipefail"), so run it with /bin/bash,
	// not /bin/sh (which is dash on Debian/Ubuntu).
	args := []string{
		"--unit=" + m.cfg.Unit, "--collect", "--quiet", "--description=RAYXO: update the panel",
		"/bin/sh", "-c", script, m.cfg.Unit, m.cfg.LogFile, m.exitFile(), "/bin/bash", m.cfg.SetupPath, "--yes",
	}

	if _, err := m.cfg.Run(ctx, systemdRunProgram, args...); err != nil {
		return fmt.Errorf("start setup.sh: %w", err)
	}

	return nil
}

// Status reports the latest update, or nil when none ran from here.
func (m *Manager) Status(ctx context.Context) (*State, error) {
	content, err := os.ReadFile(m.cfg.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read %s: %w", m.cfg.StateFile, err)
	}

	var state State
	if err := json.Unmarshal(content, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", m.cfg.StateFile, err)
	}

	state.Running = m.running(ctx)
	state.Log = tail(m.cfg.LogFile, logLines)

	for _, line := range state.Log {
		if message, ok := strings.CutPrefix(line, failurePrefix); ok {
			state.Error = message
		}
	}

	if code, err := os.ReadFile(m.exitFile()); err == nil && !state.Running {
		if value, err := strconv.Atoi(strings.TrimSpace(string(code))); err == nil {
			state.ExitCode = &value
		}
	}

	return &state, nil
}

// Running reports whether an update is still running.
func (m *Manager) Running(ctx context.Context) bool {
	return m.running(ctx)
}

// running reports whether the update's unit is still active.
func (m *Manager) running(ctx context.Context) bool {
	_, err := m.cfg.Run(ctx, systemctlProgram, "is-active", "--quiet", m.cfg.Unit+".service")

	return err == nil
}

func (m *Manager) exitFile() string {
	return m.cfg.StateFile + ".exit"
}

func (m *Manager) now() time.Time {
	if m.cfg.Now != nil {
		return m.cfg.Now()
	}

	return time.Now()
}

// validateSetupURL accepts only a RAYXO release's setup.sh over HTTPS, so the
// panel cannot make the agent fetch and run an arbitrary script.
func validateSetupURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ErrInvalidURL
	}

	if parsed.Scheme != "https" || parsed.Host != releaseHost {
		return ErrInvalidURL
	}

	if !strings.HasPrefix(parsed.Path, releasePathPrefix) || path.Base(parsed.Path) != "setup.sh" {
		return ErrInvalidURL
	}

	return nil
}

// httpDownload fetches url into dest atomically, owned by the writer (root)
// and readable by root only, capped at maxSetupBytes.
func httpDownload(ctx context.Context, rawURL, dest string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", response.StatusCode)
	}

	temp, err := os.CreateTemp(filepath.Dir(dest), ".update-setup-*.sh")
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			_ = os.Remove(temp.Name())
		}
	}()

	if err = temp.Chmod(0o700); err != nil {
		_ = temp.Close()

		return err
	}

	if _, err = io.Copy(temp, io.LimitReader(response.Body, maxSetupBytes)); err != nil {
		_ = temp.Close()

		return err
	}

	if err = temp.Close(); err != nil {
		return err
	}

	return os.Rename(temp.Name(), dest)
}

// tail returns the last n non-empty lines of the file at path, each at most
// 300 bytes; a missing file has none.
func tail(path string, n int) []string {
	file, err := os.Open(path)
	if err != nil {
		return []string{}
	}
	defer file.Close()

	lines := []string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		if len(line) > 300 {
			line = line[:300]
		}

		lines = append(lines, line)
		if len(lines) > n {
			lines = lines[1:]
		}
	}

	return lines
}
