// Package clamav manages ClamAV for the panel: the on-demand, scheduled,
// email and on-access scanning the admin turns on from Server Settings.
//
// clamd loads a large signature database into memory, so it runs only while
// ClamAV is enabled. Enabling it downloads the database (freshclam) if it is
// missing, then starts clamd and the updater. Email scanning is wired into
// Rspamd (the antivirus module → clamd). The scheduled scan is a systemd
// timer that moves infected files to quarantine. On-access scanning adds
// OnAccess directives to clamd.conf (a RAYXO-marked block) and runs clamonacc.
package clamav

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rayxo/agent/internal/runner"
)

const (
	systemctlProgram = "/usr/bin/systemctl"
	freshclamProgram = "/usr/bin/freshclam"
	clamdscanProgram = "/usr/bin/clamdscan"
	rspamadmProgram  = "/usr/bin/rspamadm"

	clamdService     = "clamav-daemon.service"
	clamdSocket      = "clamav-daemon.socket"
	freshclamService = "clamav-freshclam.service"
	scanTimer        = "rayxo-clamav-scan.timer"
	scanService      = "rayxo-clamav-scan.service"
	onAccessService  = "rayxo-clamonacc.service"

	onAccessBegin = "# BEGIN RAYXO on-access (managed by rayxod)"
	onAccessEnd   = "# END RAYXO on-access"
)

// Quarantine layout (production paths; the unit templates are hardcoded, while
// the Manager derives the same paths from Config.QuarantineDir so tests can use
// a temp dir). Items live in the quarantine root as "<id>" plus "<id>.json"
// (the metadata sidecar). The scheduled and on-access scanners move infected
// files into staging/ and log each move; the agent reconciles staging into the
// index, recovering each file's original path and virus from the log.
const (
	prodQuarantineDir = "/var/lib/rayxo/clamav/quarantine"
	prodStagingDir    = prodQuarantineDir + "/staging"
	prodLogDir        = prodQuarantineDir + "/logs"
	prodScanLog       = prodLogDir + "/scan.log"
	prodOnAccessLog   = prodLogDir + "/onaccess.log"

	stagingName = "staging"
	logsName    = "logs"

	// The source that put a file in quarantine.
	sourceOnDemand  = "on-demand"
	sourceScheduled = "scheduled"
	sourceOnAccess  = "on-access"
)

// rspamdAntivirusConf wires Rspamd's antivirus module to clamd over its
// local socket, so inbound and outbound mail is scanned by the milter.
const rspamdAntivirusConf = `# Written by rayxod; changes are overwritten.
clamav {
  type = "clamav";
  servers = "/run/clamav/clamd.ctl";
  scan_mime_parts = true;
  symbol = "CLAM_VIRUS";
  action = "reject";
  message = "Lampiran ditolak: terdeteksi virus (${VIRUS}).";
}
`

// scanServiceUnit scans every account home on a timer, moving infected files
// to the quarantine staging area and logging each move, so the agent can
// reconcile them into the quarantine index with their original path and virus.
// Exit status 1 (viruses found) is still a successful run.
const scanServiceUnit = `# Written by rayxod; changes are overwritten.
[Unit]
Description=RAYXO ClamAV scheduled scan of /home
After=clamav-daemon.service
Requires=clamav-daemon.service
ConditionPathExistsGlob=/var/lib/clamav/*.c?d

[Service]
Type=oneshot
ExecStartPre=/usr/bin/install -d -m 0750 ` + prodStagingDir + `
ExecStartPre=/usr/bin/install -d -m 0750 ` + prodLogDir + `
ExecStart=/usr/bin/clamdscan --fdpass --multiscan --move=` + prodStagingDir + ` --log=` + prodScanLog + ` /home
SuccessExitStatus=0 1
Nice=15
IOSchedulingClass=idle
`

const scanTimerUnit = `# Written by rayxod; changes are overwritten.
[Unit]
Description=RAYXO ClamAV scheduled scan

[Timer]
OnCalendar=*-*-* 03:30:00
RandomizedDelaySec=1800
Persistent=true

[Install]
WantedBy=timers.target
`

