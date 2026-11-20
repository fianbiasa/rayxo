package dns

import (
	"context"
	"fmt"
	"strings"

	"rayxo/agent/internal/drift"
)

// CheckZone reports whether the zone in PowerDNS differs from what RAYXO would
// write, without changing it. It reuses the same diff the sync uses, so a
// serial-only difference (which RAYXO bumps on every real change) is not drift.
func (m *Manager) CheckZone(ctx context.Context, zone Zone) (drift.Report, error) {
	if err := Validate(zone); err != nil {
		return drift.Report{}, err
	}

	want, err := buildRRsets(zone)
	if err != nil {
		return drift.Report{}, err
	}

	current, found, err := m.getZone(ctx, zone.Name)
	if err != nil {
		return drift.Report{}, err
	}

	var report drift.Report

	if !found {
		report.Add(drift.Target{Kind: "dns-zone", Path: zone.Name, Drifted: true, Missing: true, Diff: "zone is missing in PowerDNS"})

		return report, nil
	}

	changes := diff(current, want, zone)

	// diff always re-asserts the SOA (to bump the serial), so a lone SOA
	// change with no other records is steady state, not drift.
	if onlySOA(changes) {
		report.Add(drift.Target{Kind: "dns-zone", Path: zone.Name})

		return report, nil
	}

	report.Add(drift.Target{Kind: "dns-zone", Path: zone.Name, Drifted: true, Diff: summarizeChanges(changes)})

	return report, nil
}

// onlySOA reports whether changes is empty or only the SOA re-assert.
func onlySOA(changes []apiRRset) bool {
	for _, c := range changes {
		if c.Type != "SOA" {
			return false
		}
	}

	return true
}

// summarizeChanges renders the drift as a short, bounded list of record
// changes PowerDNS would need to match RAYXO.
func summarizeChanges(changes []apiRRset) string {
	var b strings.Builder

	for i, c := range changes {
		if c.Type == "SOA" {
			continue
		}

		if i > 60 {
			b.WriteString("… (more)\n")

			break
		}

		verb := "set"
		if c.ChangeType == "DELETE" {
			verb = "remove"
		}

		fmt.Fprintf(&b, "%s %s %s\n", verb, strings.TrimSuffix(c.Name, "."), c.Type)
	}

	return b.String()
}
