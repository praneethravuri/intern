package protocol

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// SocketPath resolves where the socket should live based on the hierarchy
// TETHER_SOCK, then $XDG_RUNTIME_DIR/tether/sock, then ~/.tether/sock.
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

// DBPath resolves where the sqlite database should live: TETHER_DB if set,
// otherwise ~/.tether/tether.db. The ~/.tether directory is created with 0700
// so the message store is only readable by its owner.
func DBPath() (string, error) {
	if db := os.Getenv("TETHER_DB"); db != "" {
		return db, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find home dir: %w", err)
	}

	dir := filepath.Join(home, ".tether")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	return filepath.Join(dir, "tether.db"), nil
}

// Listen creates the private 0700 directory that holds the socket, removes
// any stale socket file, and binds inside it (see bindSocket for the
// platform-specific file permissions).
func Listen(path string) (net.Listener, error) {
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	_ = os.Remove(path)

	listener, err := bindSocket(path)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	return listener, nil
}
