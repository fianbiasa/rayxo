// Package firewall manages one nftables table, "inet rayxo", that the admin
// turns on from the panel; it is off until then. The table is replaced on its
// own and the whole ruleset is never flushed, so it lives alongside
// fail2ban's table. Enabling or tightening the firewall opens a timed trial
// that reverts itself unless the admin confirms through it, so a wrong rule
// can never lock anyone out, and the ports RAYXO's own services need are
// always open.
package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"rayxo/agent/internal/runner"
)

const (
	nftProgram  = "/usr/sbin/nft"
	sshdProgram = "/usr/sbin/sshd"

	// panelPortFile is where the installer and panelport keep the panel's
	// port (panelport.Config.PortFile).
	panelPortFile = "/etc/rayxo/panel-port"
	defaultPanel  = 2222

	// ftpPassiveStart and ftpPassiveEnd are pure-ftpd's passive port range
	// (FTP_PASSIVE_PORTS in installer/setup.sh); the whole range stays open.
	ftpPassiveStart = 35000
	ftpPassiveEnd   = 35999
)

// serviceTCPPorts are the TCP ports RAYXO's services listen on and the
// firewall always keeps open: web, FTP control, DNS over TCP, and mail. The
// SSH and panel ports are added on top, read from the running system, and the
// FTP passive range is always opened too.
var serviceTCPPorts = []int{80, 443, 21, 53, 25, 110, 143, 465, 587, 993, 995}

// serviceUDPPorts are the UDP ports always kept open: DNS.
var serviceUDPPorts = []int{53}

var (
	// ErrTrialRunning means a firewall change is already waiting to be
	// confirmed; confirm or cancel it first.
	ErrTrialRunning = errors.New("a firewall change is waiting to be confirmed")
	// ErrNoTrial means no firewall change is waiting to be confirmed.
	ErrNoTrial = errors.New("no firewall change is waiting to be confirmed")
	// ErrInvalidConfig means a port or address in the config is not valid.
	ErrInvalidConfig = errors.New("invalid firewall setting")
)

// Config is the firewall the admin asks for: whether it is on, the extra
// ports to open beyond RAYXO's own services, and addresses to always allow or
// always block.
type Config struct {
	Enabled  bool     `json:"enabled"`
	TCPPorts []int    `json:"tcp_ports"`
	UDPPorts []int    `json:"udp_ports"`
	AllowIPs []string `json:"allow_ips"`
	DenyIPs  []string `json:"deny_ips"`
}

// normalized returns the config with every slice non-nil, so it marshals to
// JSON arrays and never null. The panel's types require arrays, and a nil Go
// slice would reach it as null and break reading them (e.g. .map()).
func (c Config) normalized() Config {
	if c.TCPPorts == nil {
		c.TCPPorts = []int{}
	}
	if c.UDPPorts == nil {
		c.UDPPorts = []int{}
	}
	if c.AllowIPs == nil {
		c.AllowIPs = []string{}
	}
	if c.DenyIPs == nil {
		c.DenyIPs = []string{}
	}

	return c
}

// Trial is a firewall change applied but not yet confirmed; it reverts to the
// active config when it expires.
type Trial struct {
	Config    Config    `json:"config"`
	ExpiresAt time.Time `json:"expires_at"`
}

// state is what the agent remembers between runs: the confirmed config it
// reverts to, and any change still waiting to be confirmed.
type state struct {
	Active  Config `json:"active"`
	Pending *Trial `json:"pending,omitempty"`
}

// Status is what the panel shows.
type Status struct {
	Active        Config `json:"active"`
	Pending       *Trial `json:"pending"`
	AlwaysOpenTCP []int  `json:"always_open_tcp"`
	AlwaysOpenUDP []int  `json:"always_open_udp"`
	FTPPassive    string `json:"ftp_passive"`
}

// ManagerConfig is how the Manager runs nft, where it keeps its state, how
// long a trial lasts, and how it learns the SSH and panel ports to keep open.
type ManagerConfig struct {
	// Run runs a program with input on stdin, like runner.RunInput.
	Run       func(ctx context.Context, input []byte, program string, args ...string) ([]byte, error)
	StateFile string
	Window    time.Duration
	Now       func() time.Time
	// SSHPorts returns the ports sshd listens on; PanelPort the panel's port.
	// Both are always kept open so the firewall can never lock the admin out.
	SSHPorts  func(ctx context.Context) []int
	PanelPort func() int
}

