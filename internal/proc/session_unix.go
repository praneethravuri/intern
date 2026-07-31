//go:build unix

package proc

import "golang.org/x/sys/unix"

// SameSession reports whether a and b share a POSIX session (the group
// descended from one session leader, typically a login shell) -- true for
// siblings a shell launched, not just direct parent/child pairs.
func SameSession(a, b int) bool {
	if a <= 0 || b <= 0 {
		return false
	}
	sa, err := unix.Getsid(a)
	if err != nil {
		return false
	}
	sb, err := unix.Getsid(b)
	if err != nil {
		return false
	}
	return sa == sb
}
