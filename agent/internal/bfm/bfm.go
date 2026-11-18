// Package bfm, the brute-force monitor, shows fail2ban's jails and the
// addresses each has banned, lets the panel unban an address, and keeps a
// small whitelist of addresses fail2ban must never ban. It never runs a
// shell: fail2ban-client is run directly with separate arguments, and the
// whitelist is the only fail2ban file the agent owns.
package bfm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"rayxo/agent/internal/runner"
)

const (
	clientProgram = "/usr/bin/fail2ban-client"

	// whitelistFile is the only fail2ban file the agent writes. Files in
	// jail.d ending in .local are read last, so the [DEFAULT] ignoreip here
	// wins over the installer's jail.local.
	whitelistFile = "/etc/fail2ban/jail.d/rayxo-whitelist.local"
)

// baseIgnore is always whitelisted: the loopback addresses, matching the
// installer's jail.local, so writing a whitelist never stops fail2ban from
// ignoring the server itself.
var baseIgnore = []string{"127.0.0.1/8", "::1"}

// ErrInvalidAddress is returned when an address to unban or whitelist is not
// a plain IP address or CIDR range in its usual form. Callers map it to an
// invalid-parameter response.
var ErrInvalidAddress = errors.New("not an IP address or CIDR range")

// Config is how the monitor runs fail2ban-client and where it writes the
// whitelist. The defaults talk to the real fail2ban; tests replace them.
type Config struct {
	// Run runs a program directly, never through a shell.
	Run func(ctx context.Context, program string, args ...string) ([]byte, error)
	// WhitelistFile is the path of the agent-owned whitelist drop-in.
	WhitelistFile string
}

// DefaultConfig talks to the real fail2ban on this server.
func DefaultConfig() Config {
	return Config{Run: runner.Run, WhitelistFile: whitelistFile}
}

// Manager shows and controls fail2ban.
type Manager struct {
	cfg Config
}

// New returns a Manager. Missing config fields fall back to the defaults.
func New(cfg Config) *Manager {
	if cfg.Run == nil {
		cfg.Run = runner.Run
	}

	if cfg.WhitelistFile == "" {
		cfg.WhitelistFile = whitelistFile
	}

	return &Manager{cfg: cfg}
}

// Jail is one fail2ban jail and its counts.
type Jail struct {
	Name            string   `json:"name"`
	CurrentlyFailed int      `json:"currently_failed"`
	TotalFailed     int      `json:"total_failed"`
	CurrentlyBanned int      `json:"currently_banned"`
	TotalBanned     int      `json:"total_banned"`
	BannedIPs       []string `json:"banned_ips"`
}

// Report is every jail fail2ban runs and the agent's whitelist.
type Report struct {
	Jails []Jail `json:"jails"`
	// Whitelist is the addresses the agent keeps fail2ban from banning,
	// beyond the loopback addresses it always ignores.
	Whitelist []string `json:"whitelist"`
}

// Status lists every jail with its counts and banned addresses, and the
// agent's whitelist.
func (m *Manager) Status(ctx context.Context) (Report, error) {
	out, err := m.cfg.Run(ctx, clientProgram, "status")
	if err != nil {
		return Report{}, err
	}

	names := parseJailList(out)
	report := Report{Jails: make([]Jail, 0, len(names))}

	for _, name := range names {
		detail, err := m.cfg.Run(ctx, clientProgram, "status", name)
		if err != nil {
			return Report{}, err
		}

		report.Jails = append(report.Jails, parseJail(name, detail))
	}

	whitelist, err := m.readWhitelist()
	if err != nil {
		return Report{}, err
	}

	report.Whitelist = whitelist

	return report, nil
}

// Unban removes a ban on one address across every jail. A caller that has
// not pre-validated ip gets ErrInvalidAddress for anything but a plain IP in
// its usual form.
func (m *Manager) Unban(ctx context.Context, ip string) error {
	address, err := cleanAddress(ip)
	if err != nil {
		return err
	}

	if _, err := m.cfg.Run(ctx, clientProgram, "unban", address); err != nil {
		return err
	}

	return nil
}

// SetWhitelist replaces the agent's whitelist with addresses (plain IPs or
// CIDR ranges), reloading fail2ban only when the file actually changes. The
// loopback addresses are always kept. An address in its wrong form gives
// ErrInvalidAddress and writes nothing.
func (m *Manager) SetWhitelist(ctx context.Context, addresses []string) error {
	cleaned, err := cleanList(addresses)
	if err != nil {
		return err
	}

	changed, err := writeIfChanged(m.cfg.WhitelistFile, renderWhitelist(cleaned))
	if err != nil {
		return err
	}

	if !changed {
		return nil
	}

	if _, err := m.cfg.Run(ctx, clientProgram, "reload"); err != nil {
		return err
	}

	return nil
}

