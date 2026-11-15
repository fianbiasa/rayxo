//go:build !linux

package files

import "syscall"

// syncHome flushes to the disk what was written in the home without being
// flushed file by file. Outside Linux, where the agent only runs in
// development, every filesystem is flushed.
func (h *Home) syncHome() error {
	syscall.Sync()

	return nil
}
