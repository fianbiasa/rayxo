package files

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// sysProcAttr makes the helper run as the account, with only the account's
// own group, die with the agent, and lead its own process group so the agent
// can kill it and any children it spawns together.
func sysProcAttr(account Account) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid:    uint32(account.UID),
			Gid:    uint32(account.GID),
			Groups: []uint32{},
		},
		Pdeathsig: syscall.SIGKILL,
		Setpgid:   true,
	}
}

// restrictHelper makes the helper non-dumpable, so another process of the same
// account cannot ptrace it or read its memory.
func restrictHelper() {
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
