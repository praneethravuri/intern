//go:build !darwin && !linux && !windows

package main

import "net"

// getPeerPID has no AF_UNIX peer-credential support on this platform; the
// pid is logging-only, not a security control (the socket directory is).
func getPeerPID(_ net.Conn) (int, error) {
	return 0, nil
}
