//go:build !linux

package actions

import "os"

// renameNoReplace renames oldpath to newpath, failing with an error that
// errors.Is(err, fs.ErrExist) reports when newpath already exists. Away from
// Linux (tests on other systems) it links the new name and removes the old:
// a hard link fails when the name is taken, which is all the no-overwrite
// guarantee the callers rely on.
func renameNoReplace(oldpath, newpath string) error {
	if err := os.Link(oldpath, newpath); err != nil {
		return err
	}

	return os.Remove(oldpath)
}
