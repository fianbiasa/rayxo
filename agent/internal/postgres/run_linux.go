package postgres

import "syscall"

// sysProcAttr runs the import reader as as, with only its own group, and makes
// it die with the agent.
func sysProcAttr(as Identity) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(as.UID), Gid: uint32(as.GID), Groups: []uint32{}},
		Pdeathsig:  syscall.SIGKILL,
		Setpgid:    true,
	}
}

// groupAttr runs the import loader in its own process group and makes it die
// with the agent.
func groupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
}
