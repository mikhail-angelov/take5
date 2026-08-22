//go:build windows

package main

import "syscall"

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// detachedSysProcAttr puts the spawned render process in its own process group, detached
// from this process's console, so it survives this process's death instead of receiving
// whatever Chrome sends its native-messaging host when the port disconnects.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
}
