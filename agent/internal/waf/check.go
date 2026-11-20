package waf

import "rayxo/agent/internal/drift"

// Check reports whether the global ModSecurity engine file on disk differs
// from what the given mode would write, without changing anything.
func (m *Manager) Check(s Settings) (drift.Report, error) {
	directive, err := engineDirective(s.Mode)
	if err != nil {
		return drift.Report{}, err
	}

	var report drift.Report
	report.Add(drift.CompareFile("waf-engine", m.cfg.EngineFile, engineFileContent(directive)))

	return report, nil
}
