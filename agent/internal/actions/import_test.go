package actions

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"rayxo/agent/internal/files"
	"rayxo/agent/internal/rpc"
)

// archiveEntry is one entry of a backup of another panel a test writes.
type archiveEntry struct {
	name    string
	content string
	link    string
	dir     bool
}

// tarOf is the tar stream of entries.
func tarOf(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()

	var stream bytes.Buffer

	writer := tar.NewWriter(&stream)

	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(entry.content))}

		switch {
		case entry.dir:
			header = &tar.Header{Name: entry.name + "/", Mode: 0o750, Typeflag: tar.TypeDir}
		case entry.link != "":
			header = &tar.Header{Name: entry.name, Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: entry.link}
		}

		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}

		if header.Typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	return stream.Bytes()
}

// compressed is data compressed with gzip, zstd or nothing.
func compressed(t *testing.T, data []byte, with string) []byte {
	t.Helper()

	switch with {
	case "gzip":
		var out bytes.Buffer

		writer := gzip.NewWriter(&out)
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}

		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		return out.Bytes()
	case "zstd":
		command := exec.Command(files.ZstdProgram, "-q", "-c")
		command.Stdin = bytes.NewReader(data)

		out, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}

		return out
	}

	return data
}

// useZstd points the agent at the zstd program of this machine, or skips
// the test without one.
func useZstd(t *testing.T) {
	t.Helper()

	program, err := exec.LookPath("zstd")
	if err != nil {
		t.Skip("zstd is not installed")
	}

	previous := files.ZstdProgram
	files.ZstdProgram = program

	t.Cleanup(func() { files.ZstdProgram = previous })
}

func writeBackupFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatal(err)
	}
}

const foreignTrigger = "/*!50003 CREATE*/ /*!50017 DEFINER=`budi`@`localhost`*/ /*!50003 TRIGGER stamp BEFORE INSERT ON items FOR EACH ROW SET NEW.id = NEW.id */;;\n"

const foreignDump = "CREATE TABLE items (id int);\nINSERT INTO items VALUES (1),('DEFINER=`x`@`y`');\n" + foreignTrigger

// cpanelBackupEntries are a cPanel backup of account budi, in the order
// pkgacct writes one: what cPanel knows of the account, the dumps, then
// the home.
func cpanelBackupEntries() []archiveEntry {
	return []archiveEntry{
		{name: "cpmove-budi", dir: true},
		{name: "cpmove-budi/cp/budi", content: "USER=budi\nDNS=budi.test\nCONTACTEMAIL=budi@example.net\n"},
		{name: "cpmove-budi/userdata/main", content: "---\nmain_domain: budi.test\n"},
		{name: "cpmove-budi/va/budi.test", content: "sales@budi.test: boss@example.net\n"},
		{name: "cpmove-budi/mysql.sql", content: "GRANT USAGE ON *.* TO 'budi_wp'@'localhost' IDENTIFIED BY PASSWORD '*24ECEAB08897F9D6E6E7B1FDAE8A81780F44CBCA';\n"},
		{name: "cpmove-budi/dnszones/budi.test.db", content: "; zone"},
		{name: "cpmove-budi/mysql/budi_shop.sql", content: foreignDump},
		{name: "cpmove-budi/mysql/budi_shop.create", content: "CREATE DATABASE budi_shop;"},
		{name: "cpmove-budi/homedir", dir: true},
		{name: "cpmove-budi/homedir/public_html", dir: true},
		{name: "cpmove-budi/homedir/public_html/index.php", content: "<?php echo 'main';"},
		{name: "cpmove-budi/homedir/public_html/addon.test/index.html", content: "addon"},
		{name: "cpmove-budi/homedir/etc/budi.test/shadow", content: "info:$6$salt$hash:20771::::::\n"},
		{name: "cpmove-budi/homedir/mail/budi.test/info/cur/1", content: "Subject: hi\n"},
		{name: "cpmove-budi/homedir/mail/budi.test/info/.Sent/cur/2", content: "Subject: sent\n"},
		{name: "cpmove-budi/homedir/mail/budi.test/info/.Sent/dovecot.index.cache", content: "cache"},
		{name: "cpmove-budi/homedir/mail/budi.test/info/dovecot-uidlist", content: "3 V1 N3\n"},
		{name: "cpmove-budi/homedir/mail/budi.test/info/maildirsize", content: "524288000S\n"},
		{name: "cpmove-budi/homedir/mail/new/3", content: "Subject: to the cPanel account\n"},
		{name: "cpmove-budi/homedir/.cpanel/datastore", content: "cPanel's own"},
		{name: "cpmove-budi/homedir/perl5/Lib.pm", content: "1;"},
		{name: "cpmove-budi/homedir/www", link: "public_html"},
	}
}

