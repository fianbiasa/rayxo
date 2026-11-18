package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeNft records every ruleset loaded into nft.
type fakeNft struct {
	inputs [][]byte
	err    error
}

func (f *fakeNft) run(_ context.Context, input []byte, _ string, _ ...string) ([]byte, error) {
	f.inputs = append(f.inputs, input)

	return nil, f.err
}

func (f *fakeNft) last() string {
	if len(f.inputs) == 0 {
		return ""
	}

	return string(f.inputs[len(f.inputs)-1])
}

func newManager(t *testing.T, nft *fakeNft, now *time.Time) *Manager {
	t.Helper()

	return New(ManagerConfig{
		Run:       nft.run,
		StateFile: filepath.Join(t.TempDir(), "firewall.json"),
		Window:    5 * time.Minute,
		Now:       func() time.Time { return *now },
		SSHPorts:  func(context.Context) []int { return []int{22} },
		PanelPort: func() int { return 2222 },
	})
}

func TestRenderOffLeavesNoTable(t *testing.T) {
	out := render(Config{Enabled: false}, []int{22}, []int{53})

	if !strings.Contains(out, "delete table inet rayxo") {
		t.Fatalf("off ruleset should delete the table:\n%s", out)
	}
	if strings.Contains(out, "chain input") {
		t.Fatalf("off ruleset should define no chain:\n%s", out)
	}
}

func TestRenderOnOpensServiceSSHPanelAdminAndPassivePorts(t *testing.T) {
	out := render(Config{Enabled: true, TCPPorts: []int{8443}, UDPPorts: []int{1194}}, []int{22, 2222, 80, 443, 53}, []int{53})

	for _, want := range []string{
		"type filter hook input priority 0; policy drop;",
		"ct state established,related accept",
		"iifname \"lo\" accept",
		"meta l4proto { icmp, ipv6-icmp } accept",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}

	for _, port := range []string{"22", "2222", "80", "443", "8443", "35000-35999"} {
		if !strings.Contains(out, port) {
			t.Fatalf("TCP port %s should be open:\n%s", port, out)
		}
	}
	if !strings.Contains(out, "udp dport") || !strings.Contains(out, "1194") {
		t.Fatalf("UDP ports should be open:\n%s", out)
	}
}

