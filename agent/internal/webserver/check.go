package webserver

import "rayxo/agent/internal/drift"

// CheckDomain renders the domain's Nginx vhost, Apache vhost and PHP-FPM pool
// section and reports where the files on disk differ from what RAYXO would
// write, without changing anything. Reads are safe without the write lock
// because every write is an atomic rename, so a read sees whole content.
func (m *Manager) CheckDomain(site Site) (drift.Report, error) {
	if err := site.Validate(); err != nil {
		return drift.Report{}, err
	}

	site.Kind = site.kind()

	var report drift.Report
	data := m.siteData(site)

	nginx, err := render("nginx-vhost.conf.tmpl", data)
	if err != nil {
		return drift.Report{}, err
	}

	report.Add(drift.CompareFile("nginx-vhost", m.nginxVhost(site.User, site.Domain), nginx))

	if site.Kind != KindForward && !site.NodeApp {
		apache, err := render("apache-vhost.conf.tmpl", data)
		if err != nil {
			return drift.Report{}, err
		}

		report.Add(drift.CompareFile("apache-vhost", m.apacheVhost(site.User, site.Domain), apache))
	}

	if site.Kind != KindForward && !site.NoPHP && !site.NodeApp {
		section, err := render("php-fpm-site.conf.tmpl", m.sitePoolData(site))
		if err != nil {
			return drift.Report{}, err
		}

		report.Add(drift.CompareFile("php-pool", m.poolSectionFile(site.User, site.PHPVersion, site.Domain), section))
	}

	return report, nil
}
