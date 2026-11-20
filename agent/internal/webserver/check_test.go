package webserver

import (
	"strings"
	"testing"
)

func TestCheckDomainReportsNoDriftAfterApply(t *testing.T) {
	env := newEnv(t)
	env.mustApply(budiSite)

	report, err := env.manager.CheckDomain(budiSite)
	if err != nil {
		t.Fatal(err)
	}

	if report.Drifted() {
		t.Fatalf("a freshly applied site reported drift: %+v", report.Targets)
	}

	if len(report.Targets) == 0 {
		t.Fatal("no targets were checked")
	}
}

func TestCheckDomainDetectsAHandEditedVhost(t *testing.T) {
	env := newEnv(t)
	env.mustApply(budiSite)

	path := env.nginxVhost("budi", "example.com")
	original := env.read(path)
	env.write(path, original+"\n# hand edit\n")

	report, err := env.manager.CheckDomain(budiSite)
	if err != nil {
		t.Fatal(err)
	}

	if !report.Drifted() {
		t.Fatal("a hand-edited vhost was not reported as drifted")
	}

	var found bool
	for _, target := range report.Targets {
		if target.Path == path {
			found = target.Drifted && strings.Contains(target.Diff, "hand edit")
		}
	}

	if !found {
		t.Fatalf("the drifted vhost was not reported with a diff: %+v", report.Targets)
	}

	// Check-mode must never write: the hand edit is still there.
	if !strings.Contains(env.read(path), "# hand edit") {
		t.Fatal("CheckDomain rewrote the file instead of only reporting")
	}
}
