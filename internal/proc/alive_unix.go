//go:build unix

package proc

import "syscall"

// Alive reports whether pid names a process that is still running, via
// signal 0. Any error, including EPERM (owned by someone else), reads as
// not alive: this can't confirm it's the process a session claimed.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
