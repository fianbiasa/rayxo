package webserver

import (
	"strings"
	"testing"
)

// A domain running a Node.js app is reverse-proxied straight to the app on its
// per-account unix socket, with the headers a WebSocket upgrade needs, and gets
// no PHP pool and no Apache vhost.
func TestApplyNodeAppProxiesToTheAppAndWritesNoApache(t *testing.T) {
	env := newEnv(t)

	site := budiSite
	site.NodeApp = true
	site.NodePort = 40123

	if _, err := env.apply(site); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	nginx := env.read(env.nginxVhost("budi", "example.com"))

	assertContains(t, "nginx vhost", nginx,
		"server_name example.com www.example.com;",
		"location / {\n        proxy_pass http://unix:/run/rayxo-nodeapp/budi/example.com.sock:;",
		"proxy_http_version 1.1;",
		"proxy_set_header Upgrade $http_upgrade;",
		"proxy_set_header Connection $connection_upgrade;",
		"location ~ /\\.(?!well-known(?:/|$)) {",
		"include "+env.cfg.NginxDir+"/budi/suspended*.inc;",
	)

	// It is not served by Apache/PHP: no proxy to Apache and no PHP handler.
	assertNotContains(t, "nginx vhost", nginx,
		"rayxo_apache_tls", "127.0.0.1:8080", "@apache", "fastcgi", "root /home", "<no value>",
	)

	if open, close := strings.Count(nginx, "{"), strings.Count(nginx, "}"); open != close {
		t.Fatalf("unbalanced braces (%d/%d):\n%s", open, close, nginx)
	}

	// No Apache vhost and no PHP-FPM pool for a Node.js app.
	env.assertMissing(env.apacheVhost("budi", "example.com"))
	env.assertMissing(env.poolFile("budi", "8.4"))
}

// Over HTTPS the domain's app is reached the same way: Nginx terminates TLS and
// proxies to the app on its unix socket (there is no Apache-over-TLS upstream).
func TestApplyNodeAppOverHTTPSStillProxiesToTheApp(t *testing.T) {
	env := newEnv(t)
	env.writeCertificate("example.com", "example.com")

	site := budiSite
	site.NodeApp = true
	site.NodePort = 40200
	site.SSL = true

	if _, err := env.apply(site); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	nginx := env.read(env.nginxVhost("budi", "example.com"))

	if strings.Count(nginx, "proxy_pass http://unix:/run/rayxo-nodeapp/budi/example.com.sock:;") != 2 {
		t.Fatalf("both the HTTP and HTTPS servers should proxy to the app:\n%s", nginx)
	}

	assertContains(t, "nginx vhost", nginx, "listen 443 ssl http2;")
	assertNotContains(t, "nginx vhost", nginx, "rayxo_apache_tls")
}