func TestRenderAllowAndDenySets(t *testing.T) {
	out := render(Config{Enabled: true, AllowIPs: []string{"198.51.100.4"}, DenyIPs: []string{"203.0.113.0/24", "2001:db8::/32"}}, []int{22}, []int{53})

	for _, want := range []string{
		"set allow4 {",
		"198.51.100.4",
		"ip saddr @allow4 accept",
		"set deny4 {",
		"203.0.113.0/24",
		"ip saddr @deny4 drop",
		"set deny6 {",
		"2001:db8::/32",
		"ip6 saddr @deny6 drop",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestApplyEnableOpensTrialAndLoadsRules(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := newManager(t, nft, &now)

	status, err := m.Apply(context.Background(), Config{Enabled: true, TCPPorts: []int{8443}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if status.Pending == nil {
		t.Fatal("enabling should open a trial")
	}
	if !status.Pending.ExpiresAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("trial should expire after the window, got %v", status.Pending.ExpiresAt)
	}
	if status.Active.Enabled {
		t.Fatal("the confirmed config should still be off until the trial is confirmed")
	}
	if !strings.Contains(nft.last(), "policy drop") || !strings.Contains(nft.last(), "8443") {
		t.Fatalf("the trial rules were not loaded:\n%s", nft.last())
	}
}

func TestConfirmKeepsConfigWithoutReapplying(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := newManager(t, nft, &now)

	if _, err := m.Apply(context.Background(), Config{Enabled: true, TCPPorts: []int{8443}}); err != nil {
		t.Fatal(err)
	}
	loads := len(nft.inputs)

	status, err := m.Confirm(context.Background())
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	if status.Pending != nil {
		t.Fatal("confirming should clear the pending change")
	}
	if !status.Active.Enabled || len(status.Active.TCPPorts) != 1 || status.Active.TCPPorts[0] != 8443 {
		t.Fatalf("the confirmed config is wrong: %+v", status.Active)
	}
	if len(nft.inputs) != loads {
		t.Fatal("confirming must not re-load the rules; they are already live")
	}
}

func TestTrialAutoRevertsWhenExpired(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := newManager(t, nft, &now)

	if _, err := m.Apply(context.Background(), Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(6 * time.Minute) // past the window
	if err := m.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if !strings.Contains(nft.last(), "delete table inet rayxo") || strings.Contains(nft.last(), "chain input") {
		t.Fatalf("an unconfirmed trial should revert to off:\n%s", nft.last())
	}

	status, _ := m.Status(context.Background())
	if status.Pending != nil || status.Active.Enabled {
		t.Fatalf("after revert the firewall should be off with no trial: %+v", status)
	}
}

func TestTrialStaysBeforeExpiry(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := newManager(t, nft, &now)

	if _, err := m.Apply(context.Background(), Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	loads := len(nft.inputs)

	now = now.Add(1 * time.Minute) // still within the window
	if err := m.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(nft.inputs) != loads {
		t.Fatal("a trial within its window must not revert")
	}
	status, _ := m.Status(context.Background())
	if status.Pending == nil {
		t.Fatal("the trial should still be pending")
	}
}

func TestApplyWhileTrialPendingIsRefused(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := newManager(t, nft, &now)

	if _, err := m.Apply(context.Background(), Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply(context.Background(), Config{Enabled: true, TCPPorts: []int{9000}}); !errors.Is(err, ErrTrialRunning) {
		t.Fatalf("want ErrTrialRunning, got %v", err)
	}
}

func TestDisableIsImmediateNoTrial(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := newManager(t, nft, &now)

	status, err := m.Apply(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Apply off: %v", err)
	}

	if status.Pending != nil || status.Active.Enabled {
		t.Fatalf("disabling should be immediate and leave the firewall off: %+v", status)
	}
	if !strings.Contains(nft.last(), "delete table inet rayxo") || strings.Contains(nft.last(), "chain input") {
		t.Fatalf("disabling should load the off ruleset:\n%s", nft.last())
	}
}

func TestCancelRevertsToConfirmed(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := newManager(t, nft, &now)

	// Confirm an enabled baseline, then start another change and cancel it.
	if _, err := m.Apply(context.Background(), Config{Enabled: true, TCPPorts: []int{8443}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Confirm(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(context.Background(), Config{Enabled: true, TCPPorts: []int{9999}}); err != nil {
		t.Fatal(err)
	}

	status, err := m.Cancel(context.Background())
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if status.Pending != nil {
		t.Fatal("cancel should clear the pending change")
	}
	if len(status.Active.TCPPorts) != 1 || status.Active.TCPPorts[0] != 8443 {
		t.Fatalf("cancel should revert to the confirmed config: %+v", status.Active)
	}
	if !strings.Contains(nft.last(), "8443") || strings.Contains(nft.last(), "9999") {
		t.Fatalf("cancel should re-load the confirmed rules:\n%s", nft.last())
	}
}

func TestRestoreReappliesConfirmedAndDropsPending(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := newManager(t, nft, &now)

	if _, err := m.Apply(context.Background(), Config{Enabled: true, TCPPorts: []int{8443}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Confirm(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(context.Background(), Config{Enabled: true, TCPPorts: []int{9999}}); err != nil {
		t.Fatal(err)
	}

	if err := m.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	status, _ := m.Status(context.Background())
	if status.Pending != nil {
		t.Fatal("restore should drop a pending change")
	}
	if len(status.Active.TCPPorts) != 1 || status.Active.TCPPorts[0] != 8443 {
		t.Fatalf("restore should keep the confirmed config: %+v", status.Active)
	}
	if !strings.Contains(nft.last(), "8443") {
		t.Fatalf("restore should re-load the confirmed rules:\n%s", nft.last())
	}
}

func TestInvalidConfigIsRejectedAndNothingApplied(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := newManager(t, nft, &now)

	for _, bad := range []Config{
		{Enabled: true, TCPPorts: []int{70000}},
		{Enabled: true, DenyIPs: []string{"not-an-ip"}},
		{Enabled: true, AllowIPs: []string{"evil; rm -rf"}},
	} {
		if _, err := m.Apply(context.Background(), bad); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("Apply(%+v) should be ErrInvalidConfig, got %v", bad, err)
		}
	}

	if len(nft.inputs) != 0 {
		t.Fatalf("a rejected config must not touch nft: %d loads", len(nft.inputs))
	}
}

func TestSSHAndPanelPortsAreAlwaysOpen(t *testing.T) {
	nft := &fakeNft{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// SSH on a non-standard port and a moved panel port must still be opened.
	m := New(ManagerConfig{
		Run:       nft.run,
		StateFile: filepath.Join(t.TempDir(), "firewall.json"),
		Window:    5 * time.Minute,
		Now:       func() time.Time { return now },
		SSHPorts:  func(context.Context) []int { return []int{2269} },
		PanelPort: func() int { return 8443 },
	})

	status, err := m.Apply(context.Background(), Config{Enabled: true}) // no admin ports at all
	if err != nil {
		t.Fatal(err)
	}

	for _, port := range []string{"2269", "8443"} {
		if !strings.Contains(nft.last(), port) {
			t.Fatalf("SSH/panel port %s must always be open even with an empty config:\n%s", port, nft.last())
		}
	}
	if !containsInt(status.AlwaysOpenTCP, 2269) || !containsInt(status.AlwaysOpenTCP, 8443) {
		t.Fatalf("status should report SSH and panel as always-open: %v", status.AlwaysOpenTCP)
	}
}

func containsInt(list []int, value int) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}

	return false
}

// TestStatusMarshalsConfigArraysNotNull guards the default state (no state
// file, firewall off): the config's port and IP fields must marshal to JSON
// arrays, never null, or the panel crashes reading them (.map() on null).
func TestStatusMarshalsConfigArraysNotNull(t *testing.T) {
	now := time.Now()
	m := newManager(t, &fakeNft{}, &now)

	status, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for _, want := range []string{
		`"tcp_ports":[]`,
		`"udp_ports":[]`,
		`"allow_ips":[]`,
		`"deny_ips":[]`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("status JSON missing %s:\n%s", want, encoded)
		}
	}
}
