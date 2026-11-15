// Package ioncube installs the ionCube Loader, which PHP needs to run
// software encoded with ionCube, such as WHMCS. The loader is ionCube's own
// closed software, free to use: it is only downloaded, from ionCube over
// HTTPS, once the administrator turns it on in the panel, and taken away
// again when it is turned off.
//
// The loader of each installed PHP version that ionCube makes one for goes
// into LibDir, root's and read-only for everyone else, and PHP loads it
// first of all its extensions, for the websites (fpm) and the command line
// (cli, which cron jobs and SSH use inside their jails): through
// mods-available/ioncube.ini, linked from conf.d as 00-ioncube.ini. A
// version whose PHP does not start with it goes without it.
package ioncube

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	// maxArchive bounds the download: ionCube's archive is about 20 MB.
	maxArchive = 256 << 20
	// maxLoader bounds one loader in it.
	maxLoader = 64 << 20
	// iniName is the file PHP reads in mods-available, linked from conf.d
	// as linkName.
	iniName  = "ioncube.ini"
	linkName = "00-ioncube.ini"
)

// loaderPattern is a loader in ionCube's archive, for PHP <version>; the
// thread-safe builds (_ts) are not for PHP-FPM.
var loaderPattern = regexp.MustCompile(`^ioncube/ioncube_loader_lin_([0-9]+\.[0-9]+)\.so$`)

// versionPattern finds the loader's version in what php -v prints.
var versionPattern = regexp.MustCompile(`ionCube PHP Loader(?: \+ ionCube24)? v?([0-9]+(?:\.[0-9]+)+)`)

// Config says where the loader comes from and goes.
type Config struct {
	// URL is ionCube's archive of loaders for this machine.
	URL string
	// LibDir holds the loaders.
	LibDir string
	// PHPDir holds the settings of every PHP version, /etc/php.
	PHPDir string
	// Client downloads the archive.
	Client *http.Client
	// PHP runs php<version> with args and returns what it printed.
	PHP func(ctx context.Context, version string, args ...string) (string, error)
	// Reload makes the PHP-FPM pools of version read their settings again.
	Reload func(ctx context.Context, version string) error
}

// DefaultConfig is the real server's: the loaders for its architecture,
// in /usr/local/lib/rayxo/ioncube.
func DefaultConfig(php func(ctx context.Context, version string, args ...string) (string, error), reload func(ctx context.Context, version string) error) Config {
	arch := "x86-64"
	if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	}

	return Config{
		URL:    "https://downloads.ioncube.com/loader_downloads/ioncube_loaders_lin_" + arch + ".tar.gz",
		LibDir: "/usr/local/lib/rayxo/ioncube",
		PHPDir: "/etc/php",
		Client: &http.Client{Timeout: 10 * time.Minute},
		PHP:    php,
		Reload: reload,
	}
}

// Manager turns the loader on and off.
type Manager struct {
	cfg Config
}

// New returns a Manager for cfg.
func New(cfg Config) *Manager {
	return &Manager{cfg: cfg}
}

// State is which PHP versions load the loader, and its version.
type State struct {
	Enabled  bool     `json:"enabled"`
	Versions []string `json:"versions"`
	// Missing are the versions ionCube makes no loader for, or whose PHP
	// does not start with it.
	Missing []string `json:"missing"`
	Loader  string   `json:"loader"`
}

// Status reports which of versions load the loader now.
func (m *Manager) Status(ctx context.Context, versions []string) State {
	state := State{Versions: []string{}, Missing: []string{}}

	for _, version := range versions {
		if !m.installed(version) {
			continue
		}

		state.Versions = append(state.Versions, version)

		if state.Loader == "" {
			state.Loader = m.loaderVersion(ctx, version)
		}
	}

	state.Enabled = len(state.Versions) > 0

	return state
}

// Enable downloads ionCube's loaders and turns them on for each of
// versions it has one for, then reloads their PHP-FPM pools.
func (m *Manager) Enable(ctx context.Context, versions []string) (State, error) {
	if err := os.MkdirAll(m.cfg.LibDir, 0o755); err != nil {
		return State{}, fmt.Errorf("create %s: %w", m.cfg.LibDir, err)
	}

	found, err := m.download(ctx, versions)
	if err != nil {
		return State{}, err
	}

	state := State{Enabled: true, Versions: []string{}, Missing: []string{}}

	for _, version := range versions {
		if !found[version] {
			state.Missing = append(state.Missing, version)
			m.remove(version)

			continue
		}

		if err := m.link(version); err != nil {
			return state, err
		}

		// A PHP that does not start with the loader goes without it.
		loader := m.loaderVersion(ctx, version)
		if loader == "" {
			m.remove(version)
			state.Missing = append(state.Missing, version)

			continue
		}

		state.Versions = append(state.Versions, version)
		state.Loader = loader
	}

	state.Enabled = len(state.Versions) > 0

	return state, m.reload(ctx, versions)
}

