// Package redis manages a per-account Redis cache: one redis-server instance
// per hosting account, run as the account's own user and reachable only over
// a unix socket in a 0700 per-account runtime directory. Accounts cannot
// reach each other's Redis (Unix permissions, enforced by the systemd unit),
// and each instance has its own password and memory cap. This is separate
// from the Rspamd Redis the installer sets up for the mail server.
package redis

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"rayxo/agent/internal/runner"
)

//go:embed templates/redis.conf.tmpl
var confTemplate string

//go:embed templates/redis.service
var unitTemplate string

const (
	systemctlProgram = "/usr/bin/systemctl"
	// unitName is the systemd template unit; one instance per account,
	// e.g. rayxo-redis@budi.service.
	unitName       = "rayxo-redis@.service"
	instancePrefix = "rayxo-redis@"
	// defaultMaxMemoryMB caps an account's cache when the panel sets no size.
	defaultMaxMemoryMB = 256
)

// Config sets where the manager writes files and how it runs programs.
type Config struct {
	// ConfDir holds one <user>.conf per account.
	ConfDir string
	// UnitDir holds the systemd template unit.
	UnitDir string
	// Run runs a program; nil uses the real runner. Tests inject a fake.
	Run func(ctx context.Context, program string, args ...string) ([]byte, error)
}

// DefaultConfig is the configuration for the real server.
func DefaultConfig() Config {
	return Config{
		// Outside /etc/rayxo (which is root:rayxo 0750): each instance runs as
		// the account's own user and must be able to read its own conf here.
		ConfDir: "/etc/rayxo-redis",
		UnitDir: "/etc/systemd/system",
		Run:     runner.Run,
	}
}

// Manager provisions and removes per-account Redis instances.
type Manager struct{ cfg Config }

// New makes a Manager from cfg.
func New(cfg Config) *Manager {
	if cfg.Run == nil {
		cfg.Run = runner.Run
	}

	return &Manager{cfg: cfg}
}

// Instance is one account's cache: its user and ids, the password its app
// authenticates with, and the memory cap (0 uses the default).
type Instance struct {
	User        string
	UID         int
	GID         int
	Password    string
	MaxMemoryMB int
}

// Provision creates or updates the account's Redis instance and starts it.
// Calling it again with the same settings changes nothing.
func (m *Manager) Provision(ctx context.Context, in Instance) error {
	if err := m.ensureUnit(ctx); err != nil {
		return err
	}

	if err := os.MkdirAll(m.cfg.ConfDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", m.cfg.ConfDir, err)
	}

	// The account's own user traverses this directory to read its conf, so it
	// must stay world-traversable even if the agent runs with a tight umask.
	if err := os.Chmod(m.cfg.ConfDir, 0o755); err != nil {
		return fmt.Errorf("set mode on %s: %w", m.cfg.ConfDir, err)
	}

	conf, err := m.renderConf(in)
	if err != nil {
		return err
	}

	path := m.confPath(in.User)
	changed := !sameContent(path, conf)

	if changed {
		// The account's own user reads the conf (it holds its password).
		if err := writeOwned(path, conf, 0o600, in.UID, in.GID); err != nil {
			return err
		}
	}

	unit := instancePrefix + in.User

	if err := m.systemctl(ctx, "enable", "--now", unit); err != nil {
		return err
	}

	// A changed password or memory cap only takes effect on restart.
	if changed {
		if err := m.systemctl(ctx, "restart", unit); err != nil {
			return err
		}
	}

	return nil
}

// Deprovision stops and removes the account's instance. It succeeds when the
// instance or the account no longer exists.
func (m *Manager) Deprovision(ctx context.Context, user string) error {
	// disable --now stops and disables the instance; a unit that is not
	// loaded is not an error worth failing the account's deletion for.
	_, _ = m.cfg.Run(ctx, systemctlProgram, "disable", "--now", instancePrefix+user)

	if err := os.Remove(m.confPath(user)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", m.confPath(user), err)
	}

	return nil
}

// Running reports whether the account's instance is active.
func (m *Manager) Running(ctx context.Context, user string) (bool, error) {
	_, err := m.cfg.Run(ctx, systemctlProgram, "is-active", "--quiet", instancePrefix+user)
	if err == nil {
		return true, nil
	}

	// is-active exits non-zero for every state but active.
	if runner.ExitCode(err) > 0 {
		return false, nil
	}

	return false, fmt.Errorf("check whether Redis of %s is running: %w", user, err)
}

func (m *Manager) confPath(user string) string {
	return filepath.Join(m.cfg.ConfDir, user+".conf")
}

// ensureUnit writes the systemd template unit when it is missing or changed,
// and reloads systemd so the change is picked up.
func (m *Manager) ensureUnit(ctx context.Context) error {
	unit, err := m.renderUnit()
	if err != nil {
		return err
	}

	path := filepath.Join(m.cfg.UnitDir, unitName)
	if sameContent(path, unit) {
		return nil
	}

	if err := os.MkdirAll(m.cfg.UnitDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", m.cfg.UnitDir, err)
	}

	if err := writeOwned(path, unit, 0o644, 0, 0); err != nil {
		return err
	}

	return m.systemctl(ctx, "daemon-reload")
}

// renderUnit fills the systemd template unit with the conf directory so the
// unit and the conf files the manager writes always use the same path.
func (m *Manager) renderUnit() ([]byte, error) {
	tmpl, err := template.New("redis.service").Parse(unitTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse redis.service template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ ConfDir string }{ConfDir: m.cfg.ConfDir}); err != nil {
		return nil, fmt.Errorf("render redis.service: %w", err)
	}

	return buf.Bytes(), nil
}

func (m *Manager) renderConf(in Instance) ([]byte, error) {
	max := in.MaxMemoryMB
	if max <= 0 {
		max = defaultMaxMemoryMB
	}

	tmpl, err := template.New("redis.conf").Parse(confTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse redis.conf template: %w", err)
	}

	var buf bytes.Buffer
	data := struct {
		User        string
		Password    string
		MaxMemoryMB int
	}{User: in.User, Password: in.Password, MaxMemoryMB: max}

	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render redis.conf: %w", err)
	}

	return buf.Bytes(), nil
}

func (m *Manager) systemctl(ctx context.Context, args ...string) error {
	if _, err := m.cfg.Run(ctx, systemctlProgram, args...); err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}

	return nil
}

// sameContent reports whether path already holds exactly content.
func sameContent(path string, content []byte) bool {
	current, err := os.ReadFile(path)

	return err == nil && bytes.Equal(current, content)
}

// writeOwned writes content to path atomically with mode, then gives it to
// uid:gid. A uid and gid of 0 leaves it owned by the writer (root).
func writeOwned(path string, content []byte, mode os.FileMode, uid, gid int) (err error) {
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

	if err == nil && (uid != 0 || gid != 0) {
		err = os.Chown(temp.Name(), uid, gid)
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
