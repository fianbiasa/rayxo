package nodeapp

import "rayxo/agent/internal/drift"

// Check reports whether a domain's Node.js app systemd unit and environment
// file on disk differ from what RAYXO would write, without changing them.
func (m *Manager) Check(app App) (drift.Report, error) {
	unit, err := m.renderUnit(app)
	if err != nil {
		return drift.Report{}, err
	}

	var report drift.Report
	report.Add(drift.CompareFile("node-env", m.envPath(app.Domain), renderEnv(app)))
	report.Add(drift.CompareFile("node-unit", m.unitPath(app.Domain), unit))

	return report, nil
}
