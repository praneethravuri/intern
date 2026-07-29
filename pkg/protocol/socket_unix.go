//go:build unix

package protocol

import (
	"net"
	"sync"

	"golang.org/x/sys/unix"
)

// umaskMu guards the process-wide umask across concurrent Listen calls.
var umaskMu sync.Mutex

// bindSocket binds the unix socket under a umask that forces it to 0600;
// bind(2) has no way to set an explicit mode, and chmod after the fact
// leaves the socket briefly connectable.
func bindSocket(path string) (net.Listener, error) {
	umaskMu.Lock()
	oldMask := unix.Umask(0177) // 0777 &^ 0177 = 0600
	listener, err := net.Listen("unix", path)
	unix.Umask(oldMask)
	umaskMu.Unlock()

	return listener, err
}
