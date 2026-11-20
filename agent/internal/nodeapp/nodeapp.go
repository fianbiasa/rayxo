// Package nodeapp runs a hosting account's Node.js application as a systemd
// service: one service per domain, run as the account's own user in its
// resource slice (rayxo-<user>.slice), listening on a per-account unix socket
// that Nginx reverse-proxies the domain to. The socket (not a shared loopback
// TCP port) keeps the app reachable only by Nginx and its own account, never
// by another tenant on 127.0.0.1. The account builds its own app; rayxod only
// runs the built output. Each app's environment (including its secrets) is
// written 0600 and owned by the account, outside /etc/rayxo so the app's own
// user can read it.
package nodeapp

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"rayxo/agent/internal/runner"
)

//go:embed templates/nodeapp.service.tmpl
var unitTemplate string

const (
	systemctlProgram = "/usr/bin/systemctl"
	nodeBinary       = "/usr/bin/node"
	// unitPrefix starts every node app unit; the domain follows, e.g.
	// rayxo-nodeapp-example.com.service.
	unitPrefix = "rayxo-nodeapp-"
	// socketRoot holds one 0710 <user>:rayxo-nginx subdirectory per account,
	// each with that account's app sockets. Only the account (owner) and Nginx
	// (group rayxo-nginx) can reach a socket; another tenant cannot traverse
	// the subdirectory, so it can never connect to the app.
	socketRoot = "/run/rayxo-nodeapp"
	// nginxGroup owns the socket directory as group and, through the setgid
	// bit, the socket itself, so Nginx (which runs as rayxo-nginx) can connect.
	nginxGroup = "rayxo-nginx"
)

// Config sets where the manager writes files and how it runs programs.
type Config struct {
	// ConfDir holds one <domain>.env per node app.
	ConfDir string
	// UnitDir holds the rendered systemd units.
	UnitDir string
	// SocketRoot holds a per-account directory with the app's listen socket, so
	// each app is reachable only by Nginx and its own account, never on a
	// shared loopback TCP port other accounts could connect to.
	SocketRoot string
	// NodeBinary is the node executable the unit runs.
	NodeBinary string
	// Run runs a program; nil uses the real runner. Tests inject a fake.
	Run func(ctx context.Context, program string, args ...string) ([]byte, error)
}

// DefaultConfig is the configuration for the real server.
func DefaultConfig() Config {
	return Config{
		// Outside /etc/rayxo (root:rayxo 0750): the app runs as the account's
		// own user, which must be able to read its own env file here.
		ConfDir:    "/etc/rayxo-nodeapp",
		UnitDir:    "/etc/systemd/system",
		SocketRoot: socketRoot,
		NodeBinary: nodeBinary,
		Run:        runner.Run,
	}
}

// Manager provisions and removes per-domain Node.js app services.
type Manager struct{ cfg Config }

// New makes a Manager from cfg.
func New(cfg Config) *Manager {
	if cfg.Run == nil {
		cfg.Run = runner.Run
	}

	if cfg.NodeBinary == "" {
		cfg.NodeBinary = nodeBinary
	}

	if cfg.SocketRoot == "" {
		cfg.SocketRoot = socketRoot
	}

	return &Manager{cfg: cfg}
}

// App is one domain's Node.js app: the account that owns it, where it runs,
// its entry file and environment. It listens on the per-account unix socket
// the manager points its env and Nginx at, not a TCP port.
type App struct {
	User    string
	UID     int
	GID     int
	Domain  string
	WorkDir string
	// Entry is the script node runs, relative to WorkDir.
	Entry string
	// Env are the account's own variables; NODE_ENV and the socket variables
	// are added by the manager and win over any the account sets.
	Env map[string]string
	// Port is the loopback port the panel allocated. It is no longer bound (the
	// app listens on a unix socket); kept so the panel's payload stays stable.
	Port int
}

// Provision creates or updates the domain's Node.js app service and starts
// it. Calling it again with the same settings only restarts on a change.
func (m *Manager) Provision(ctx context.Context, app App) error {
	if err := os.MkdirAll(m.cfg.ConfDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", m.cfg.ConfDir, err)
	}

	// The account's own user traverses this directory to read its env file,
	// so it must stay world-traversable even under a tight umask.
	if err := os.Chmod(m.cfg.ConfDir, 0o755); err != nil {
		return fmt.Errorf("set mode on %s: %w", m.cfg.ConfDir, err)
	}

	env := m.renderEnv(app)
	envPath := m.envPath(app.Domain)
	envChanged := !sameContent(envPath, env)

	if envChanged {
		// The app's own user reads the env file (it holds its secrets).
		if err := writeOwned(envPath, env, 0o600, app.UID, app.GID); err != nil {
			return err
		}
	}

	unit, err := m.renderUnit(app)
	if err != nil {
		return err
	}

	unitPath := filepath.Join(m.cfg.UnitDir, m.unitName(app.Domain))
	unitChanged := !sameContent(unitPath, unit)

	if unitChanged {
		if err := os.MkdirAll(m.cfg.UnitDir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", m.cfg.UnitDir, err)
		}

		if err := writeOwned(unitPath, unit, 0o644, 0, 0); err != nil {
			return err
		}

		if err := m.systemctl(ctx, "daemon-reload"); err != nil {
			return err
		}
	}

	name := m.unitName(app.Domain)

	if err := m.systemctl(ctx, "enable", "--now", name); err != nil {
		return err
	}

	// A changed entry, env or port only takes effect on restart.
	if envChanged || unitChanged {
		if err := m.systemctl(ctx, "restart", name); err != nil {
			return err
		}
	}

	return nil
}

