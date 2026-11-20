package cgroup

import (
	"path/filepath"

	"rayxo/agent/internal/drift"
	"rayxo/agent/internal/validate"
)

// Check reports whether an account's resource-limit slice unit and its
// per-user drop-in on disk differ from what the given limits would write,
// without changing anything. An account with no limits should have neither
// file.
func (m *Manager) Check(user string, uid int, limits Limits) (drift.Report, error) {
	if err := validate.Username(user); err != nil {
		return drift.Report{}, err
	}

	if err := limits.Check(); err != nil {
		return drift.Report{}, err
	}

	var report drift.Report

	slicePath := filepath.Join(m.cfg.UnitDir, sliceName(user))
	dropInPath := m.userDropIn(uid)

	if limits.None() {
		report.Add(drift.CompareAbsent("cgroup-slice", slicePath))
		report.Add(drift.CompareAbsent("cgroup-dropin", dropInPath))

		return report, nil
	}

	settings := sliceSettings(limits)
	unit := "[Unit]\nDescription=Hosting account " + user + " (rayxo)\n\n" + settings

	report.Add(drift.CompareFile("cgroup-slice", slicePath, []byte(unit)))
	report.Add(drift.CompareFile("cgroup-dropin", dropInPath, []byte(settings)))

	return report, nil
}