// DefaultConfig manages the real firewall on this server.
func DefaultConfig() ManagerConfig {
	return ManagerConfig{
		Run:       runner.RunInput,
		StateFile: "/var/lib/rayxo/firewall.json",
		Window:    5 * time.Minute,
		Now:       time.Now,
		SSHPorts:  detectSSHPorts,
		PanelPort: detectPanelPort,
	}
}

// Manager applies the firewall and keeps its trial honest.
type Manager struct {
	cfg ManagerConfig
}

// New returns a Manager, filling in any missing config with the defaults.
func New(cfg ManagerConfig) *Manager {
	defaults := DefaultConfig()
	if cfg.Run == nil {
		cfg.Run = defaults.Run
	}
	if cfg.StateFile == "" {
		cfg.StateFile = defaults.StateFile
	}
	if cfg.Window == 0 {
		cfg.Window = defaults.Window
	}
	if cfg.Now == nil {
		cfg.Now = defaults.Now
	}
	if cfg.SSHPorts == nil {
		cfg.SSHPorts = defaults.SSHPorts
	}
	if cfg.PanelPort == nil {
		cfg.PanelPort = defaults.PanelPort
	}

	return &Manager{cfg: cfg}
}

// Status reports the firewall's state for the panel.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	st, err := m.load()
	if err != nil {
		return Status{}, err
	}

	return m.statusFor(ctx, st), nil
}

// Apply turns the firewall on (or changes it) behind a trial that reverts
// unless confirmed, or turns it off at once. Turning it off only opens
// access, so it is immediate and needs no trial. A change while another is
// waiting to be confirmed is refused.
func (m *Manager) Apply(ctx context.Context, config Config) (Status, error) {
	cleaned, err := cleanConfig(config)
	if err != nil {
		return Status{}, err
	}

	st, err := m.load()
	if err != nil {
		return Status{}, err
	}

	if !cleaned.Enabled {
		if err := m.write(ctx, cleaned); err != nil {
			return Status{}, err
		}

		st.Active, st.Pending = cleaned, nil

		return m.finish(ctx, st)
	}

	if st.Pending != nil {
		return Status{}, ErrTrialRunning
	}

	if err := m.write(ctx, cleaned); err != nil {
		return Status{}, err
	}

	st.Pending = &Trial{Config: cleaned, ExpiresAt: m.cfg.Now().Add(m.cfg.Window).UTC()}

	return m.finish(ctx, st)
}

// Confirm keeps the pending change for good: it becomes the config the
// firewall reverts to. The rules are already live, so nothing is re-applied.
func (m *Manager) Confirm(ctx context.Context) (Status, error) {
	st, err := m.load()
	if err != nil {
		return Status{}, err
	}

	if st.Pending == nil {
		return Status{}, ErrNoTrial
	}

	st.Active, st.Pending = st.Pending.Config, nil
	if err := m.save(st); err != nil {
		return Status{}, err
	}

	return m.statusFor(ctx, st), nil
}

// Cancel reverts a pending change at once, back to the confirmed config.
func (m *Manager) Cancel(ctx context.Context) (Status, error) {
	st, err := m.load()
	if err != nil {
		return Status{}, err
	}

	if st.Pending == nil {
		return Status{}, ErrNoTrial
	}

	return m.revert(ctx, st)
}

// Restore re-applies the confirmed config, for the agent's startup after a
// reboot cleared the kernel's rules. A change still waiting to be confirmed
// is dropped, so a restart can only ever leave the known-good firewall.
func (m *Manager) Restore(ctx context.Context) error {
	st, err := m.load()
	if err != nil {
		return err
	}

	st.Pending = nil
	if err := m.write(ctx, st.Active); err != nil {
		return err
	}

	return m.save(st)
}

// Tick reverts a pending change once its trial has expired. Watch calls it.
func (m *Manager) Tick(ctx context.Context) error {
	st, err := m.load()
	if err != nil || st.Pending == nil {
		return err
	}

	if m.cfg.Now().After(st.Pending.ExpiresAt) {
		_, err := m.revert(ctx, st)

		return err
	}

	return nil
}

// Watch runs Tick every interval until ctx ends.
func (m *Manager) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickCtx, cancel := context.WithTimeout(ctx, interval)
			_ = m.Tick(tickCtx)
			cancel()
		}
	}
}

func (m *Manager) revert(ctx context.Context, st state) (Status, error) {
	if err := m.write(ctx, st.Active); err != nil {
		return Status{}, err
	}

	st.Pending = nil

	return m.finish(ctx, st)
}

