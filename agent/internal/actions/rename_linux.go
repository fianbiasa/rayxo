//go:build linux

package actions

import "golang.org/x/sys/unix"

// renameNoReplace renames oldpath to newpath, failing with an error that
// errors.Is(err, fs.ErrExist) reports when newpath already exists, so two
// backups of the same name cannot silently overwrite each other even if one
// appears between the caller's existence check and the rename.
func renameNoReplace(oldpath, newpath string) error {
	return unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_NOREPLACE)
}
