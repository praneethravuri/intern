// Package protocol defines the wire format and socket transport for tether.
package protocol

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
)

// SocketPath resolves where the socket should live based on the hierarchy
func SocketPath() (string, error) {
	if sock := os.Getenv("TETHER_SOCK"); sock != "" {
		return sock, nil
	}

	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "tether", "sock"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find home dir: %w", err)
	}

	return filepath.Join(home, ".tether", "sock"), nil
}

// Listen creates the secure 0700 directory and binds a 0600 socket
func Listen(path string) (net.Listener, error) {
	dir := filepath.Dir(path)

	// 0700: only the owner can read, write or enter this directory
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	// Remove any existing dead socket file
	_ = os.Remove(path)

	// Umask 0177 strips all group.other permissions and execute bits
	// 0777 - 0177 = 0600
	oldMask := unix.Umask(0177)
	defer unix.Umask(oldMask) // restore immediately

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	return listener, nil

}
