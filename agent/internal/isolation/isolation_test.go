//go:build integration && linux

package isolation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"rayxo/agent/internal/client"
)

var socketPath = flag.String("socket", "/run/rayxo/agent.sock", "agent socket")

// The web server's address; the websites answer there by name, on port 80
// and, with a certificate, 443.
const webHost = "127.0.0.1"

// Where the certificates customers bring themselves go, one folder per
// domain (webserver.DefaultConfig).
const customCertificateDir = "/etc/rayxo/ssl/domains"

// account is a hosting account made for the test, with one website.
type account struct {
	Username string `json:"username"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Home     string `json:"home"`
	domain   string
}

func (a account) docroot() string {
	return filepath.Join(a.Home, "domains", a.domain, "public_html")
}

// server is the RAYXO server under test, with attacker a and victim b.
type server struct {
	secret     string
	php        string
	phpVersion string
	a, b       account
}

func TestAnAccountCannotReadAnotherAccountsFiles(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("runs as root on a RAYXO server")
	}

	s := setUp(t)

	if !t.Run("controls", s.controls) {
		t.Fatal("the checks below would prove nothing while the controls fail")
	}

	t.Run("as the Linux user (SFTP, cron, file manager)", s.asLinuxUser)
	t.Run("from the website's PHP", s.fromWebsitePHP)
	t.Run("through symlinks served by Nginx and Apache", s.throughSymlinks)
	t.Run("through .htaccess", s.throughHtaccess)
	t.Run("through the other account's PHP-FPM socket", s.throughPHPSocket)
	t.Run("through the panel's PHP-FPM pool", s.throughPanelPool)
	t.Run("through .htaccess PHP setting injection", s.throughIniInjection)
	t.Run("through Apache expressions in .htaccess", s.throughApacheExpressions)
	t.Run("a normal app .htaccess still works", s.legitHtaccess)
	t.Run("a migrated site's .htaccess still works", s.migratedHtaccess)
	t.Run("a site's own force-HTTPS rule works over HTTPS", s.forceHTTPS)
	t.Run("the pool owner cannot be forged from .htaccess", s.guardOwnerCannotBeForged)
	t.Run("hidden files are never served", s.hiddenFiles)
	t.Run("through the file manager", s.throughFileManager)
	t.Run("through a symlinked document root", s.throughDocumentRootSymlink)
	t.Run("a home opened to others would be caught", s.openedHomeIsCaught)
}

func setUp(t *testing.T) *server {
	version := newestPHP(t)
	suffix := randomHex(t, 3)

	s := &server{
		secret:     "rayxo-isolation-" + randomHex(t, 16),
		php:        "/usr/bin/php" + version,
		phpVersion: version,
	}
	s.a = createAccount(t, "isoa"+suffix, version)
	s.b = createAccount(t, "isob"+suffix, version)

	// The victim's files, as an upload by FTP or SFTP leaves them (0644).
	writeAs(t, s.b, filepath.Join(s.b.docroot(), "secret.txt"), s.secret)
	writeAs(t, s.b, filepath.Join(s.b.docroot(), "secret.html"), s.secret)
	writeAs(t, s.b, filepath.Join(s.b.docroot(), "wp-config.php"), "<?php // "+s.secret+"\n")
	writeAs(t, s.b, filepath.Join(s.b.Home, "notes.txt"), s.secret)

	// The attacker's own files, for the controls.
	writeAs(t, s.a, filepath.Join(s.a.docroot(), "mine.txt"), "mine")
	writeAs(t, s.a, filepath.Join(s.a.docroot(), "mine.html"), "mine")
	writeAs(t, s.a, filepath.Join(s.a.docroot(), "ok.php"), "<?php echo 'php-ok';\n")
	symlinkAs(t, s.a, filepath.Join(s.a.docroot(), "mine.txt"), filepath.Join(s.a.docroot(), "own-link.txt"))
	symlinkAs(t, s.a, filepath.Join(s.a.docroot(), "mine.html"), filepath.Join(s.a.docroot(), "own-link.html"))

	return s
}

// controls show that every channel the checks use works, and that the
// victim's files are readable where they should be, so a refusal below is
// the isolation at work and not a broken test.
func (s *server) controls(t *testing.T) {
	for _, path := range []string{"/secret.txt", "/secret.html"} {
		if _, body := get(t, s.b.domain, path); !strings.Contains(body, s.secret) {
			t.Errorf("%s's own website does not serve %s: %q", s.b.Username, path, body)
		}
	}

	if out := runAs(t, s.b, "cat", filepath.Join(s.b.Home, "notes.txt")); !strings.Contains(out, s.secret) {
		t.Errorf("%s cannot read its own notes.txt: %q", s.b.Username, out)
	}

	if _, body := get(t, s.a.domain, "/ok.php"); body != "php-ok" {
		t.Errorf("%s's PHP does not run: %q", s.a.Username, body)
	}

	for _, path := range []string{"/own-link.txt", "/own-link.html"} {
		if _, body := get(t, s.a.domain, path); body != "mine" {
			t.Errorf("a symlink to %s's own file is not served at %s: %q", s.a.Username, path, body)
		}
	}
}

// asLinuxUser covers everything that runs as the account's Linux user:
// SFTP sessions, cron jobs (PHP CLI there has no open_basedir) and the
// file manager's helper.
func (s *server) asLinuxUser(t *testing.T) {
	s.refute(t, "cat", runAs(t, s.a, "cat", filepath.Join(s.b.docroot(), "secret.txt")))
	s.refute(t, "cat", runAs(t, s.a, "cat", filepath.Join(s.b.Home, "notes.txt")))
	s.refuteListing(t, "ls", runAs(t, s.a, "ls", "-la", s.b.Home))
	s.refuteListing(t, "find", runAs(t, s.a, "find", s.b.Home))

	readScript := fmt.Sprintf(`echo @file_get_contents(%q), @file_get_contents(%q);`,
		filepath.Join(s.b.docroot(), "wp-config.php"), filepath.Join(s.b.Home, "notes.txt"))
	s.refute(t, "PHP CLI", runAs(t, s.a, s.php, "-r", readScript))
}

func (s *server) fromWebsitePHP(t *testing.T) {
	relative := filepath.Join("../../../..", s.b.Username, "notes.txt")
	script := fmt.Sprintf(`<?php
header('Content-Type: text/plain');
foreach ([%q, %q, %q, %q] as $number => $path) {
    $content = @file_get_contents($path);
    echo 'read ', $number, ': ', $content === false ? 'refused' : $content, "\n";
}
$list = @scandir(%q);
echo 'scandir: ', $list === false ? 'refused' : implode(' ', $list), "\n";
$link = sys_get_temp_dir() . '/stolen.txt';
@symlink(%q, $link);
echo 'symlink: ', @file_get_contents($link) ?: 'refused', "\n";
`, filepath.Join(s.b.docroot(), "secret.txt"), filepath.Join(s.b.docroot(), "wp-config.php"),
		filepath.Join(s.b.Home, "notes.txt"), relative, s.b.Home, filepath.Join(s.b.Home, "notes.txt"))
	writeAs(t, s.a, filepath.Join(s.a.docroot(), "read.php"), script)

	_, body := get(t, s.a.domain, "/read.php")
	if !strings.Contains(body, "scandir: ") {
		t.Fatalf("read.php did not run: %q", body)
	}

	s.refute(t, "read.php", body)
	s.refuteListing(t, "read.php", body)
}

// throughSymlinks covers the web server, which may enter every home: a
// link the attacker made to the victim's file or folder must not be served,
// by Nginx (static files) or by Apache (everything else).
func (s *server) throughSymlinks(t *testing.T) {
	symlinkAs(t, s.a, filepath.Join(s.b.docroot(), "secret.txt"), filepath.Join(s.a.docroot(), "link.txt"))
	symlinkAs(t, s.a, filepath.Join(s.b.docroot(), "secret.html"), filepath.Join(s.a.docroot(), "link.html"))
	symlinkAs(t, s.a, filepath.Join(s.b.docroot(), "wp-config.php"), filepath.Join(s.a.docroot(), "link.php"))
	symlinkAs(t, s.a, s.b.docroot(), filepath.Join(s.a.docroot(), "folder"))

	for _, path := range []string{
		"/link.txt", "/link.html", "/link.php",
		"/folder/secret.txt", "/folder/secret.html", "/folder/wp-config.php", "/folder/",
	} {
		status, body := get(t, s.a.domain, path)
		s.refute(t, fmt.Sprintf("GET %s (%d)", path, status), body)
		s.refuteListing(t, fmt.Sprintf("GET %s (%d)", path, status), body)
	}
}

// throughDocumentRootSymlink replaces the attacker's whole document root,
// which the attacker owns and may rename, with a symlink to the victim's
// document root and then to the victim's home: the web server must check
// the document root itself too, not only what is below it.
func (s *server) throughDocumentRootSymlink(t *testing.T) {
	docroot := s.a.docroot()
	moved := docroot + ".moved"

	if err := os.Rename(docroot, moved); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = os.Remove(docroot)
		_ = os.Rename(moved, docroot)
	})

	targets := map[string][]string{
		s.b.docroot(): {"/secret.txt", "/secret.html", "/wp-config.php"},
		s.b.Home:      {"/notes.txt"},
	}

	for target, paths := range targets {
		_ = os.Remove(docroot)
		symlinkAs(t, s.a, target, docroot)

		for _, path := range paths {
			status, body := get(t, s.a.domain, path)
			s.refute(t, fmt.Sprintf("GET %s with the document root linked to %s (%d)", path, target, status), body)
		}
	}
}

// throughHtaccess tries to switch the symlink protection off and to have
// Apache list the victim's folder, from the attacker's .htaccess.
func (s *server) throughHtaccess(t *testing.T) {
	follow := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "follow"))
	writeAs(t, s.a, filepath.Join(follow, ".htaccess"), "Options +FollowSymLinks +Indexes\n")
	symlinkAs(t, s.a, filepath.Join(s.b.docroot(), "secret.html"), filepath.Join(follow, "link.html"))

	indexes := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "indexes"))
	writeAs(t, s.a, filepath.Join(indexes, ".htaccess"), "Options +Indexes\n")
	symlinkAs(t, s.a, s.b.docroot(), filepath.Join(indexes, "folder"))

	for _, path := range []string{"/follow/link.html", "/indexes/folder/", "/indexes/folder/secret.html"} {
		status, body := get(t, s.a.domain, path)
		s.refute(t, fmt.Sprintf("GET %s (%d)", path, status), body)
		s.refuteListing(t, fmt.Sprintf("GET %s (%d)", path, status), body)
	}
}

// throughPHPSocket tries to get the attacker's code run by the victim's
// PHP-FPM pool, which runs as the victim: through Apache, by pointing the
// attacker's PHP files at that pool in .htaccess, and directly, by
// connecting to the pool's socket.
func (s *server) throughPHPSocket(t *testing.T) {
	handler := victimPHPHandler(t, s.b)

	steal := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "steal"))
	writeAs(t, s.a, filepath.Join(steal, ".htaccess"), fmt.Sprintf("<FilesMatch \"\\.php$\">\n    SetHandler %q\n</FilesMatch>\n", handler))
	writeAs(t, s.a, filepath.Join(steal, "steal.php"), fmt.Sprintf("<?php echo 'ran: ', @file_get_contents(%q);\n", filepath.Join(s.b.docroot(), "secret.txt")))

	status, body := get(t, s.a.domain, "/steal/steal.php")
	s.refute(t, fmt.Sprintf("GET /steal/steal.php (%d)", status), body)

	socket := strings.TrimPrefix(strings.SplitN(handler, "|", 2)[0], "proxy:unix:")
	connect := fmt.Sprintf(`echo @stream_socket_client(%q, $code, $message, 2) ? 'connected' : 'refused';`, "unix://"+socket)

	if out := runAs(t, s.a, s.php, "-r", connect); out != "refused" {
		t.Errorf("%s's Linux user can connect to %s's PHP-FPM socket %s: %q", s.a.Username, s.b.Username, socket, out)
	}

	writeAs(t, s.a, filepath.Join(s.a.docroot(), "connect.php"), "<?php "+connect+"\n")

	if _, body := get(t, s.a.domain, "/connect.php"); body != "refused" {
		t.Errorf("%s's website can connect to %s's PHP-FPM socket %s: %q", s.a.Username, s.b.Username, socket, body)
	}
}

// throughPanelPool tries to get the attacker's .htaccess to run code in the
// panel's own PHP-FPM pool (user rayxo), which could read every tenant's data
// and the panel's secrets. It must not: the override list forbids SetHandler,
// and the panel pool's open_basedir (and, once Nginx has its own user, the
// socket's owner) keep a routed request from running there. The probe prints
// its open_basedir, which would name a panel path only if it ran in that pool.
func (s *server) throughPanelPool(t *testing.T) {
	handler := "proxy:unix:/run/php/rayxo.sock|fcgi://localhost"

	jump := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "jump"))
	writeAs(t, s.a, filepath.Join(jump, "where.php"), "<?php echo 'BASEDIR:', ini_get('open_basedir');\n")

	for _, route := range []struct{ how, htaccess string }{
		{"SetHandler", fmt.Sprintf("<FilesMatch \"\\.php$\">\n    SetHandler %q\n</FilesMatch>\n", handler)},
		{"RewriteRule [H]", fmt.Sprintf("RewriteEngine On\nRewriteRule \\.php$ - [H=%s]\n", handler)},
	} {
		writeAs(t, s.a, filepath.Join(jump, ".htaccess"), route.htaccess)

		status, body := get(t, s.a.domain, "/jump/where.php")
		for _, panelPath := range []string{"/usr/local/rayxo", "/var/lib/rayxo"} {
			if strings.Contains(body, panelPath) {
				t.Errorf("%s: GET /jump/where.php (%d) ran in the panel pool (open_basedir has %q):\n%s", route.how, status, panelPath, body)
			}
		}
		t.Logf("panel pool via %s (%d): %q", route.how, status, excerpt(body))
	}
}

// throughIniInjection tries to widen the attacker's own pool open_basedir from
// .htaccess, which together with allow_url_include would let a prepended file
// run code outside the home. The pool fixes open_basedir as a non-overridable
// admin value, and Apache strips PHP_VALUE/PHP_ADMIN_VALUE from every proxied
// request, so no way of setting an environment variable from .htaccess
// (mod_rewrite [E=], SetEnv, SetEnvIf) may change it.
func (s *server) throughIniInjection(t *testing.T) {
	inject := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "inject"))
	writeAs(t, s.a, filepath.Join(inject, "basedir.php"), "<?php echo 'BASEDIR:', ini_get('open_basedir');\n")

	for _, route := range []struct{ how, htaccess string }{
		{"RewriteRule [E=]", "RewriteEngine On\nRewriteRule ^basedir\\.php$ - [E=PHP_ADMIN_VALUE:open_basedir=/]\n"},
		{"SetEnv", "SetEnv PHP_ADMIN_VALUE \"open_basedir=/\"\n"},
		{"SetEnvIf", "SetEnvIf Request_URI \"basedir\\.php$\" PHP_ADMIN_VALUE=open_basedir=/\n"},
	} {
		writeAs(t, s.a, filepath.Join(inject, ".htaccess"), route.htaccess)

		status, body := get(t, s.a.domain, "/inject/basedir.php")
		if !strings.HasPrefix(body, "BASEDIR:") {
			t.Errorf("%s: GET /inject/basedir.php (%d): the probe did not run, so the directive was refused: %q", route.how, status, excerpt(body))

			continue
		}

		if basedir := strings.TrimPrefix(body, "BASEDIR:"); !strings.Contains(basedir, s.a.Home) {
			t.Errorf("%s: GET /inject/basedir.php (%d): .htaccess widened open_basedir to %q (expected to contain the home %q)", route.how, status, basedir, s.a.Home)
		} else {
			t.Logf("open_basedir after %s injection: %q", route.how, basedir)
		}
	}
}

// throughApacheExpressions tries to have Apache itself read the victim's file
// for the attacker. Apache serves every account's website, so it can read
// them all, and its expressions have a file() function; ErrorDocument, Header
// and <If> all take expressions in .htaccess. A leak would show the secret in
// the response, and the <If> probe would answer whether the file matches.
func (s *server) throughApacheExpressions(t *testing.T) {
	secret := filepath.Join(s.b.docroot(), "secret.txt")
	probe := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "expr"))

	for _, route := range []struct{ how, htaccess string }{
		{"ErrorDocument", fmt.Sprintf("ErrorDocument 404 \"%%{file:%s}\"\n", secret)},
		{"Header expr=", fmt.Sprintf("Header always set X-Probe \"expr=%%{file:%s}\"\n", secret)},
		{"<If> file()", fmt.Sprintf("<If \"file('%s') =~ /rayxo-isolation-/\">\n    Header always set X-Probe matched\n</If>\n", secret)},
	} {
		writeAs(t, s.a, filepath.Join(probe, ".htaccess"), route.htaccess)

		status, header, body := request(t, s.a.domain, "/expr/missing")
		s.refute(t, fmt.Sprintf("%s: GET /expr/missing (%d)", route.how, status), fmt.Sprintf("%v\n%s", header, body))

		if header.Get("X-Probe") == "matched" {
			t.Errorf("%s: GET /expr/missing (%d): Apache told the attacker what %s holds", route.how, status, secret)
		}
		t.Logf("%s: %d, X-Probe %q, Apache: %s", route.how, status, header.Get("X-Probe"), lastApacheError(s.a.domain))
	}
}

// lastApacheError is the last line of the website's Apache error log, which
// says why Apache refused a request.
func lastApacheError(domain string) string {
	content, err := os.ReadFile(filepath.Join("/var/log/rayxo/domains", domain+".apache-error.log"))
	if err != nil {
		return err.Error()
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")

	return lines[len(lines)-1]
}

// legitHtaccess checks that a normal app .htaccess — the directives WordPress,
// Laravel and the like use — still works after the override list was narrowed,
// so the isolation fix did not break real sites. A rejected directive would
// make Apache return 500 for the directory.
func (s *server) legitHtaccess(t *testing.T) {
	app := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "app"))
	htaccess := strings.Join([]string{
		"RewriteEngine On",
		"RewriteBase /app/",
		"RewriteCond %{REQUEST_FILENAME} !-f",
		"RewriteRule ^ index.php [L]",
		"<IfModule mod_headers.c>",
		`    Header set X-Frame-Options "SAMEORIGIN"`,
		"</IfModule>",
		"<IfModule mod_expires.c>",
		"    ExpiresActive On",
		`    ExpiresByType text/css "access plus 1 week"`,
		"</IfModule>",
		"AddType application/json .json",
		"ErrorDocument 404 /app/index.php",
		"Options -Indexes",
	}, "\n") + "\n"
	writeAs(t, s.a, filepath.Join(app, ".htaccess"), htaccess)
	writeAs(t, s.a, filepath.Join(app, "index.php"), "<?php echo 'app-ok';\n")

	// A missing path rewrites to index.php (the WordPress/Laravel front
	// controller) and runs it; a rejected directive would 500 instead.
	status, body := get(t, s.a.domain, "/app/does-not-exist")
	if status != http.StatusOK || body != "app-ok" {
		t.Errorf("a normal app .htaccess broke: GET /app/does-not-exist => %d %q (want 200 \"app-ok\")", status, body)
	}
}

// migratedHtaccess checks the .htaccess lines sites bring along from
// DirectAdmin and cPanel, which allow FileInfo in full: environment variables
// (the Authorization header for APIs, no-gzip for images), mod_alias
// redirects and output filters. Apache answers 500 for the whole folder when
// one of them is not allowed.
func (s *server) migratedHtaccess(t *testing.T) {
	site := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "migrated"))
	htaccess := strings.Join([]string{
		`SetEnvIf Authorization "(.*)" HTTP_AUTHORIZATION=$1`,
		`SetEnvIfNoCase Request_URI "env\.php$" RAYXO_SETENVIF=yes`,
		`SetEnvIfNoCase Request_URI \.(?:gif|jpe?g|png)$ no-gzip dont-vary`,
		"BrowserMatch ^Mozilla/4 gzip-only-text/html",
		"BrowserMatchNoCase MSIE force-no-vary",
		"SetEnv RAYXO_SETENV yes",
		"UnsetEnv RAYXO_NOTHING",
		"<IfModule mod_deflate.c>",
		"    AddOutputFilter DEFLATE html",
		"    SetOutputFilter DEFLATE",
		"</IfModule>",
		"ExpiresActive On",
		`ExpiresByType text/html "access plus 1 hour"`,
		"Redirect 301 /migrated/old.html /migrated/new.html",
		"RedirectPermanent /migrated/permanent.html /migrated/new.html",
		"RedirectMatch 302 ^/migrated/legacy/(.*)$ /migrated/$1",
	}, "\n") + "\n"
	writeAs(t, s.a, filepath.Join(site, ".htaccess"), htaccess)
	writeAs(t, s.a, filepath.Join(site, "env.php"), "<?php echo 'setenv=', $_SERVER['RAYXO_SETENV'] ?? '', ' setenvif=', $_SERVER['RAYXO_SETENVIF'] ?? '';\n")

	status, header, body := request(t, s.a.domain, "/migrated/env.php")
	if status != http.StatusOK || body != "setenv=yes setenvif=yes" {
		t.Errorf("GET /migrated/env.php => %d %q (want 200 \"setenv=yes setenvif=yes\")", status, excerpt(body))
	}

	// ExpiresActive outside <IfModule> needs mod_expires on.
	if cache := header.Get("Cache-Control"); !strings.Contains(cache, "max-age=3600") {
		t.Errorf("GET /migrated/env.php: Cache-Control %q, want max-age=3600 from ExpiresByType", cache)
	}

	for _, redirect := range []struct {
		path   string
		status int
		to     string
	}{
		{"/migrated/old.html", http.StatusMovedPermanently, "/migrated/new.html"},
		{"/migrated/permanent.html", http.StatusMovedPermanently, "/migrated/new.html"},
		{"/migrated/legacy/page.html", http.StatusFound, "/migrated/page.html"},
	} {
		status, header, body := request(t, s.a.domain, redirect.path)
		if status != redirect.status || !strings.HasSuffix(header.Get("Location"), redirect.to) {
			t.Errorf("GET %s => %d to %q (want %d to %s): %q", redirect.path, status, header.Get("Location"), redirect.status, redirect.to, excerpt(body))
		}
	}
}

// forceHTTPS checks a website's own force-HTTPS rule, about the most common
// .htaccess there is, and in most sites moved from DirectAdmin or cPanel.
// Over HTTPS it must serve the page, not redirect to itself forever; over
// HTTP it must still redirect. Nginx sends HTTPS requests to Apache over
// TLS, so Apache sees HTTPS, the port and the scheme as the visitor does.
func (s *server) forceHTTPS(t *testing.T) {
	s.serveHTTPS(t, s.a)

	site := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "secure"))
	writeAs(t, s.a, filepath.Join(site, "env.php"),
		"<?php echo 'https=', $_SERVER['HTTPS'] ?? '', ' port=', $_SERVER['SERVER_PORT'], ' scheme=', $_SERVER['REQUEST_SCHEME'];\n")
	folder := mkdirAs(t, s.a, filepath.Join(site, "folder"))
	writeAs(t, s.a, filepath.Join(folder, "index.html"), "folder")

	secure := "https://" + s.a.domain + "/secure/"

	// PHP runs there too: Apache's TLS vhost also tells the pool's guard
	// whose request it is.
	if status, _, body := fetch(t, secure+"env.php"); status != http.StatusOK || body != "https=on port=443 scheme=https" {
		t.Errorf("GET %senv.php => %d %q (want 200 \"https=on port=443 scheme=https\")", secure, status, excerpt(body))
	}

	// Apache's own redirect, of a folder without its slash, stays on HTTPS.
	if status, header, _ := fetch(t, secure+"folder"); status != http.StatusMovedPermanently || header.Get("Location") != secure+"folder/" {
		t.Errorf("GET %sfolder => %d to %q (want 301 to %sfolder/)", secure, status, header.Get("Location"), secure)
	}

	for _, condition := range []string{
		"RewriteCond %{HTTPS} !=on",
		"RewriteCond %{HTTPS} off",
		"RewriteCond %{SERVER_PORT} 80",
		"RewriteCond %{SERVER_PORT} !^443$",
		"RewriteCond %{REQUEST_SCHEME} =http",
	} {
		writeAs(t, s.a, filepath.Join(site, ".htaccess"),
			"RewriteEngine On\n"+condition+"\nRewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [L,R=301]\n")

		if status, header, body := fetch(t, secure+"env.php"); status != http.StatusOK {
			t.Errorf("%s: GET %senv.php => %d to %q (want 200, not a redirect to itself): %q", condition, secure, status, header.Get("Location"), excerpt(body))
		}

		plain := "http://" + s.a.domain + "/secure/env.php"
		if status, header, _ := fetch(t, plain); status != http.StatusMovedPermanently || header.Get("Location") != secure+"env.php" {
			t.Errorf("%s: GET %s => %d to %q (want 301 to %senv.php)", condition, plain, status, header.Get("Location"), secure)
		}
	}
}

// serveHTTPS turns HTTPS on for the website of owner, with a self-signed
// certificate installed as one a customer brings would be. It goes back to
// plain HTTP when the test ends.
func (s *server) serveHTTPS(t *testing.T, owner account) {
	t.Helper()

	dir := filepath.Join(customCertificateDir, owner.domain)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	certificate, key := selfSignedCertificate(t, owner.domain, "www."+owner.domain)
	for name, content := range map[string][]byte{"fullchain.pem": certificate, "privkey.pem": key} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	website := map[string]any{"username": owner.Username, "domain": owner.domain, "php_version": s.phpVersion}

	t.Cleanup(func() {
		// Plain HTTP first, so Nginx never refers to a missing certificate.
		if _, err := callAgent("web.apply_domain", website); err != nil {
			t.Errorf("turn HTTPS off for %s: %v", owner.domain, err)
		}

		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove the test certificate: %v", err)
		}
	})

	withHTTPS := map[string]any{"ssl": true, "certificate": "custom"}
	for key, value := range website {
		withHTTPS[key] = value
	}

	if _, err := callAgent("web.apply_domain", withHTTPS); err != nil {
		t.Fatalf("turn HTTPS on for %s: %v", owner.domain, err)
	}

	waitFor(t, "https://"+owner.domain+"/")
}

// selfSignedCertificate returns a certificate for names and its key, PEM
// encoded.
func selfSignedCertificate(t *testing.T, names ...string) (certificate, key []byte) {
	t.Helper()

	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}

	keyDER, err := x509.MarshalECPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// guardOwnerCannotBeForged proves the per-pool guard cannot be fooled: a
// request routed to another account's pool carries the requesting account's
// RAYXO_POOL_OWNER, which that account's own Apache vhost forces after
// .htaccess has run. If .htaccess could set it, cross-tenant routing would
// pass the guard. The test sets it to the victim from the attacker's own
// .htaccess; the attacker's vhost must override it back, so the attacker's own
// request still succeeds (were the forgery to win, the attacker's own pool
// guard would reject it).
func (s *server) guardOwnerCannotBeForged(t *testing.T) {
	forge := mkdirAs(t, s.a, filepath.Join(s.a.docroot(), "forge"))
	writeAs(t, s.a, filepath.Join(forge, "who.php"), "<?php echo 'OK';\n")

	for _, route := range []struct{ how, htaccess string }{
		{"RewriteRule [E=]", "RewriteEngine On\nRewriteRule ^who\\.php$ - [E=RAYXO_POOL_OWNER:" + s.b.Username + "]\n"},
		{"SetEnv", "SetEnv RAYXO_POOL_OWNER " + s.b.Username + "\n"},
	} {
		writeAs(t, s.a, filepath.Join(forge, ".htaccess"), route.htaccess)

		status, body := get(t, s.a.domain, "/forge/who.php")
		if status != http.StatusOK || body != "OK" {
			t.Errorf("%s: GET /forge/who.php => %d %q: the vhost must force RAYXO_POOL_OWNER so .htaccess cannot forge it (want 200 \"OK\")", route.how, status, excerpt(body))
		}
	}
}

// hiddenFiles checks that a website never serves a hidden file or folder,
// whatever its .htaccess says: an app's .env with its database password, a
// .git checkout, a dump or archive kept in a hidden folder (archives are
// static files Nginx would otherwise send straight from disk). /.well-known/
// stays public, for app links, security.txt and the like.
func (s *server) hiddenFiles(t *testing.T) {
	docroot := s.a.docroot()
	writeAs(t, s.a, filepath.Join(docroot, ".env"), "APP_KEY="+s.secret+"\n")
	git := mkdirAs(t, s.a, filepath.Join(docroot, ".git"))
	writeAs(t, s.a, filepath.Join(git, "config"), "[core] "+s.secret+"\n")
	private := mkdirAs(t, s.a, filepath.Join(docroot, ".private"))
	writeAs(t, s.a, filepath.Join(private, "backup.zip"), s.secret)
	writeAs(t, s.a, filepath.Join(private, "dump.sql"), s.secret)

	for _, path := range []string{"/.env", "/.git/config", "/.private/backup.zip", "/.private/dump.sql"} {
		if status, body := get(t, s.a.domain, path); strings.Contains(body, s.secret) {
			t.Errorf("GET %s (%d) served a hidden file: %q", path, status, excerpt(body))
		}
	}

	wellKnown := filepath.Join(docroot, ".well-known")
	if _, err := os.Stat(wellKnown); os.IsNotExist(err) {
		mkdirAs(t, s.a, wellKnown)
	}
	writeAs(t, s.a, filepath.Join(wellKnown, "rayxo-check.txt"), "well-known-ok")

	if status, body := get(t, s.a.domain, "/.well-known/rayxo-check.txt"); status != http.StatusOK || body != "well-known-ok" {
		t.Errorf("GET /.well-known/rayxo-check.txt => %d %q (want 200 \"well-known-ok\")", status, excerpt(body))
	}
}

// throughFileManager asks the agent's file manager, as the attacker, for
// the victim's files: by the links above and by a path that climbs out.
func (s *server) throughFileManager(t *testing.T) {
	inDocroot := filepath.Join("domains", s.a.domain, "public_html")

	for _, call := range []struct{ method, path string }{
		{"fs.read", filepath.Join(inDocroot, "link.txt")},
		{"fs.read", filepath.Join(inDocroot, "folder", "secret.txt")},
		{"fs.list", filepath.Join(inDocroot, "folder")},
		{"fs.read", filepath.Join("..", s.b.Username, "notes.txt")},
	} {
		result, err := callAgent(call.method, map[string]any{"username": s.a.Username, "path": call.path})
		how := fmt.Sprintf("%s %s (error: %v)", call.method, call.path, err)
		s.refute(t, how, string(result))
		s.refuteListing(t, how, string(result))
	}
}

// openedHomeIsCaught opens the victim's home to others for a moment and
// expects the attacker to read it, which shows that the home's mode is
// what stops the reads above and that the check would see a leak.
func (s *server) openedHomeIsCaught(t *testing.T) {
	info, err := os.Stat(s.b.Home)
	if err != nil {
		t.Fatal(err)
	}

	mode := info.Mode().Perm()
	if err := os.Chmod(s.b.Home, mode|0o001); err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := os.Chmod(s.b.Home, mode); err != nil {
			t.Errorf("restore the mode of %s to %o: %v", s.b.Home, mode, err)
		}
	}()

	if out := runAs(t, s.a, "cat", filepath.Join(s.b.Home, "notes.txt")); !strings.Contains(out, s.secret) {
		t.Errorf("with %s open to others, %s still cannot read notes.txt (%q): the checks would not notice a leak", s.b.Home, s.a.Username, out)
	}
}

// refute fails when output holds the victim's secret.
func (s *server) refute(t *testing.T, how, output string) {
	t.Helper()

	if strings.Contains(output, s.secret) {
		t.Errorf("%s: %s read %s's file:\n%s", how, s.a.Username, s.b.Username, output)

		return
	}

	t.Logf("%s: no secret in %q", how, excerpt(output))
}

// excerpt is the first line of output, shortened, for the log.
func excerpt(output string) string {
	line, _, _ := strings.Cut(output, "\n")
	if len(line) > 90 {
		return line[:90] + "…"
	}

	return line
}

// refuteListing fails when output lists the victim's files.
func (s *server) refuteListing(t *testing.T, how, output string) {
	t.Helper()

	if strings.Contains(output, "notes.txt") || strings.Contains(output, "wp-config.php") {
		t.Errorf("%s: %s listed %s's files:\n%s", how, s.a.Username, s.b.Username, output)
	}
}

func createAccount(t *testing.T, username, phpVersion string) account {
	t.Helper()

	result, err := callAgent("account.create", map[string]any{"username": username})
	if err != nil {
		t.Fatalf("account.create %s: %v", username, err)
	}

	t.Cleanup(func() {
		if _, err := callAgent("account.delete", map[string]any{"username": username}); err != nil {
			t.Errorf("delete test account %s: %v; remove it with: rayxo call account.delete '{\"username\":%q}'", username, err, username)
		}
	})

	var created account
	if err := json.Unmarshal(result, &created); err != nil {
		t.Fatalf("account.create %s: %v", username, err)
	}

	created.domain = username + ".test"

	if _, err := callAgent("web.apply_domain", map[string]any{
		"username":    username,
		"domain":      created.domain,
		"php_version": phpVersion,
	}); err != nil {
		t.Fatalf("web.apply_domain %s: %v", created.domain, err)
	}

	waitForWebsite(t, created.domain)

	return created
}

// waitForWebsite waits until the web servers answer for domain with its
// default page: they reload in the background after the agent returns.
func waitForWebsite(t *testing.T, domain string) {
	t.Helper()

	waitFor(t, "http://"+domain+"/")
}

// waitFor waits until the web servers answer url with 200. Until Nginx has
// reloaded, a new HTTPS website may not even complete the TLS handshake.
// After the first 200 it waits a little longer: Nginx's master gives its new
// workers 100 ms to start before it stops the old ones, which meanwhile still
// take connections with the old configuration, without the website.
func waitFor(t *testing.T, url string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for {
		status, _, _, err := tryFetch(url)
		if err == nil && status == http.StatusOK {
			time.Sleep(300 * time.Millisecond)

			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s still answers %d (%v) after 30 seconds", url, status, err)
		}

		time.Sleep(250 * time.Millisecond)
	}
}

// newestPHP is the newest PHP version with both PHP-FPM and the CLI.
func newestPHP(t *testing.T) string {
	t.Helper()

	result, err := callAgent("php.versions", map[string]any{})
	if err != nil {
		t.Fatalf("php.versions: %v", err)
	}

	var installed struct {
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(result, &installed); err != nil {
		t.Fatalf("php.versions: %v", err)
	}

	for i := len(installed.Versions) - 1; i >= 0; i-- {
		if _, err := os.Stat("/usr/bin/php" + installed.Versions[i]); err == nil {
			return installed.Versions[i]
		}
	}

	t.Fatalf("no PHP version has its CLI installed: %v", installed.Versions)

	return ""
}

var handlerPattern = regexp.MustCompile(`SetHandler "(proxy:unix:[^"|]+\|fcgi://[^"]+)"`)