// Deprovision stops and removes the domain's app service. It succeeds when
// the service is already gone.
func (m *Manager) Deprovision(ctx context.Context, domain string) error {
	_, _ = m.cfg.Run(ctx, systemctlProgram, "disable", "--now", m.unitName(domain))

	if err := os.Remove(m.unitPath(domain)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", m.unitPath(domain), err)
	}

	if err := os.Remove(m.envPath(domain)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", m.envPath(domain), err)
	}

	// The unit file is gone; let systemd forget it.
	_, _ = m.cfg.Run(ctx, systemctlProgram, "daemon-reload")

	return nil
}

// Restart restarts the domain's app service (after the account rebuilt it).
func (m *Manager) Restart(ctx context.Context, domain string) error {
	return m.systemctl(ctx, "restart", m.unitName(domain))
}

// Running reports whether the domain's app service is active.
func (m *Manager) Running(ctx context.Context, domain string) (bool, error) {
	_, err := m.cfg.Run(ctx, systemctlProgram, "is-active", "--quiet", m.unitName(domain))
	if err == nil {
		return true, nil
	}

	if runner.ExitCode(err) > 0 {
		return false, nil
	}

	return false, fmt.Errorf("check whether the Node.js app of %s is running: %w", domain, err)
}

func (m *Manager) unitName(domain string) string { return unitPrefix + domain + ".service" }

func (m *Manager) unitPath(domain string) string {
	return filepath.Join(m.cfg.UnitDir, m.unitName(domain))
}

func (m *Manager) envPath(domain string) string {
	return filepath.Join(m.cfg.ConfDir, domain+".env")
}

// socketDir is the account's per-account socket directory (0710
// <user>:rayxo-nginx, setgid), inside the shared socket root.
func (m *Manager) socketDir(user string) string {
	return filepath.Join(m.cfg.SocketRoot, user)
}

// socketPath is where one domain's app listens, inside the account's
// socketDir, so only the account and Nginx can reach it.
func (m *Manager) socketPath(user, domain string) string {
	return filepath.Join(m.socketDir(user), domain+".sock")
}

// renderUnit fills the systemd unit for one app.
func (m *Manager) renderUnit(app App) ([]byte, error) {
	tmpl, err := template.New("nodeapp.service").Parse(unitTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse nodeapp.service template: %w", err)
	}

	var buf bytes.Buffer
	data := struct {
		User, Group, Domain, WorkDir, Entry, EnvFile, NodeBinary, SocketRoot, SocketDir, Socket string
	}{
		User:       app.User,
		Group:      nginxGroup,
		Domain:     app.Domain,
		WorkDir:    app.WorkDir,
		Entry:      app.Entry,
		EnvFile:    m.envPath(app.Domain),
		NodeBinary: m.cfg.NodeBinary,
		SocketRoot: m.cfg.SocketRoot,
		SocketDir:  m.socketDir(app.User),
		Socket:     m.socketPath(app.User, app.Domain),
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render nodeapp.service: %w", err)
	}

	return buf.Bytes(), nil
}

// renderEnv writes the systemd EnvironmentFile: the account's own variables
// first, then NODE_ENV and the socket variables, which the manager controls
// and which therefore win over any the account set. Keys are sorted so the
// file only changes when its contents do.
//
// The app must listen on the unix socket, not a TCP port: NITRO_UNIX_SOCKET is
// Nitro's (Nuxt's) own variable, RAYXO_NODE_SOCKET is the explicit one to pass
// to server.listen(), and PORT is set to the socket path too so the common
// server.listen(process.env.PORT) binds the socket (Node treats a non-numeric
// value as a pipe path). No HOST is set, which would turn that call back into
// a TCP listen.
func (m *Manager) renderEnv(app App) []byte {
	var buf bytes.Buffer

	buf.WriteString("# Node.js app " + app.Domain + ". Written by rayxod; changes are overwritten.\n")

	keys := make([]string, 0, len(app.Env))
	for key := range app.Env {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		buf.WriteString(key + "=" + app.Env[key] + "\n")
	}

	socket := m.socketPath(app.User, app.Domain)
	buf.WriteString("NODE_ENV=production\n")
	buf.WriteString("NITRO_UNIX_SOCKET=" + socket + "\n")
	buf.WriteString("RAYXO_NODE_SOCKET=" + socket + "\n")
	buf.WriteString("PORT=" + socket + "\n")

	return buf.Bytes()
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
