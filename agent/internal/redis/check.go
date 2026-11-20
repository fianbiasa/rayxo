package redis

import (
	"strings"

	"rayxo/agent/internal/drift"
)

// Check reports whether an account's Redis config on disk differs from what
// RAYXO would write, without changing it.
func (m *Manager) Check(in Instance) (drift.Report, error) {
	conf, err := m.renderConf(in)
	if err != nil {
		return drift.Report{}, err
	}

	var report drift.Report
	report.Add(drift.CompareFileMasked("redis-conf", m.confPath(in.User), conf, maskRedisConf))

	return report, nil
}

// maskRedisConf hides the instance password so it never appears in a drift
// diff shown in the panel; every other line (socket path, memory cap) is safe
// to show.
func maskRedisConf(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "requirepass ") {
		return "requirepass \"(redacted)\""
	}

	return line
}
