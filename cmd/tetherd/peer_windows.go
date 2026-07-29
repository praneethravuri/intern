//go:build windows

package main

import "net"

// getPeerPID has no equivalent of SO_PEERCRED on Windows; the pid is
// logging-only, not a security control (the socket directory ACL is).
func getPeerPID(_ net.Conn) (int, error) {
	return 0, nil
}
