//go:build windows

package protocol

import "net"

// bindSocket binds the AF_UNIX socket on Windows. There is no umask and no
// POSIX mode enforcement here; the containing directory's ACL is the only
// control. os.Stat on the socket path also fails on Windows (golang/go#57535)
// — dial it to check liveness instead.
func bindSocket(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}
