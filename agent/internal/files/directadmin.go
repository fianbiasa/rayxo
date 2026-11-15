package files

import (
	"archive/tar"
	"io"
	"path"
	"slices"
	"strings"
)

// A DirectAdmin backup (user.<creator>.<user>.tar.zst from Admin Backup,
// or the backup an account makes of itself) holds what DirectAdmin knows
// of the account in backup/: user.conf, crontab.conf, <database>.conf with
// the database's users, and per domain its domain.conf, pointers,
// subdomains and email/. The dumps are backup/<database>.sql. The websites
// and mail are in domains/ and imap/, laid out as here, and the rest of
// the home is an archive of its own, backup/home.tar.zst (or .gz, or
// none). Its .shadow and login_keys/ are never read.

// directAdminMeta is the folder of a DirectAdmin backup that holds what
// DirectAdmin knows of the account.
const directAdminMeta = "backup"

// readDirectAdmin gathers what a DirectAdmin backup holds.
func readDirectAdmin(stream io.Reader, in *inspector) error {
	return walkTar(stream, func(name string, header *tar.Header, content io.Reader) error {
		top, rest, _ := strings.Cut(name, "/")

		switch {
		case top == "domains" || top == "imap":
			in.homeEntry(name, header)
		case top != directAdminMeta:
			return nil
		case directAdminHomeArchive(rest, header):
			return eachNested(content, func(inner string, header *tar.Header, _ io.Reader) error {
				in.homeEntry(inner, header)

				return nil
			})
		default:
			if database, ok := directAdminDump(name, header); ok {
				in.dump(database, header.Size)
			}

			if directAdminMetaFile(rest) {
				return in.file(name, header, content)
			}
		}

		return nil
	})
}

// addDirectAdminHome copies the home of a DirectAdmin backup: domains/,
// imap/ and the archive of the rest.
func (b *BackupWriter) addDirectAdminHome(stream io.Reader, rules []HomeRule, copied *HomeCopied) error {
	return walkTar(stream, func(name string, header *tar.Header, content io.Reader) error {
		top, rest, _ := strings.Cut(name, "/")

		switch {
		case top == "domains" || top == "imap":
			return b.addHomeEntry(rules, name, header, content, copied)
		case top == directAdminMeta && directAdminHomeArchive(rest, header):
			return eachNested(content, func(inner string, header *tar.Header, content io.Reader) error {
				return b.addHomeEntry(rules, inner, header, content, copied)
			})
		}

		return nil
	})
}

// eachNested hands every entry of the archive content to visit.
func eachNested(content io.Reader, visit func(name string, header *tar.Header, content io.Reader) error) error {
	nested, err := openArchive(content)
	if err != nil {
		return err
	}

	err = walkTar(nested.reader, visit)
	if closed := nested.close(err == nil); err == nil {
		err = closed
	}

	return err
}

// directAdminHomeArchive reports whether rest (under backup/) is the
// archive of the home.
func directAdminHomeArchive(rest string, header *tar.Header) bool {
	return header.Typeflag == tar.TypeReg && slices.Contains([]string{"home.tar", "home.tar.gz", "home.tar.zst"}, rest)
}

// directAdminDump is the database whose dump name is: backup/<db>.sql.
func directAdminDump(name string, header *tar.Header) (string, bool) {
	file, ok := strings.CutPrefix(name, directAdminMeta+"/")
	if !ok || header.Typeflag != tar.TypeReg || path.Ext(file) != ".sql" || strings.Contains(file, "/") {
		return "", false
	}

	return strings.TrimSuffix(file, ".sql"), true
}

// directAdminMetaFile reports whether rest (under backup/) is one of the
// small files the panel reads: user.conf, crontab.conf and the
// <database>.conf files, and per domain its settings, pointers,
// subdomains, mailboxes (passwd, quota), forwarders (aliases), catch-all
// (email.conf) and automatic replies.
func directAdminMetaFile(rest string) bool {
	parts := strings.Split(rest, "/")

	switch len(parts) {
	case 1:
		return strings.HasSuffix(parts[0], ".conf")
	case 2:
		return slices.Contains([]string{"domain.conf", "domain.pointers", "subdomain.list", "domain.subdomains.docroot.override"}, parts[1])
	case 3:
		return parts[1] == "email" && slices.Contains([]string{"passwd", "quota", "aliases", "email.conf", "autoresponder.conf", "vacation.conf"}, parts[2])
	}

	return false
}
