package webserver

import "rayxo/agent/internal/drift"

// CheckDomain renders every file RAYXO writes for a domain and its account —
// the Nginx and Apache vhosts, the PHP-FPM pool section and master, the
// php-guard prepend, the password-protected-folder htpasswd files, and the
// suspended page when the account is suspended — and reports where the files
// on disk differ, without changing anything. Reads are safe without the write
// lock because every write is an atomic rename, so a read sees whole content.
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

	// htpasswd of the domain's password-protected folders. Only the files
	// that should exist are checked; removal entries (empty content) are
	// skipped so a correctly-removed file is not reported as missing.
	changes, err := m.htpasswdChanges(site)
	if err != nil {
		return drift.Report{}, err
	}

	for _, change := range changes {
		if len(change.content) > 0 {
			report.Add(drift.CompareFile("htpasswd", change.path, change.content))
		}
	}

	if site.Kind != KindForward && !site.NoPHP && !site.NodeApp {
		section, err := render("php-fpm-site.conf.tmpl", m.sitePoolData(site))
		if err != nil {
			return drift.Report{}, err
		}

		report.Add(drift.CompareFile("php-pool", m.poolSectionFile(site.User, site.PHPVersion, site.Domain), section))

		vhosts, err := m.apacheVhosts(site.User, site.Domain)
		if err != nil {
			return drift.Report{}, err
		}

		legacy := usesSocket(vhosts, m.legacyPoolData(site.User, site.PHPVersion).Socket)

		main, err := m.renderMaster(site.User, site.PHPVersion, legacy, true)
		if err != nil {
			return drift.Report{}, err
		}

		report.Add(drift.CompareFile("php-pool-master", m.poolFile(site.User, site.PHPVersion), main))

		guard, err := render("php-guard.php.tmpl", accountData{User: site.User})
		if err != nil {
			return drift.Report{}, err
		}

		report.Add(drift.CompareFile("php-guard", m.guardFile(site.User), guard))
	}

	if site.Suspended {
		page, err := render("suspended.inc.tmpl", m.accountData(site.User))
		if err != nil {
			return drift.Report{}, err
		}

		report.Add(drift.CompareFile("suspended-page", m.suspendedFile(site.User), page))
	}

	return report, nil
}

// CheckLoginDomain renders a reseller's login-site vhost and reports whether
// the file on disk differs, without changing it.
func (m *Manager) CheckLoginDomain(domain string) (drift.Report, error) {
	content, err := m.renderLoginSite(domain)
	if err != nil {
		return drift.Report{}, err
	}

	var report drift.Report
	report.Add(drift.CompareFile("login-site", m.loginSite(domain), content))

	return report, nil
}
