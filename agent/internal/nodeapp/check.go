package nodeapp

import (
	"strings"

	"rayxo/agent/internal/drift"
)

// Check reports whether a domain's Node.js app systemd unit and environment
// file on disk differ from what RAYXO would write, without changing them.
func (m *Manager) Check(app App) (drift.Report, error) {
	unit, err := m.renderUnit(app)
	if err != nil {
		return drift.Report{}, err
	}

	var report drift.Report
	// The env file holds the account's own secrets, so its diff is masked; the
	// systemd unit has none.
	report.Add(drift.CompareFileMasked("node-env", m.envPath(app.Domain), m.renderEnv(app), maskNodeEnv))
	report.Add(drift.CompareFile("node-unit", m.unitPath(app.Domain), unit))

	return report, nil
}

// nodeEnvPublicKeys are the variables RAYXO itself sets, which are safe to show
// in a drift diff; every other line is an account-supplied value and is hidden.
var nodeEnvPublicKeys = map[string]bool{
	"NODE_ENV":          true,
	"NITRO_UNIX_SOCKET": true,
	"RAYXO_NODE_SOCKET": true,
	"PORT":              true,
}

// maskNodeEnv hides the value of every account-supplied environment variable so
// a drift diff shown in the panel never leaks a secret the account set.
func maskNodeEnv(line string) string {
	if line == "" || strings.HasPrefix(line, "#") {
		return line
	}

	key, _, ok := strings.Cut(line, "=")
	if !ok || nodeEnvPublicKeys[key] {
		return line
	}

	return key + "=(redacted)"
}
