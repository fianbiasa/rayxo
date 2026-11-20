package mail

import (
	"os"
	"path/filepath"

	"rayxo/agent/internal/drift"
	"rayxo/agent/internal/sysuser"
	"rayxo/agent/internal/validate"
)

// CheckSieve reports whether a mailbox's Sieve script on disk differs from the
// script RAYXO would write, without changing it. An empty script means no
// custom script: a file being present is then the drift.
func (m *Manager) CheckSieve(account sysuser.User, domain, localPart, script string) (drift.Report, error) {
	if err := validate.Domain(domain); err != nil {
		return drift.Report{}, err
	}

	if err := ValidateLocalPart(localPart); err != nil {
		return drift.Report{}, err
	}

	if err := ValidateSieve(script); err != nil {
		return drift.Report{}, err
	}

	path := filepath.Join(account.Home, imapDir, domain, localPart, SieveFile)

	var report drift.Report

	if script == "" {
		if _, err := os.Stat(path); err == nil {
			report.Add(drift.Target{Kind: "mail-sieve", Path: path, Drifted: true, Diff: "a Sieve script is present but none is configured"})
		} else {
			report.Add(drift.Target{Kind: "mail-sieve", Path: path})
		}

		return report, nil
	}

	report.Add(drift.CompareFile("mail-sieve", path, []byte(script)))

	return report, nil
}