// Disable takes the loader away from every one of versions and reloads
// their PHP-FPM pools.
func (m *Manager) Disable(ctx context.Context, versions []string) (State, error) {
	for _, version := range versions {
		m.remove(version)
	}

	_ = os.Remove(m.cfg.LibDir)

	return State{Versions: []string{}, Missing: []string{}}, m.reload(ctx, versions)
}

// download fetches ionCube's archive and keeps the loader of each of
// versions it holds, replacing the one there.
func (m *Manager) download(ctx context.Context, versions []string) (map[string]bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.URL, nil)
	if err != nil {
		return nil, err
	}

	response, err := m.cfg.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download the ionCube Loader: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download the ionCube Loader: HTTP %d", response.StatusCode)
	}

	compressed, err := gzip.NewReader(io.LimitReader(response.Body, maxArchive))
	if err != nil {
		return nil, fmt.Errorf("the ionCube Loader download is damaged: %w", err)
	}
	defer compressed.Close()

	found := map[string]bool{}
	reader := tar.NewReader(compressed)

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return found, nil
		}

		if err != nil {
			return nil, fmt.Errorf("the ionCube Loader download is damaged: %w", err)
		}

		match := loaderPattern.FindStringSubmatch(strings.TrimPrefix(header.Name, "./"))
		if match == nil || header.Typeflag != tar.TypeReg || header.Size > maxLoader || !slices.Contains(versions, match[1]) {
			continue
		}

		if err := m.keep(match[1], reader, header.Size); err != nil {
			return nil, err
		}

		found[match[1]] = true
	}
}

// keep writes the loader of version from content, then renames it into
// place, so PHP never loads half of one.
func (m *Manager) keep(version string, content io.Reader, size int64) error {
	temp, err := os.CreateTemp(m.cfg.LibDir, ".loader-*")
	if err != nil {
		return fmt.Errorf("save the ionCube Loader for PHP %s: %w", version, err)
	}

	defer func() { _ = os.Remove(temp.Name()) }()

	_, err = io.CopyN(temp, content, size)
	if err == nil {
		err = temp.Chmod(0o644)
	}

	if closed := temp.Close(); err == nil {
		err = closed
	}

	if err == nil {
		err = os.Rename(temp.Name(), m.loaderPath(version))
	}

	if err != nil {
		return fmt.Errorf("save the ionCube Loader for PHP %s: %w", version, err)
	}

	return nil
}

// link writes the settings that load the loader of version, and links
// them into the settings of PHP-FPM and of the command line.
func (m *Manager) link(version string) error {
	ini := filepath.Join(m.cfg.PHPDir, version, "mods-available", iniName)
	content := "; Written by rayxod: the ionCube Loader, turned on in the panel's server settings.\n" +
		"zend_extension=" + m.loaderPath(version) + "\n"

	if err := os.WriteFile(ini, []byte(content), 0o644); err != nil {
		return fmt.Errorf("turn on the ionCube Loader for PHP %s: %w", version, err)
	}

	for _, sapi := range []string{"fpm", "cli"} {
		dir := filepath.Join(m.cfg.PHPDir, version, sapi, "conf.d")
		if _, err := os.Stat(dir); err != nil {
			continue
		}

		link := filepath.Join(dir, linkName)
		_ = os.Remove(link)

		if err := os.Symlink(ini, link); err != nil {
			return fmt.Errorf("turn on the ionCube Loader for PHP %s: %w", version, err)
		}
	}

	return nil
}

// remove takes the loader of version away: its settings and the loader.
func (m *Manager) remove(version string) {
	for _, sapi := range []string{"fpm", "cli"} {
		_ = os.Remove(filepath.Join(m.cfg.PHPDir, version, sapi, "conf.d", linkName))
	}

	_ = os.Remove(filepath.Join(m.cfg.PHPDir, version, "mods-available", iniName))
	_ = os.Remove(m.loaderPath(version))
}

// installed reports whether PHP version loads the loader.
func (m *Manager) installed(version string) bool {
	_, link := os.Stat(filepath.Join(m.cfg.PHPDir, version, "fpm", "conf.d", linkName))
	_, loader := os.Stat(m.loaderPath(version))

	return link == nil && loader == nil
}

// loaderVersion is the version of the loader PHP version runs with, or
// empty when it does not start with it.
func (m *Manager) loaderVersion(ctx context.Context, version string) string {
	output, err := m.cfg.PHP(ctx, version, "-v")
	if err != nil {
		return ""
	}

	if match := versionPattern.FindStringSubmatch(output); match != nil {
		return match[1]
	}

	return ""
}

func (m *Manager) reload(ctx context.Context, versions []string) error {
	var errs []error

	for _, version := range versions {
		errs = append(errs, m.cfg.Reload(ctx, version))
	}

	return errors.Join(errs...)
}

func (m *Manager) loaderPath(version string) string {
	return filepath.Join(m.cfg.LibDir, "ioncube_loader_lin_"+version+".so")
}
