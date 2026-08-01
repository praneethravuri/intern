package protocol

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// SocketPath resolves where the socket should live based on the hierarchy
// INTERN_SOCK, then $XDG_RUNTIME_DIR/intern/sock, then ~/.intern/sock.
func SocketPath() (string, error) {
	if sock := os.Getenv("INTERN_SOCK"); sock != "" {
		return sock, nil
	}

	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "intern", "sock"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find home dir: %w", err)
	}

	return filepath.Join(home, ".intern", "sock"), nil
}

// DBPath resolves where the sqlite database should live: INTERN_DB if set,
// otherwise ~/.intern/intern.db. The ~/.intern directory is created with 0700
// so the message store is only readable by its owner.
func DBPath() (string, error) {
	if db := os.Getenv("INTERN_DB"); db != "" {
		return db, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find home dir: %w", err)
	}

	dir := filepath.Join(home, ".intern")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	return filepath.Join(dir, "intern.db"), nil
}

// LogPath resolves where the daemon's log lives: ~/.intern/daemon.log. This
// is the file every bug report starts with, so its location never varies
// with how the daemon was started.
func LogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find home dir: %w", err)
	}

	dir := filepath.Join(home, ".intern")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	return filepath.Join(dir, "daemon.log"), nil
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