func cpanelRules() []files.HomeRule {
	return []files.HomeRule{
		{From: "public_html", To: "domains/budi.test/public_html"},
		{From: "public_html/addon.test", To: "domains/addon.test/public_html"},
		{From: "mail/budi.test/info", To: "imap/budi.test/info/Maildir", Mail: true},
		{From: "mail", To: ""},
		{From: "perl5", To: "perl5"},
	}
}

// directAdminBackupEntries are a DirectAdmin backup of account budi, as
// Admin Backup writes one: what DirectAdmin knows of the account and the
// dumps in backup/ (with the rest of the home in its own archive), then
// the websites and the mail.
func directAdminBackupEntries(t *testing.T, with string) []archiveEntry {
	t.Helper()

	home := tarOf(t, []archiveEntry{
		{name: ".bashrc", content: "# shell"},
		{name: "private", dir: true},
		{name: "private/notes.txt", content: "notes"},
		{name: "Maildir/cur/9", content: "Subject: to the DirectAdmin account\n"},
	})

	return []archiveEntry{
		{name: "backup", dir: true},
		{name: "backup/user.conf", content: "username=budi\ndomain=budi.test\nemail=budi@example.net\n"},
		{name: "backup/crontab.conf", content: "000=*/15 * * * * echo da\n"},
		{name: "backup/budi_shop.conf", content: "budi_wp=accesshosts=localhost&passwd=*24ECEAB08897F9D6E6E7B1FDAE8A81780F44CBCA\n"},
		{name: "backup/budi_shop.sql", content: foreignDump},
		{name: "backup/budi.test/domain.conf", content: "domain=budi.test\n"},
		{name: "backup/budi.test/subdomain.list", content: "blog\n"},
		{name: "backup/budi.test/email/passwd", content: "info:$6$salt$hash\n"},
		{name: "backup/budi.test/email/data/limit/info", content: "200"},
		{name: "backup/.shadow", content: "$6$secret"},
		{name: "backup/login_keys/rootauth/key.conf", content: "key=secret"},
		{name: "backup/home.tar" + map[string]string{"gzip": ".gz", "zstd": ".zst"}[with], content: string(compressed(t, home, with))},
		{name: "domains", dir: true},
		{name: "domains/budi.test/public_html", dir: true},
		{name: "domains/budi.test/public_html/index.php", content: "<?php echo 'main';"},
		{name: "domains/budi.test/public_html/blog/index.html", content: "blog"},
		{name: "domains/budi.test/stats/index.html", content: "old statistics"},
		{name: "imap/budi.test/info/Maildir/cur/1", content: "Subject: hi\n"},
		{name: "imap/budi.test/info/Maildir/dovecot.index.cache", content: "cache"},
	}
}

func directAdminRules() []files.HomeRule {
	return []files.HomeRule{
		{From: "domains/budi.test/public_html", To: "domains/budi.test/public_html"},
		{From: "domains/budi.test/public_html/blog", To: "domains/blog.budi.test/public_html"},
		{From: "imap/budi.test/info", To: "imap/budi.test/info", Mail: true},
		{From: ".bashrc", To: ".bashrc"},
		{From: "private", To: "private"},
	}
}

func (env *serverBackupEnv) convertImport(t *testing.T, params map[string]any) (map[string]any, error) {
	t.Helper()

	base := map[string]any{
		"kind":      files.KindCpanel,
		"source":    "cpmove-budi.tar.gz",
		"name":      "budi-imported.tar.gz",
		"top":       "cpmove-budi",
		"manifest":  map[string]any{"format": 1, "username": "budi"},
		"settings":  map[string]any{"mailboxes": []any{}},
		"databases": []map[string]string{{"from": "budi_shop", "to": "budi_shop"}},
		"rules":     cpanelRules(),
	}
	for key, value := range params {
		base[key] = value
	}

	raw, err := callBackup(env.methods, "backup.import_convert", base)
	if err != nil {
		return nil, err
	}

	var result map[string]any

	return result, json.Unmarshal(raw, &result)
}