// onAccessServiceUnit runs clamonacc, which watches /home through fanotify and
// moves infected files to quarantine. It reads the OnAccess directives from
// clamd.conf (see onAccessBlock).
const onAccessServiceUnit = `# Written by rayxod; changes are overwritten.
[Unit]
Description=RAYXO ClamAV on-access scanning of /home
After=clamav-daemon.service
Requires=clamav-daemon.service

[Service]
ExecStartPre=/usr/bin/install -d -m 0750 ` + prodStagingDir + `
ExecStartPre=/usr/bin/install -d -m 0750 ` + prodLogDir + `
ExecStart=/usr/sbin/clamonacc --foreground --move=` + prodStagingDir + ` --log=` + prodOnAccessLog + `
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

// onAccessBlock is appended to clamd.conf while on-access is on. Prevention is
// off (scan and move, never block a file open) so it cannot lock out a site.
const onAccessBlock = onAccessBegin + `
OnAccessIncludePath /home
OnAccessPrevention no
OnAccessExtraScanning yes
OnAccessExcludeUname clamav
OnAccessExcludeRootUID yes
` + onAccessEnd + "\n"

// Config sets where the manager writes files and how it runs programs.
type Config struct {
	RspamdLocalD  string // /etc/rspamd/local.d
	UnitDir       string // /etc/systemd/system
	ClamdConf     string // /etc/clamav/clamd.conf
	DatabaseDir   string // /var/lib/clamav
	QuarantineDir string // /var/lib/rayxo/clamav/quarantine
	Run           func(ctx context.Context, program string, args ...string) ([]byte, error)
}

// DefaultConfig is the configuration for the real server.
func DefaultConfig() Config {
	return Config{
		RspamdLocalD:  "/etc/rspamd/local.d",
		UnitDir:       "/etc/systemd/system",
		ClamdConf:     "/etc/clamav/clamd.conf",
		DatabaseDir:   "/var/lib/clamav",
		QuarantineDir: "/var/lib/rayxo/clamav/quarantine",
		Run:           runner.Run,
	}
}

// Manager turns ClamAV and its scan modes on and off and runs scans.
type Manager struct{ cfg Config }

// New makes a Manager from cfg.
func New(cfg Config) *Manager {
	if cfg.Run == nil {
		cfg.Run = runner.Run
	}

	return &Manager{cfg: cfg}
}

// Settings is the whole ClamAV state the panel asks for.
type Settings struct {
	Enabled   bool `json:"enabled"`
	Email     bool `json:"email"`
	OnAccess  bool `json:"on_access"`
	Scheduled bool `json:"scheduled"`
}

// Status is what the panel shows.
type Status struct {
	Enabled   bool `json:"enabled"`
	Running   bool `json:"running"`
	Database  bool `json:"database"`
	Email     bool `json:"email"`
	OnAccess  bool `json:"on_access"`
	Scheduled bool `json:"scheduled"`
}

// Detection is one infected file found by a scan.
type Detection struct {
	File  string `json:"file"`
	Virus string `json:"virus"`
}

// ScanResult is the outcome of an on-demand scan.
type ScanResult struct {
	Clean    bool        `json:"clean"`
	Infected []Detection `json:"infected"`
}

// Apply brings ClamAV to the given settings. Enabling it can take minutes the
// first time, because it downloads the signature database.
func (m *Manager) Apply(ctx context.Context, s Settings) error {
	if !s.Enabled {
		if err := m.setEmail(ctx, false); err != nil {
			return err
		}
		if err := m.setOnAccess(ctx, false); err != nil {
			return err
		}
		if err := m.setScheduled(ctx, false); err != nil {
			return err
		}
		// Stop the daemon that holds the database in memory.
		_, _ = m.cfg.Run(ctx, systemctlProgram, "disable", "--now", clamdService, clamdSocket)
		_, _ = m.cfg.Run(ctx, systemctlProgram, "disable", "--now", freshclamService)

		return nil
	}

	if err := m.ensureDatabase(ctx); err != nil {
		return err
	}

	if err := m.systemctl(ctx, "enable", "--now", freshclamService); err != nil {
		return err
	}

	if err := m.systemctl(ctx, "enable", "--now", clamdService); err != nil {
		return err
	}

	if err := m.setEmail(ctx, s.Email); err != nil {
		return err
	}

	if err := m.setOnAccess(ctx, s.OnAccess); err != nil {
		return err
	}

	return m.setScheduled(ctx, s.Scheduled)
}

// Status reports the running state and which modes are on.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	block, _ := os.ReadFile(m.cfg.ClamdConf)

	return Status{
		Running:   m.isActive(ctx, clamdService),
		Database:  m.databasePresent(),
		Email:     sameContent(m.antivirusPath(), []byte(rspamdAntivirusConf)),
		OnAccess:  m.isActive(ctx, onAccessService) && bytes.Contains(block, []byte(onAccessBegin)),
		Scheduled: m.isActive(ctx, scanTimer),
		Enabled:   m.isActive(ctx, clamdService),
	}, nil
}

// Scan runs clamd over path and returns the infected files. It does not move
// or delete anything.
func (m *Manager) Scan(ctx context.Context, path string) (ScanResult, error) {
	out, err := m.cfg.Run(ctx, clamdscanProgram, "--fdpass", "--multiscan", "--no-summary", path)

	// clamdscan exits 0 (clean), 1 (viruses found) or 2 (error).
	switch runner.ExitCode(err) {
	case -1:
		if err != nil {
			return ScanResult{}, fmt.Errorf("scan %s: %w", path, err)
		}
	case 0, 1:
		// 0 clean, 1 found: both are a completed scan.
	default:
		return ScanResult{}, fmt.Errorf("scan %s: %w", path, err)
	}

	infected := parseDetections(out)

	return ScanResult{Clean: len(infected) == 0, Infected: infected}, nil
}

// ensureDatabase downloads the signature database with a one-shot freshclam
// when it is missing, so clamd can start.
func (m *Manager) ensureDatabase(ctx context.Context) error {
	if m.databasePresent() {
		return nil
	}

	// A running freshclam service would hold the lock; stop it for the one-shot.
	_, _ = m.cfg.Run(ctx, systemctlProgram, "stop", freshclamService)

	if _, err := m.cfg.Run(ctx, freshclamProgram, "--quiet"); err != nil && !m.databasePresent() {
		return fmt.Errorf("download the virus database: %w", err)
	}

	return nil
}

// setEmail writes or removes the Rspamd antivirus config and reloads Rspamd.
func (m *Manager) setEmail(ctx context.Context, on bool) error {
	path := m.antivirusPath()

	if !on {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}

		return m.reloadRspamd(ctx)
	}

	if sameContent(path, []byte(rspamdAntivirusConf)) {
		return nil
	}

	if err := os.MkdirAll(m.cfg.RspamdLocalD, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", m.cfg.RspamdLocalD, err)
	}
	if err := writeFile(path, []byte(rspamdAntivirusConf), 0o644); err != nil {
		return err
	}

	return m.reloadRspamd(ctx)
}

func (m *Manager) reloadRspamd(ctx context.Context) error {
	if _, err := m.cfg.Run(ctx, rspamadmProgram, "configtest"); err != nil {
		return fmt.Errorf("Rspamd rejected the antivirus config: %w", err)
	}

	return m.systemctl(ctx, "reload-or-restart", "rspamd")
}

// setScheduled writes and enables (or disables) the daily scan timer.
func (m *Manager) setScheduled(ctx context.Context, on bool) error {
	if !on {
		_, _ = m.cfg.Run(ctx, systemctlProgram, "disable", "--now", scanTimer)

		return nil
	}

	if err := m.writeUnit(ctx, scanService, scanServiceUnit); err != nil {
		return err
	}
	if err := m.writeUnit(ctx, scanTimer, scanTimerUnit); err != nil {
		return err
	}

	return m.systemctl(ctx, "enable", "--now", scanTimer)
}

// setOnAccess manages the clamd.conf OnAccess block and the clamonacc service.
func (m *Manager) setOnAccess(ctx context.Context, on bool) error {
	changed, err := m.writeOnAccessBlock(on)
	if err != nil {
		return err
	}

	if !on {
		_, _ = m.cfg.Run(ctx, systemctlProgram, "disable", "--now", onAccessService)
		if changed {
			// clamd no longer needs OnAccess loaded.
			_ = m.restartClamd(ctx)
		}

		return nil
	}

	if changed {
		if err := m.restartClamd(ctx); err != nil {
			return err
		}
	}

	if err := m.writeUnit(ctx, onAccessService, onAccessServiceUnit); err != nil {
		return err
	}

	return m.systemctl(ctx, "enable", "--now", onAccessService)
}

func (m *Manager) restartClamd(ctx context.Context) error {
	return m.systemctl(ctx, "restart", clamdService)
}

// writeOnAccessBlock adds or removes the RAYXO OnAccess block in clamd.conf.
// It reports whether the file changed.
func (m *Manager) writeOnAccessBlock(on bool) (bool, error) {
	current, err := os.ReadFile(m.cfg.ClamdConf)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", m.cfg.ClamdConf, err)
	}

	stripped := stripBlock(current)

	var updated []byte
	if on {
		trimmed := bytes.TrimRight(stripped, "\n")
		updated = append(trimmed, '\n')
		updated = append(updated, []byte(onAccessBlock)...)
	} else {
		updated = stripped
	}

	if bytes.Equal(current, updated) {
		return false, nil
	}

	if err := writeFile(m.cfg.ClamdConf, updated, 0o644); err != nil {
		return false, err
	}

	return true, nil
}

// stripBlock removes a previous RAYXO OnAccess block (markers included).
func stripBlock(content []byte) []byte {
	begin := bytes.Index(content, []byte(onAccessBegin))
	if begin == -1 {
		return content
	}

	endMarker := []byte(onAccessEnd)
	end := bytes.Index(content[begin:], endMarker)
	if end == -1 {
		return content[:begin]
	}

	after := begin + end + len(endMarker)
	// Drop the newline that follows the end marker too.
	if after < len(content) && content[after] == '\n' {
		after++
	}

	before := bytes.TrimRight(content[:begin], "\n")

	return append(before, append([]byte("\n"), content[after:]...)...)
}

func (m *Manager) writeUnit(ctx context.Context, name, content string) error {
	path := filepath.Join(m.cfg.UnitDir, name)
	if sameContent(path, []byte(content)) {
		return nil
	}

	if err := writeFile(path, []byte(content), 0o644); err != nil {
		return err
	}

	return m.systemctl(ctx, "daemon-reload")
}

func (m *Manager) antivirusPath() string {
	return filepath.Join(m.cfg.RspamdLocalD, "antivirus.conf")
}

// databasePresent reports whether a signature database is on disk.
func (m *Manager) databasePresent() bool {
	for _, pattern := range []string{"*.cvd", "*.cld"} {
		matches, _ := filepath.Glob(filepath.Join(m.cfg.DatabaseDir, pattern))
		if len(matches) > 0 {
			return true
		}
	}

	return false
}

func (m *Manager) isActive(ctx context.Context, unit string) bool {
	_, err := m.cfg.Run(ctx, systemctlProgram, "is-active", "--quiet", unit)

	return err == nil
}

func (m *Manager) systemctl(ctx context.Context, args ...string) error {
	if _, err := m.cfg.Run(ctx, systemctlProgram, args...); err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}

	return nil
}

// parseDetections pulls "<file>: <Virus> FOUND" lines from clamdscan output.
func parseDetections(out []byte) []Detection {
	var found []Detection

	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasSuffix(line, " FOUND") {
			continue
		}

		body := strings.TrimSuffix(line, " FOUND")
		idx := strings.LastIndex(body, ": ")
		if idx == -1 {
			continue
		}

		found = append(found, Detection{File: body[:idx], Virus: body[idx+2:]})
	}

	return found
}

// sameContent reports whether path already holds exactly content.
func sameContent(path string, content []byte) bool {
	current, err := os.ReadFile(path)

	return err == nil && bytes.Equal(current, content)
}

// writeFile writes content to path atomically with mode.
func writeFile(path string, content []byte, mode os.FileMode) (err error) {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	defer func() {
		if err != nil {
			_ = os.Remove(temp.Name())
		}
	}()

	if _, err = temp.Write(content); err == nil {
		err = temp.Chmod(mode)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	if err = os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