// readWhitelist returns the whitelisted addresses the agent manages, without
// the always-ignored loopback addresses. A missing file means an empty
// whitelist, not an error.
func (m *Manager) readWhitelist() ([]string, error) {
	content, err := os.ReadFile(m.cfg.WhitelistFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}

		return nil, err
	}

	extras := []string{}

	for _, line := range strings.Split(string(content), "\n") {
		value, ok := valueAfter(line, "ignoreip")
		if !ok {
			continue
		}

		// "ignoreip = a b c" — drop the "= " and keep what is not base.
		value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "="))
		for _, field := range strings.Fields(value) {
			if !contains(baseIgnore, field) {
				extras = append(extras, field)
			}
		}

		break
	}

	return extras, nil
}

// renderWhitelist is the file content for the given extra addresses, with the
// loopback addresses always first.
func renderWhitelist(extras []string) []byte {
	all := append(append([]string{}, baseIgnore...), extras...)

	var builder strings.Builder
	builder.WriteString("# Managed by RAYXO. Edit the whitelist from the panel, not here.\n")
	builder.WriteString("[DEFAULT]\n")
	builder.WriteString("ignoreip = " + strings.Join(all, " ") + "\n")

	return []byte(builder.String())
}

// parseJailList reads the jail names out of "fail2ban-client status".
func parseJailList(output []byte) []string {
	for _, line := range strings.Split(string(output), "\n") {
		value, ok := valueAfter(line, "Jail list:")
		if !ok {
			continue
		}

		names := []string{}
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				names = append(names, name)
			}
		}

		return names
	}

	return []string{}
}

// parseJail reads one jail's counts and banned addresses out of
// "fail2ban-client status <jail>".
func parseJail(name string, output []byte) Jail {
	jail := Jail{Name: name, BannedIPs: []string{}}

	for _, line := range strings.Split(string(output), "\n") {
		switch {
		case has(line, "Currently failed:"):
			jail.CurrentlyFailed = intAfter(line, "Currently failed:")
		case has(line, "Total failed:"):
			jail.TotalFailed = intAfter(line, "Total failed:")
		case has(line, "Currently banned:"):
			jail.CurrentlyBanned = intAfter(line, "Currently banned:")
		case has(line, "Total banned:"):
			jail.TotalBanned = intAfter(line, "Total banned:")
		case has(line, "Banned IP list:"):
			value, _ := valueAfter(line, "Banned IP list:")
			jail.BannedIPs = append(jail.BannedIPs, strings.Fields(value)...)
		}
	}

	return jail
}

// cleanAddress returns ip when it is a plain IP address in its usual form,
// else ErrInvalidAddress. It never accepts a range, a zone, or a hostname, so
// nothing but an address reaches fail2ban-client.
func cleanAddress(ip string) (string, error) {
	address, err := netip.ParseAddr(ip)
	if err != nil || address.String() != ip || address.Zone() != "" || address.Is4In6() {
		return "", fmt.Errorf("%w: %q", ErrInvalidAddress, ip)
	}

	return address.String(), nil
}

// cleanList validates and de-duplicates addresses for the whitelist. Each is
// a plain IP or a CIDR range in its usual form; loopback entries and
// duplicates are dropped, and order is kept.
func cleanList(addresses []string) ([]string, error) {
	seen := map[string]bool{}
	for _, base := range baseIgnore {
		seen[base] = true
	}

	cleaned := []string{}

	for _, entry := range addresses {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		value, err := cleanEntry(entry)
		if err != nil {
			return nil, err
		}

		if seen[value] {
			continue
		}

		seen[value] = true
		cleaned = append(cleaned, value)
	}

	return cleaned, nil
}

// cleanEntry returns entry when it is a plain IP or a CIDR range in its usual
// form, else ErrInvalidAddress.
func cleanEntry(entry string) (string, error) {
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil || prefix.String() != entry || prefix.Addr().Zone() != "" {
			return "", fmt.Errorf("%w: %q", ErrInvalidAddress, entry)
		}

		return prefix.String(), nil
	}

	return cleanAddress(entry)
}

// writeIfChanged writes content to path atomically, but only when it differs
// from what is there now. It reports whether it wrote.
func writeIfChanged(path string, content []byte) (bool, error) {
	if current, err := os.ReadFile(path); err == nil {
		if string(current) == string(content) {
			return false, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	if err := writeAtomic(path, content, 0o644); err != nil {
		return false, err
	}

	return true, nil
}

// writeAtomic replaces path with content by renaming a temporary file over
// it, so a reader sees either the old file or the new one, never a partial
// write. The temporary name starts with a dot and does not end in .local or
// .conf, so fail2ban's include globs never pick it up.
func writeAtomic(path string, content []byte, mode fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	defer func() {
		if err != nil {
			_ = os.Remove(temp.Name())
		}
	}()

	_, err = temp.Write(content)
	if err == nil {
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

// valueAfter returns the text after label on line, trimmed, and whether label
// was there. Tree-drawing characters before the label are ignored.
func valueAfter(line, label string) (string, bool) {
	index := strings.Index(line, label)
	if index < 0 {
		return "", false
	}

	return strings.TrimSpace(line[index+len(label):]), true
}

// has reports whether line carries label.
func has(line, label string) bool {
	return strings.Contains(line, label)
}

// intAfter reads the integer after label on line, or 0.
func intAfter(line, label string) int {
	value, _ := valueAfter(line, label)
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}

	return number
}

// contains reports whether value is in list.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}

	return false
}