func (env *serverBackupEnv) inspectImport(t *testing.T, name string) files.ForeignInspection {
	t.Helper()

	raw, err := callBackup(env.methods, "backup.import_inspect", map[string]any{"name": name})
	if err != nil {
		t.Fatal(err)
	}

	var inspection files.ForeignInspection
	if err := json.Unmarshal(raw, &inspection); err != nil {
		t.Fatal(err)
	}

	return inspection
}

// refusedNames is the list of entry names a convert result reports as left
// out because their names could lead out of the home.
func refusedNames(t *testing.T, result map[string]any) []string {
	t.Helper()

	names := []string{}

	for _, name := range result["refused_names"].([]any) {
		names = append(names, name.(string))
	}

	return names
}

func sortedEntries(entries map[string]string) []string {
	return slices.Sorted(func(yield func(string) bool) {
		for name := range entries {
			if !yield(name) {
				return
			}
		}
	})
}

func homeNames(inspection files.ForeignInspection) []string {
	names := []string{}
	for _, entry := range inspection.Home {
		names = append(names, entry.Name+":"+entry.Type)
	}

	return names
}

func TestImportMethodsAreRegisteredForHours(t *testing.T) {
	methods := serverBackupMethods(newQuotaSystem(), &homeHelper{}, &fakeDatabaseServer{}, files.Transfers{}, BackupsConfig{})

	for _, name := range []string{"backup.import_inspect", "backup.import_convert"} {
		if methods[name].Handler == nil || methods[name].Timeout != backupTimeout || methods[name].LockKey != nil {
			t.Errorf("%s must be registered, run for hours and lock no account", name)
		}
	}
}

func TestACpanelBackupIsReadThrough(t *testing.T) {
	env := newServerBackupEnv(t)
	writeBackupFile(t, filepath.Join(env.dir, "cpmove-budi.tar.gz"), compressed(t, tarOf(t, cpanelBackupEntries()), "gzip"))

	inspection := env.inspectImport(t, "cpmove-budi.tar.gz")

	if inspection.Kind != files.KindCpanel || inspection.Top != "cpmove-budi" || inspection.Files["cp/budi"] == "" || inspection.Files["userdata/main"] == "" ||
		inspection.Files["va/budi.test"] == "" || inspection.Files["mysql.sql"] == "" || inspection.Files["homedir/etc/budi.test/shadow"] == "" {
		t.Errorf("inspection = %+v", inspection)
	}

	if _, read := inspection.Files["dnszones/budi.test.db"]; read {
		t.Error("only the files the panel needs are read")
	}

	if len(inspection.Databases) != 1 || inspection.Databases[0].Name != "budi_shop" || inspection.Databases[0].Size == 0 {
		t.Errorf("databases = %+v", inspection.Databases)
	}

	if names := homeNames(inspection); !slices.Equal(names, []string{".cpanel:directory", "etc:directory", "mail:directory", "perl5:directory", "public_html:directory", "www:link"}) {
		t.Errorf("home = %v", names)
	}

	if inspection.Entries != 13 || inspection.Bytes == 0 {
		t.Errorf("entries = %d, bytes = %d", inspection.Entries, inspection.Bytes)
	}
}

func TestADirectAdminBackupIsReadThroughWhateverItIsCompressedWith(t *testing.T) {
	for _, with := range []string{"zstd", "gzip", "none"} {
		t.Run(with, func(t *testing.T) {
			if with == "zstd" {
				useZstd(t)
			}

			env := newServerBackupEnv(t)
			name := "user.admin.budi.tar" + map[string]string{"gzip": ".gz", "zstd": ".zst"}[with]
			writeBackupFile(t, filepath.Join(env.dir, name), compressed(t, tarOf(t, directAdminBackupEntries(t, with)), with))

			inspection := env.inspectImport(t, name)

			read := sortedEntries(inspection.Files)
			want := []string{"backup/budi.test/domain.conf", "backup/budi.test/email/passwd", "backup/budi.test/subdomain.list", "backup/budi_shop.conf", "backup/crontab.conf", "backup/user.conf"}

			if inspection.Kind != files.KindDirectAdmin || inspection.Top != "" || !slices.Equal(read, want) {
				t.Errorf("kind = %q, top = %q, read = %v", inspection.Kind, inspection.Top, read)
			}

			if len(inspection.Databases) != 1 || inspection.Databases[0].Name != "budi_shop" {
				t.Errorf("databases = %+v", inspection.Databases)
			}

			if names := homeNames(inspection); !slices.Equal(names, []string{".bashrc:file", "Maildir:directory", "domains:directory", "imap:directory", "private:directory"}) {
				t.Errorf("home = %v", names)
			}
		})
	}
}