func (m *Manager) finish(ctx context.Context, st state) (Status, error) {
	if err := m.save(st); err != nil {
		return Status{}, err
	}

	return m.statusFor(ctx, st), nil
}

func (m *Manager) statusFor(ctx context.Context, st state) Status {
	pending := st.Pending
	if pending != nil {
		trial := *pending
		trial.Config = trial.Config.normalized()
		pending = &trial
	}

	return Status{
		Active:        st.Active.normalized(),
		Pending:       pending,
		AlwaysOpenTCP: m.alwaysOpenTCP(ctx),
		AlwaysOpenUDP: m.alwaysOpenUDP(),
		FTPPassive:    fmt.Sprintf("%d-%d", ftpPassiveStart, ftpPassiveEnd),
	}
}

// write renders config and loads it into nft, replacing the inet rayxo table
// without touching any other table.
func (m *Manager) write(ctx context.Context, config Config) error {
	ruleset := render(config, m.alwaysOpenTCP(ctx), m.alwaysOpenUDP())
	if _, err := m.cfg.Run(ctx, []byte(ruleset), nftProgram, "-f", "-"); err != nil {
		return err
	}

	return nil
}

func (m *Manager) alwaysOpenTCP(ctx context.Context) []int {
	ports := append([]int{}, serviceTCPPorts...)
	ports = append(ports, m.cfg.PanelPort())
	ports = append(ports, m.cfg.SSHPorts(ctx)...)

	return dedupePorts(ports)
}

func (m *Manager) alwaysOpenUDP() []int {
	return dedupePorts(append([]int{}, serviceUDPPorts...))
}

func (m *Manager) load() (state, error) {
	content, err := os.ReadFile(m.cfg.StateFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return state{Active: Config{Enabled: false}}, nil
		}

		return state{}, fmt.Errorf("read %s: %w", m.cfg.StateFile, err)
	}

	var st state
	if err := json.Unmarshal(content, &st); err != nil {
		return state{}, fmt.Errorf("parse %s: %w", m.cfg.StateFile, err)
	}

	return st, nil
}

func (m *Manager) save(st state) error {
	content, err := json.Marshal(st)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(m.cfg.StateFile), 0o750); err != nil {
		return fmt.Errorf("write %s: %w", m.cfg.StateFile, err)
	}

	if err := os.WriteFile(m.cfg.StateFile, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", m.cfg.StateFile, err)
	}

	return nil
}

// render builds the nft input: when off it only ensures no inet rayxo table
// is left; when on it drops by default and accepts loopback, established
// traffic, the allow set, ICMP, and the open ports, after dropping the deny
// set.
func render(config Config, tcpOpen, udpOpen []int) string {
	var b strings.Builder

	// Ensure-then-delete replaces the table atomically and leaves other
	// tables, including fail2ban's, untouched.
	b.WriteString("table inet rayxo\n")
	b.WriteString("delete table inet rayxo\n")

	if !config.Enabled {
		return b.String()
	}

	allow4, allow6 := splitFamilies(config.AllowIPs)
	deny4, deny6 := splitFamilies(config.DenyIPs)

	b.WriteString("table inet rayxo {\n")
	writeSet(&b, "allow4", "ipv4_addr", allow4)
	writeSet(&b, "allow6", "ipv6_addr", allow6)
	writeSet(&b, "deny4", "ipv4_addr", deny4)
	writeSet(&b, "deny6", "ipv6_addr", deny6)
	b.WriteString("\tchain input {\n")
	b.WriteString("\t\ttype filter hook input priority 0; policy drop;\n")
	b.WriteString("\t\tct state established,related accept\n")
	b.WriteString("\t\tct state invalid drop\n")
	b.WriteString("\t\tiifname \"lo\" accept\n")
	if len(deny4) > 0 {
		b.WriteString("\t\tip saddr @deny4 drop\n")
	}
	if len(deny6) > 0 {
		b.WriteString("\t\tip6 saddr @deny6 drop\n")
	}
	if len(allow4) > 0 {
		b.WriteString("\t\tip saddr @allow4 accept\n")
	}
	if len(allow6) > 0 {
		b.WriteString("\t\tip6 saddr @allow6 accept\n")
	}
	b.WriteString("\t\tmeta l4proto { icmp, ipv6-icmp } accept\n")
	b.WriteString("\t\ttcp dport { " + tcpElements(config, tcpOpen) + " } accept\n")
	if udp := udpElements(config, udpOpen); udp != "" {
		b.WriteString("\t\tudp dport { " + udp + " } accept\n")
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n")

	return b.String()
}

// tcpElements is the TCP dport set: the always-open ports, the admin's extra
// ports, and the FTP passive range.
func tcpElements(config Config, open []int) string {
	ports := dedupePorts(append(append([]int{}, open...), config.TCPPorts...))

	elements := make([]string, 0, len(ports)+1)
	for _, port := range ports {
		elements = append(elements, strconv.Itoa(port))
	}
	elements = append(elements, fmt.Sprintf("%d-%d", ftpPassiveStart, ftpPassiveEnd))

	return strings.Join(elements, ", ")
}

func udpElements(config Config, open []int) string {
	ports := dedupePorts(append(append([]int{}, open...), config.UDPPorts...))

	elements := make([]string, 0, len(ports))
	for _, port := range ports {
		elements = append(elements, strconv.Itoa(port))
	}

	return strings.Join(elements, ", ")
}

func writeSet(b *strings.Builder, name, kind string, elements []string) {
	if len(elements) == 0 {
		return
	}

	fmt.Fprintf(b, "\tset %s {\n\t\ttype %s\n\t\tflags interval\n\t\tauto-merge\n\t\telements = { %s }\n\t}\n",
		name, kind, strings.Join(elements, ", "))
}

// cleanConfig validates and de-duplicates a config, returning ErrInvalidConfig
// for a port out of range or an address that is not a plain IP or CIDR in its
// usual form.
func cleanConfig(config Config) (Config, error) {
	out := Config{Enabled: config.Enabled}

	var err error
	if out.TCPPorts, err = cleanPorts(config.TCPPorts); err != nil {
		return Config{}, err
	}
	if out.UDPPorts, err = cleanPorts(config.UDPPorts); err != nil {
		return Config{}, err
	}
	if out.AllowIPs, err = cleanIPs(config.AllowIPs); err != nil {
		return Config{}, err
	}
	if out.DenyIPs, err = cleanIPs(config.DenyIPs); err != nil {
		return Config{}, err
	}

	return out, nil
}

func cleanPorts(ports []int) ([]int, error) {
	seen := map[int]bool{}
	out := []int{}

	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("%w: port %d", ErrInvalidConfig, port)
		}

		if !seen[port] {
			seen[port] = true
			out = append(out, port)
		}
	}

	return out, nil
}