// victimPHPHandler is the SetHandler value of the victim's website, which
// names its PHP-FPM socket.
func victimPHPHandler(t *testing.T, victim account) string {
	t.Helper()

	vhost, err := os.ReadFile(filepath.Join("/etc/apache2/rayxo", victim.Username, victim.domain+".conf"))
	if err != nil {
		t.Fatal(err)
	}

	match := handlerPattern.FindSubmatch(vhost)
	if match == nil {
		t.Fatalf("no PHP handler in the Apache vhost of %s", victim.domain)
	}

	return string(match[1])
}

func callAgent(method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	return client.Call(ctx, *socketPath, method, params)
}

// runAs runs a command as the account's Linux user and returns what it
// printed, errors included: only what it could read matters.
func runAs(t *testing.T, as account, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command("runuser", append([]string{"-u", as.Username, "--", name}, args...)...)
	cmd.Dir = "/"

	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %s as %s: %v", name, as.Username, err)
		}
	}

	return strings.TrimSpace(string(out))
}

// get asks the web server for path on domain, without following redirects.
func get(t *testing.T, domain, path string) (int, string) {
	t.Helper()

	status, _, body := request(t, domain, path)

	return status, body
}

// request is get with the response headers too.
func request(t *testing.T, domain, path string) (int, http.Header, string) {
	t.Helper()

	return fetch(t, "http://"+domain+path)
}