func TestOnlyABackupOfCpanelOrDirectAdminIsRead(t *testing.T) {
	env := newServerBackupEnv(t)

	created, err := env.create(t, map[string]any{"files": true})
	if err != nil {
		t.Fatal(err)
	}

	writeBackupFile(t, filepath.Join(env.dir, "no-account.tar.gz"), compressed(t, tarOf(t, []archiveEntry{{name: "top/homedir/a", content: "a"}}), "gzip"))
	writeBackupFile(t, filepath.Join(env.dir, "no-user.tar"), tarOf(t, []archiveEntry{{name: "backup/budi.test/domain.conf", content: "domain=budi.test"}}))

	for _, name := range []string{created.Name, "no-account.tar.gz", "no-user.tar"} {
		_, err := callBackup(env.methods, "backup.import_inspect", map[string]any{"name": name})

		var refused *rpc.Error
		if !errors.As(err, &refused) || !strings.Contains(refused.Message, "not a cPanel or DirectAdmin backup") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestADamagedZstdBackupIsRefused(t *testing.T) {
	useZstd(t)

	env := newServerBackupEnv(t)
	whole := compressed(t, tarOf(t, directAdminBackupEntries(t, "zstd")), "zstd")
	writeBackupFile(t, filepath.Join(env.dir, "user.admin.budi.tar.zst"), whole[:len(whole)/2])

	_, err := callBackup(env.methods, "backup.import_inspect", map[string]any{"name": "user.admin.budi.tar.zst"})

	var refused *rpc.Error
	if !errors.As(err, &refused) || !strings.Contains(refused.Message, "damaged") {
		t.Errorf("err = %v", err)
	}
}

func TestACpanelBackupBecomesARayxoBackupThatRestores(t *testing.T) {
	env := newServerBackupEnv(t)
	source := filepath.Join(env.dir, "cpmove-budi.tar.gz")
	writeBackupFile(t, source, compressed(t, tarOf(t, cpanelBackupEntries()), "gzip"))

	result, err := env.convertImport(t, nil)
	if err != nil {
		t.Fatal(err)
	}

	if result["name"] != "budi-imported.tar.gz" || result["size"].(float64) == 0 || !strings.Contains(result["manifest"].(string), `"username":"budi"`) {
		t.Errorf("result = %v", result)
	}

	entries := archiveEntries(t, filepath.Join(env.dir, "budi-imported.tar.gz"))
	want := []string{
		".rayxo-backup/", ".rayxo-backup/databases/", ".rayxo-backup/databases/budi_shop.sql", ".rayxo-backup/manifest.json", ".rayxo-backup/settings.json",
		"domains/addon.test/public_html/index.html", "domains/budi.test/public_html/index.php",
		"imap/budi.test/info/Maildir/.Sent/cur/2", "imap/budi.test/info/Maildir/cur/1", "imap/budi.test/info/Maildir/dovecot-uidlist",
		"perl5/Lib.pm",
	}

	if names := sortedEntries(entries); !slices.Equal(names, want) {
		t.Errorf("entries = %v", names)
	}

	dump := entries[".rayxo-backup/databases/budi_shop.sql"]
	if !strings.Contains(dump, "/*!50017 DEFINER=CURRENT_USER*/") || !strings.Contains(dump, "('DEFINER=`x`@`y`')") {
		t.Errorf("dump = %q", dump)
	}

	if _, err := os.Stat(source); err != nil {
		t.Error("the cPanel backup must stay")
	}

	if leftovers, _ := filepath.Glob(filepath.Join(env.dir, ".*")); len(leftovers) != 0 {
		t.Errorf("left behind: %v", leftovers)
	}

	if _, err := callBackup(env.methods, "backup.server_inspect", map[string]any{"name": "budi-imported.tar.gz"}); err != nil {
		t.Fatalf("the result must read as a RAYXO backup: %v", err)
	}

	if _, err := callBackup(env.methods, "backup.server_restore", map[string]any{"username": "budi", "name": "budi-imported.tar.gz", "files": true, "email": true, "databases": []string{"budi_shop"}}); err != nil {
		t.Fatal(err)
	}

	if readHome(t, filepath.Join(env.home, "domains/addon.test/public_html/index.html")) != "addon" ||
		readHome(t, filepath.Join(env.home, "imap/budi.test/info/Maildir/cur/1")) != "Subject: hi\n" ||
		!strings.Contains(env.databases.imported["budi_shop"], "DEFINER=CURRENT_USER") {
		t.Error("the converted backup did not restore")
	}
}

func TestADirectAdminBackupBecomesARayxoBackupThatRestores(t *testing.T) {
	useZstd(t)

	env := newServerBackupEnv(t)
	writeBackupFile(t, filepath.Join(env.dir, "user.admin.budi.tar.zst"), compressed(t, tarOf(t, directAdminBackupEntries(t, "zstd")), "zstd"))

	_, err := env.convertImport(t, map[string]any{"kind": files.KindDirectAdmin, "source": "user.admin.budi.tar.zst", "top": "", "rules": directAdminRules()})
	if err != nil {
		t.Fatal(err)
	}

	entries := archiveEntries(t, filepath.Join(env.dir, "budi-imported.tar.gz"))
	want := []string{
		".bashrc", ".rayxo-backup/", ".rayxo-backup/databases/", ".rayxo-backup/databases/budi_shop.sql", ".rayxo-backup/manifest.json", ".rayxo-backup/settings.json",
		"domains/blog.budi.test/public_html/index.html", "domains/budi.test/public_html/", "domains/budi.test/public_html/index.php",
		"imap/budi.test/info/Maildir/cur/1", "private/", "private/notes.txt",
	}

	if names := sortedEntries(entries); !slices.Equal(names, want) {
		t.Errorf("entries = %v", names)
	}

	if !strings.Contains(entries[".rayxo-backup/databases/budi_shop.sql"], "DEFINER=CURRENT_USER") {
		t.Error("the definers of the dump must be the importing user's")
	}

	if leftovers, _ := filepath.Glob(filepath.Join(env.dir, ".*")); len(leftovers) != 0 {
		t.Errorf("left behind: %v", leftovers)
	}

	if _, err := callBackup(env.methods, "backup.server_restore", map[string]any{"username": "budi", "name": "budi-imported.tar.gz", "files": true, "email": true, "databases": []string{"budi_shop"}}); err != nil {
		t.Fatal(err)
	}

	if readHome(t, filepath.Join(env.home, "domains/blog.budi.test/public_html/index.html")) != "blog" ||
		readHome(t, filepath.Join(env.home, "private/notes.txt")) != "notes" {
		t.Error("the converted backup did not restore")
	}
}

func TestAnImportThatFailsLeavesNothing(t *testing.T) {
	env := newServerBackupEnv(t)
	writeBackupFile(t, filepath.Join(env.dir, "cpmove-budi.tar.gz"), compressed(t, tarOf(t, cpanelBackupEntries()), "gzip"))

	_, err := env.convertImport(t, map[string]any{"databases": []map[string]string{{"from": "budi_gone", "to": "budi_gone"}}})
	if err == nil || !strings.Contains(err.Error(), "no dump of budi_gone") {
		t.Errorf("err = %v", err)
	}

	env.free = 9 << 30

	_, err = env.convertImport(t, nil)

	var refused *rpc.Error
	if !errors.As(err, &refused) || refused.Data.(map[string]string)["reason"] != reasonNoSpace {
		t.Errorf("err = %v", err)
	}

	if leftovers, _ := filepath.Glob(filepath.Join(env.dir, "*")); !slices.Equal(leftovers, []string{filepath.Join(env.dir, "cpmove-budi.tar.gz")}) {
		t.Errorf("left behind: %v", leftovers)
	}
}

func TestAnImportReportsTheNamesOfTheEntriesItDrops(t *testing.T) {
	env := newServerBackupEnv(t)

	// A cPanel backup with two entries the importer drops: one whose name is
	// not valid UTF-8, one whose name is absolute. Both are still left out.
	entries := append(cpanelBackupEntries(),
		archiveEntry{name: "cpmove-budi/homedir/caf\xe9.txt", content: "bad name"},
		archiveEntry{name: "/etc/evil", content: "out of the home"},
	)
	writeBackupFile(t, filepath.Join(env.dir, "cpmove-budi.tar.gz"), compressed(t, tarOf(t, entries), "gzip"))

	result, err := env.convertImport(t, nil)
	if err != nil {
		t.Fatal(err)
	}

	if result["refused"].(float64) != 2 {
		t.Errorf("refused = %v", result["refused"])
	}

	want := []string{strconv.Quote("cpmove-budi/homedir/caf\xe9.txt"), strconv.Quote("/etc/evil")}
	if names := refusedNames(t, result); !slices.Equal(names, want) {
		t.Errorf("refused names = %v", names)
	}

	// The dropped entries are still left out: the backup holds only the files
	// with usable names, exactly as without the bad entries.
	output := sortedEntries(archiveEntries(t, filepath.Join(env.dir, "budi-imported.tar.gz")))
	good := []string{
		".rayxo-backup/", ".rayxo-backup/databases/", ".rayxo-backup/databases/budi_shop.sql", ".rayxo-backup/manifest.json", ".rayxo-backup/settings.json",
		"domains/addon.test/public_html/index.html", "domains/budi.test/public_html/index.php",
		"imap/budi.test/info/Maildir/.Sent/cur/2", "imap/budi.test/info/Maildir/cur/1", "imap/budi.test/info/Maildir/dovecot-uidlist",
		"perl5/Lib.pm",
	}

	if !slices.Equal(output, good) {
		t.Errorf("entries = %v", output)
	}
}

func TestAnImportReportsNamesDroppedFromTheNestedHome(t *testing.T) {
	env := newServerBackupEnv(t)

	// A DirectAdmin backup whose home archive (backup/home.tar.gz) holds an
	// entry with a non-UTF-8 name: the nested walk reports it too.
	home := tarOf(t, []archiveEntry{
		{name: ".bashrc", content: "# shell"},
		{name: "caf\xe9.txt", content: "bad name"},
	})
	entries := []archiveEntry{
		{name: "backup", dir: true},
		{name: "backup/user.conf", content: "username=budi\n"},
		{name: "backup/home.tar.gz", content: string(compressed(t, home, "gzip"))},
		{name: "domains", dir: true},
		{name: "domains/budi.test/public_html", dir: true},
		{name: "domains/budi.test/public_html/index.php", content: "<?php echo 'main';"},
	}
	writeBackupFile(t, filepath.Join(env.dir, "user.admin.budi.tar.gz"), compressed(t, tarOf(t, entries), "gzip"))

	result, err := env.convertImport(t, map[string]any{
		"kind":      files.KindDirectAdmin,
		"source":    "user.admin.budi.tar.gz",
		"top":       "",
		"databases": []map[string]string{},
		"rules": []files.HomeRule{
			{From: ".bashrc", To: ".bashrc"},
			{From: "domains/budi.test/public_html", To: "domains/budi.test/public_html"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if result["refused"].(float64) != 1 || !slices.Equal(refusedNames(t, result), []string{strconv.Quote("caf\xe9.txt")}) {
		t.Errorf("refused = %v, names = %v", result["refused"], result["refused_names"])
	}

	// The badly named entry of the nested home is still left out.
	output := sortedEntries(archiveEntries(t, filepath.Join(env.dir, "budi-imported.tar.gz")))
	want := []string{
		".bashrc", ".rayxo-backup/", ".rayxo-backup/manifest.json", ".rayxo-backup/settings.json",
		"domains/budi.test/public_html/", "domains/budi.test/public_html/index.php",
	}

	if !slices.Equal(output, want) {
		t.Errorf("entries = %v", output)
	}
}

func TestAnImportOnlyWritesInsideTheHomeAndTheBackupsFolder(t *testing.T) {
	env := newServerBackupEnv(t)
	writeBackupFile(t, filepath.Join(env.dir, "cpmove-budi.tar.gz"), compressed(t, tarOf(t, cpanelBackupEntries()), "gzip"))

	for name, params := range map[string]map[string]any{
		"rule out of the home":        {"rules": []files.HomeRule{{From: "public_html", To: "../etc"}}},
		"rule into the backup's meta": {"rules": []files.HomeRule{{From: "public_html", To: ".rayxo-backup/settings.json"}}},
		"rule from out of the home":   {"rules": []files.HomeRule{{From: "../x", To: "x"}}},
		"replacing the source":        {"name": "cpmove-budi.tar.gz"},
		"source by a path":            {"source": "../cpmove-budi.tar.gz"},
		"database name here":          {"databases": []map[string]string{{"from": "budi_shop", "to": "Budi-Shop"}}},
		"database named twice":        {"databases": []map[string]string{{"from": "a", "to": "budi_a"}, {"from": "b", "to": "budi_a"}}},
		"top by a path":               {"top": "../cpmove-budi"},
		"unknown panel":               {"kind": "plesk"},
		"DirectAdmin with a top":      {"kind": files.KindDirectAdmin},
	} {
		_, err := env.convertImport(t, params)

		var refused *rpc.Error
		if !errors.As(err, &refused) || refused.Code != rpc.CodeInvalidParams {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
