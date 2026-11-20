// Package waf manages the server-wide ModSecurity (web application firewall)
// engine state the admin sets from Server Settings: off, detection-only or on.
//
// The OWASP Core Rule Set and the module are loaded by the installer; this
// package only writes the one file that sets the default SecRuleEngine
// (included last by the global Apache config, so it wins) and reloads Apache.
// Per-domain overrides live in each site's vhost, written by the webserver
// package.
package waf

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"rayxo/agent/internal/runner"
)

const (
	systemctlProgram = "/usr/bin/systemctl"
	apachectlProgram = "/usr/sbin/apache2ctl"
	apacheService    = "apache2"

	// ModeOff, ModeDetection and ModeOn are the modes the panel sends.
	ModeOff       = "off"
	ModeDetection = "detection"
	ModeOn        = "on"
)

// Config sets where the manager writes the engine file and how it runs
// programs.
type Config struct {
	// EngineFile holds the global SecRuleEngine directive; the Apache config
	// includes it last so it overrides the base config.
	EngineFile string
	Run        func(ctx context.Context, program string, args ...string) ([]byte, error)
}

// DefaultConfig is the configuration for the real server.
func DefaultConfig() Config {
	return Config{
		EngineFile: "/etc/rayxo/modsec/engine.conf",
		Run:        runner.Run,
	}
}

// Manager turns the ModSecurity engine off, to detection-only or on.
type Manager struct{ cfg Config }

// New makes a Manager from cfg.
func New(cfg Config) *Manager {
	if cfg.Run == nil {
		cfg.Run = runner.Run
	}

	if cfg.EngineFile == "" {
		cfg.EngineFile = DefaultConfig().EngineFile
	}

	return &Manager{cfg: cfg}
}

// Settings is the WAF state the panel asks for.
type Settings struct {
	Mode string `json:"mode"`
}

// Status is what the panel shows.
type Status struct {
	Mode string `json:"mode"`
}

// engineDirective maps a panel mode to the SecRuleEngine value.
func engineDirective(mode string) (string, error) {
	switch mode {
	case ModeOff:
		return "Off", nil
	case ModeDetection:
		return "DetectionOnly", nil
	case ModeOn:
		return "On", nil
	default:
		return "", fmt.Errorf("mode %q must be off, detection or on", mode)
	}
}

// engineFileContent is the engine file for a directive.
func engineFileContent(directive string) []byte {
	return []byte("# Written by rayxod; changes are overwritten.\n" +
		"<IfModule security2_module>\n" +
		"    SecRuleEngine " + directive + "\n" +
		"</IfModule>\n")
}

// Apply sets the global ModSecurity engine to the mode and reloads Apache. It
// only reloads when the engine file changed.
func (m *Manager) Apply(ctx context.Context, s Settings) error {
	directive, err := engineDirective(s.Mode)
	if err != nil {
		return err
	}

	content := engineFileContent(directive)
	if sameContent(m.cfg.EngineFile, content) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(m.cfg.EngineFile), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(m.cfg.EngineFile), err)
	}

	if err := writeFile(m.cfg.EngineFile, content, 0o644); err != nil {
		return err
	}

	if _, err := m.cfg.Run(ctx, apachectlProgram, "configtest"); err != nil {
		return fmt.Errorf("Apache rejected the ModSecurity config: %w", err)
	}

	if _, err := m.cfg.Run(ctx, systemctlProgram, "reload-or-restart", apacheService); err != nil {
		return fmt.Errorf("reload Apache: %w", err)
	}

	return nil
}

// Status reports the global ModSecurity mode from the engine file; a missing
// file means off.
func (m *Manager) Status(_ context.Context) (Status, error) {
	content, err := os.ReadFile(m.cfg.EngineFile)
	if err != nil {
		return Status{Mode: ModeOff}, nil
	}

	switch {
	case bytes.Contains(content, []byte("SecRuleEngine On")):
		return Status{Mode: ModeOn}, nil
	case bytes.Contains(content, []byte("SecRuleEngine DetectionOnly")):
		return Status{Mode: ModeDetection}, nil
	default:
		return Status{Mode: ModeOff}, nil
	}
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
