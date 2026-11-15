//go:build linux

package files

import "golang.org/x/sys/unix"

// syncHome flushes to the disk what was written in the home without being
// flushed file by file: one syncfs for the whole filesystem.
func (h *Home) syncHome() error {
	dir, err := h.root.Open(".")
	if err != nil {
		return fail(err, "")
	}
	defer dir.Close()

	if err := unix.Syncfs(int(dir.Fd())); err != nil {
		return fail(err, "")
	}

	return nil
}
