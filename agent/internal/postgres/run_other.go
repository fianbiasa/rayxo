//go:build !linux

package postgres

import "syscall"

// sysProcAttr runs the import reader as as; outside Linux (development only) it
// cannot also die with the agent.
func sysProcAttr(as Identity) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(as.UID), Gid: uint32(as.GID), Groups: []uint32{}},
		Setpgid:    true,
	}
}

// groupAttr runs the import loader in its own process group; outside Linux
// (development only) it cannot also die with the agent.
func groupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
