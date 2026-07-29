//go:build unix

package proc

import "syscall"

// Alive reports whether pid names a process that is still running, via
// signal 0 (POSIX's existence-check no-op).
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return err != syscall.ESRCH
}
