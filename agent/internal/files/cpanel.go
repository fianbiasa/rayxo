package files

import (
	"archive/tar"
	"io"
	"path"
	"slices"
	"strings"
)

// A cPanel backup (cpmove-<user>.tar.gz from pkgacct, or a full backup made
// in cPanel) holds everything under one top folder: what cPanel knows of
// the account (cp/<user>, userdata/, va/, cron/, mysql.sql...), the dumps of
// its databases (mysql/<database>.sql) and its home (homedir/).

const (
	// cpanelHome is the folder of a cPanel backup that holds the home.
	cpanelHome = "homedir"
	// cpanelDumps is the folder that holds the dumps of the databases.
	cpanelDumps = "mysql"
)

// readCpanel gathers what a cPanel backup holds.
func readCpanel(stream io.Reader, in *inspector) error {
	return walkCpanel(stream, &in.inspection.Top, func(rel string, header *tar.Header, content io.Reader) error {
		if inner, ok := strings.CutPrefix(rel, cpanelHome+"/"); ok {
			in.homeEntry(inner, header)
		}

		if name, ok := cpanelDump(rel, header); ok {
			in.dump(name, header.Size)
		}

		if cpanelMetaFile(rel) {
			return in.file(rel, header, content)
		}

		return nil
	})
}

// addCpanelHome copies the home of a cPanel backup, homedir/.
func (b *BackupWriter) addCpanelHome(stream io.Reader, top string, rules []HomeRule, copied *HomeCopied) error {
	return walkCpanel(stream, &top, func(rel string, header *tar.Header, content io.Reader) error {
		if inner, ok := strings.CutPrefix(rel, cpanelHome+"/"); ok {
			return b.addHomeEntry(rules, inner, header, content, copied)
		}

		return nil
	})
}

// cpanelDump is the database whose dump rel is: mysql/<database>.sql.
func cpanelDump(rel string, header *tar.Header) (string, bool) {
	file, ok := strings.CutPrefix(rel, cpanelDumps+"/")
	if !ok || header.Typeflag != tar.TypeReg || path.Ext(file) != ".sql" || strings.Contains(file, "/") {
		return "", false
	}

	return strings.TrimSuffix(file, ".sql"), true
}

// walkCpanel hands every entry of a cPanel backup's tar stream to visit,
// by its name under the top folder. top is the folder all entries must be
// in; an empty one becomes that of the first entry.
func walkCpanel(stream io.Reader, top *string, visit func(rel string, header *tar.Header, content io.Reader) error) error {
	return walkTar(stream, func(name string, header *tar.Header, content io.Reader) error {
		first, rel, _ := strings.Cut(name, "/")

		if *top == "" {
			*top = first
		}

		if first != *top {
			return errorf(ReasonInvalid, "the file is not a cPanel or DirectAdmin backup: it holds more than the folder %s", *top)
		}

		if rel == "" {
			return nil
		}

		return visit(rel, header, content)
	})
}

// cpanelMetaFile reports whether rel is one of the small files the panel
// reads: the account (cp/), its domains (userdata/), forwarders (va/),
// cron jobs (cron/), database users (mysql.sql) and mailboxes (the shadow
// and quota files of each mail domain in the home).
func cpanelMetaFile(rel string) bool {
	switch rel {
	case "version", "homedir_paths", "mysql.sql", "mysql.sql-auth.json":
		return true
	}

	parts := strings.Split(rel, "/")

	switch len(parts) {
	case 2:
		return slices.Contains([]string{"cp", "userdata", "va", "cron"}, parts[0])
	case 4:
		return parts[0] == cpanelHome && parts[1] == "etc" && (parts[3] == "shadow" || parts[3] == "quota")
	}

	return false
}
