package bfm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClient stands in for fail2ban-client: it records every call and returns
// the output or error set for the arguments it is given.
type fakeClient struct {
	outputs map[string]string
	errs    map[string]error
	calls   [][]string
}

func newFakeClient() *fakeClient {
	return &fakeClient{outputs: map[string]string{}, errs: map[string]error{}}
}

func (f *fakeClient) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	key := strings.Join(args, " ")
	if err := f.errs[key]; err != nil {
		return nil, err
	}

	return []byte(f.outputs[key]), nil
}

func (f *fakeClient) ran(args ...string) bool {
	key := strings.Join(args, " ")
	for _, call := range f.calls {
		if strings.Join(call, " ") == key {
			return true
		}
	}

	return false
}

const sshdStatus = `Status for the jail: sshd
|- Filter
|  |- Currently failed: 2
|  |- Total failed:     9
|  ` + "`" + `- Journal matches:  _SYSTEMD_UNIT=sshd.service
` + "`" + `- Actions
   |- Currently banned: 1
   |- Total banned:     3
   ` + "`" + `- Banned IP list:   203.0.113.7`

const dovecotStatus = `Status for the jail: dovecot
|- Filter
|  |- Currently failed: 0
|  |- Total failed:     0
|  ` + "`" + `- Journal matches:  _SYSTEMD_UNIT=dovecot.service
` + "`" + `- Actions
   |- Currently banned: 0
   |- Total banned:     0
   ` + "`" + `- Banned IP list:`

func newManager(t *testing.T, client *fakeClient) *Manager {
	t.Helper()

	return New(Config{Run: client.run, WhitelistFile: filepath.Join(t.TempDir(), "jail.d", "rayxo-whitelist.local")})
}

func TestStatusParsesJailsCountsAndBans(t *testing.T) {
	client := newFakeClient()
	client.outputs["status"] = "Status\n|- Number of jail:\t2\n`- Jail list:\tsshd, dovecot\n"
	client.outputs["status sshd"] = sshdStatus
	client.outputs["status dovecot"] = dovecotStatus

	report, err := newManager(t, client).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if len(report.Jails) != 2 {
		t.Fatalf("got %d jails, want 2: %+v", len(report.Jails), report.Jails)
	}

	sshd := report.Jails[0]
	if sshd.Name != "sshd" || sshd.CurrentlyFailed != 2 || sshd.TotalFailed != 9 || sshd.CurrentlyBanned != 1 || sshd.TotalBanned != 3 {
		t.Fatalf("sshd parsed wrong: %+v", sshd)
	}

	if len(sshd.BannedIPs) != 1 || sshd.BannedIPs[0] != "203.0.113.7" {
		t.Fatalf("sshd banned IPs wrong: %v", sshd.BannedIPs)
	}

	if dovecot := report.Jails[1]; dovecot.Name != "dovecot" || len(dovecot.BannedIPs) != 0 {
		t.Fatalf("dovecot parsed wrong: %+v", dovecot)
	}
}

func TestStatusReadsManagedWhitelist(t *testing.T) {
	client := newFakeClient()
	client.outputs["status"] = "`- Jail list:\tsshd\n"
	client.outputs["status sshd"] = sshdStatus

	manager := newManager(t, client)
	if err := os.MkdirAll(filepath.Dir(manager.cfg.WhitelistFile), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(manager.cfg.WhitelistFile, []byte("[DEFAULT]\nignoreip = 127.0.0.1/8 ::1 198.51.100.4 203.0.113.0/24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if len(report.Whitelist) != 2 || report.Whitelist[0] != "198.51.100.4" || report.Whitelist[1] != "203.0.113.0/24" {
		t.Fatalf("whitelist should drop the loopback base: %v", report.Whitelist)
	}
}

func TestUnbanValidatesThenCalls(t *testing.T) {
	client := newFakeClient()
	manager := newManager(t, client)

	if err := manager.Unban(context.Background(), "203.0.113.7"); err != nil {
		t.Fatalf("Unban: %v", err)
	}

	if !client.ran("unban", "203.0.113.7") {
		t.Fatalf("did not call fail2ban-client unban: %v", client.calls)
	}

	client.calls = nil
	if err := manager.Unban(context.Background(), "not-an-ip"); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("want ErrInvalidAddress, got %v", err)
	}

	if len(client.calls) != 0 {
		t.Fatalf("a bad address must not reach fail2ban-client: %v", client.calls)
	}
}

func TestUnbanRejectsRangeAndNonCanonical(t *testing.T) {
	manager := newManager(t, newFakeClient())

	for _, bad := range []string{"203.0.113.0/24", "::ffff:203.0.113.7", "203.0.113.007"} {
		if err := manager.Unban(context.Background(), bad); !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("Unban(%q) should be rejected, got %v", bad, err)
		}
	}
}

func TestSetWhitelistWritesKeepsLoopbackAndReloads(t *testing.T) {
	client := newFakeClient()
	manager := newManager(t, client)

	if err := manager.SetWhitelist(context.Background(), []string{"198.51.100.4", "203.0.113.0/24"}); err != nil {
		t.Fatalf("SetWhitelist: %v", err)
	}

	content, err := os.ReadFile(manager.cfg.WhitelistFile)
	if err != nil {
		t.Fatalf("read whitelist: %v", err)
	}

	want := "ignoreip = 127.0.0.1/8 ::1 198.51.100.4 203.0.113.0/24"
	if !strings.Contains(string(content), want) {
		t.Fatalf("whitelist file missing %q:\n%s", want, content)
	}

	if !client.ran("reload") {
		t.Fatalf("a changed whitelist must reload fail2ban: %v", client.calls)
	}
}

func TestSetWhitelistSkipsReloadWhenUnchanged(t *testing.T) {
	client := newFakeClient()
	manager := newManager(t, client)

	if err := manager.SetWhitelist(context.Background(), []string{"198.51.100.4"}); err != nil {
		t.Fatalf("first SetWhitelist: %v", err)
	}

	client.calls = nil
	if err := manager.SetWhitelist(context.Background(), []string{"198.51.100.4"}); err != nil {
		t.Fatalf("second SetWhitelist: %v", err)
	}

	if client.ran("reload") {
		t.Fatalf("an unchanged whitelist must not reload fail2ban: %v", client.calls)
	}
}

func TestSetWhitelistRejectsBadEntryAndWritesNothing(t *testing.T) {
	client := newFakeClient()
	manager := newManager(t, client)

	if err := manager.SetWhitelist(context.Background(), []string{"198.51.100.4", "evil; rm -rf"}); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("want ErrInvalidAddress, got %v", err)
	}

	if _, err := os.Stat(manager.cfg.WhitelistFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a rejected whitelist must not write the file")
	}

	if len(client.calls) != 0 {
		t.Fatalf("a rejected whitelist must not touch fail2ban: %v", client.calls)
	}
}

func TestSetWhitelistDropsDuplicatesAndLoopback(t *testing.T) {
	client := newFakeClient()
	manager := newManager(t, client)

	if err := manager.SetWhitelist(context.Background(), []string{"127.0.0.1/8", "198.51.100.4", "198.51.100.4"}); err != nil {
		t.Fatalf("SetWhitelist: %v", err)
	}

	content, _ := os.ReadFile(manager.cfg.WhitelistFile)
	if got := strings.Count(string(content), "198.51.100.4"); got != 1 {
		t.Fatalf("198.51.100.4 should appear once, appeared %d times:\n%s", got, content)
	}

	if strings.Count(string(content), "127.0.0.1/8") != 1 {
		t.Fatalf("loopback should appear once (from the base), not be duplicated:\n%s", content)
	}
}
