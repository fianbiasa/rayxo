package redis

import "rayxo/agent/internal/drift"

// Check reports whether an account's Redis config on disk differs from what
// RAYXO would write, without changing it.
func (m *Manager) Check(in Instance) (drift.Report, error) {
	conf, err := m.renderConf(in)
	if err != nil {
		return drift.Report{}, err
	}

	var report drift.Report
	report.Add(drift.CompareFile("redis-conf", m.confPath(in.User), conf))

	return report, nil
}
