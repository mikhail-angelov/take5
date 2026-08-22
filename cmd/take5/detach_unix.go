//go:build !windows

package main

import "syscall"

// detachedSysProcAttr puts the spawned render process in its own session, so it survives
// this process's death instead of receiving whatever signal Chrome sends its native-messaging
// host's process group when the port disconnects.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
