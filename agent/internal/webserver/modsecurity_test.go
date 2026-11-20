package webserver

import "testing"

// Every Apache vhost sends its ModSecurity audit log to a per-domain file,
// guarded by IfModule so it stays valid when the module is not loaded, and
// emits no per-site SecRuleEngine when the site inherits the global default.
func TestApacheVhostModSecurityInheritsByDefault(t *testing.T) {
	env := newEnv(t)

	if _, err := env.apply(budiSite); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	apache := env.read(env.apacheVhost("budi", "example.com"))

	assertContains(t, "apache vhost", apache,
		"<IfModule security2_module>",
		"SecAuditLog "+env.cfg.LogDir+"/example.com.modsec_audit.log",
	)
	assertNotContains(t, "apache vhost", apache, "SecRuleEngine")
}

// A per-domain override emits the matching SecRuleEngine inside the IfModule
// block, so the website can turn the WAF on, off or to detection-only.
func TestApacheVhostModSecurityOverride(t *testing.T) {
	cases := map[ModSecurityMode]string{
		ModSecurityOff:       "SecRuleEngine Off",
		ModSecurityDetection: "SecRuleEngine DetectionOnly",
		ModSecurityOn:        "SecRuleEngine On",
	}

	for mode, directive := range cases {
		t.Run(string(mode), func(t *testing.T) {
			env := newEnv(t)

			site := budiSite
			site.ModSecurity = mode

			if _, err := env.apply(site); err != nil {
				t.Fatalf("apply failed: %v", err)
			}

			assertContains(t, "apache vhost", env.read(env.apacheVhost("budi", "example.com")), directive)
		})
	}
}

// An unknown ModSecurity mode is refused before anything is written.
func TestValidateRejectsUnknownModSecurityMode(t *testing.T) {
	site := budiSite
	site.ModSecurity = "paranoid"

	if err := site.Validate(); err == nil {
		t.Fatal("expected an error for an unknown ModSecurity mode")
	}
}