func cleanIPs(ips []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}

	for _, raw := range ips {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}

		value, err := cleanIPEntry(entry)
		if err != nil {
			return nil, err
		}

		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}

	return out, nil
}

// cleanIPEntry returns entry when it is a plain IP or a CIDR range in its
// usual form, else ErrInvalidConfig.
func cleanIPEntry(entry string) (string, error) {
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil || prefix.String() != entry || prefix.Addr().Zone() != "" {
			return "", fmt.Errorf("%w: %q", ErrInvalidConfig, entry)
		}

		return prefix.String(), nil
	}

	address, err := netip.ParseAddr(entry)
	if err != nil || address.String() != entry || address.Zone() != "" || address.Is4In6() {
		return "", fmt.Errorf("%w: %q", ErrInvalidConfig, entry)
	}

	return address.String(), nil
}

// splitFamilies sorts cleaned IPs and CIDRs into IPv4 and IPv6; a colon marks
// IPv6.
func splitFamilies(ips []string) (v4, v6 []string) {
	for _, ip := range ips {
		if strings.Contains(ip, ":") {
			v6 = append(v6, ip)
		} else {
			v4 = append(v4, ip)
		}
	}

	return v4, v6
}

func dedupePorts(ports []int) []int {
	seen := map[int]bool{}
	out := []int{}

	for _, port := range ports {
		if !seen[port] {
			seen[port] = true
			out = append(out, port)
		}
	}

	sort.Ints(out)

	return out
}

// detectSSHPorts reads the ports sshd listens on from its effective config,
// falling back to 22 so SSH is never left closed.
func detectSSHPorts(ctx context.Context) []int {
	out, err := runner.Run(ctx, sshdProgram, "-T")
	if err != nil {
		return []int{22}
	}

	ports := []int{}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "port "); ok {
			if port, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil && port > 0 && port < 65536 {
				ports = append(ports, port)
			}
		}
	}

	if len(ports) == 0 {
		return []int{22}
	}

	return ports
}

// detectPanelPort reads the panel's port, falling back to the default.
func detectPanelPort() int {
	content, err := os.ReadFile(panelPortFile)
	if err != nil {
		return defaultPanel
	}

	if port, err := strconv.Atoi(strings.TrimSpace(string(content))); err == nil && port > 0 && port < 65536 {
		return port
	}

	return defaultPanel
}