// fetch asks the web server for url, http:// or https://, without
// following redirects.
func fetch(t *testing.T, url string) (int, http.Header, string) {
	t.Helper()

	status, header, body, err := tryFetch(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}

	return status, header, body
}

// tryFetch is fetch that returns its error.
func tryFetch(url string) (int, http.Header, string, error) {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}

				var dialer net.Dialer

				return dialer.DialContext(ctx, "tcp", net.JoinHostPort(webHost, port))
			},
			// The test's own self-signed certificates (serveHTTPS).
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // only the test's own certificates
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	response, err := httpClient.Get(url)
	if err != nil {
		return 0, nil, "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, "", err
	}

	return response.StatusCode, response.Header, strings.TrimSpace(string(body)), nil
}

// writeAs writes a file the way the account's own upload would.
func writeAs(t *testing.T, owner account, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chown(path, owner.UID, owner.GID); err != nil {
		t.Fatal(err)
	}
}

func mkdirAs(t *testing.T, owner account, path string) string {
	t.Helper()

	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Chown(path, owner.UID, owner.GID); err != nil {
		t.Fatal(err)
	}

	return path
}

// symlinkAs makes a symlink owned by the account, as if it made it itself.
func symlinkAs(t *testing.T, owner account, target, link string) {
	t.Helper()

	if err := os.Symlink(target, link); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}

	if err := os.Lchown(link, owner.UID, owner.GID); err != nil {
		t.Fatal(err)
	}
}

func randomHex(t *testing.T, bytes int) string {
	t.Helper()

	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal(err)
	}

	return hex.EncodeToString(buffer)
}
