package protocol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSocketPath_EnvOverride(t *testing.T) {
	expected := "/tmp/custom.sock"
	t.Setenv("TETHER_SOCK", expected) // t.Setenv safely cleans up after the test

	got, err := SocketPath()
	if err != nil {
		t.Fatalf("SocketPath() error = %v", err)
	}

	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestListen_Permissions(t *testing.T) {
	// Create a short temp dir in /tmp to avoid macOS path length limits for sockets
	dir, err := os.MkdirTemp("/tmp", "tether-test-*")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = os.RemoveAll(dir) }() // Ensure cleanup when the test finishes

	sockPath := filepath.Join(dir, "sock")

	listener, err := Listen(sockPath)
	if err != nil {
		t.Fatalf("Listen() failed: %v", err)
	}

	defer func() { _ = listener.Close() }()

	// 1. Verify directory is 0700
	dirStat, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirStat.Mode().Perm() != 0700 {
		t.Errorf("dir permissions got %04o, want 0700", dirStat.Mode().Perm())
	}

	// 2. Verify socket is 0600
	sockStat, err := os.Stat(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	if sockStat.Mode().Perm() != 0600 {
		t.Errorf("socket permissions got %04o, want 0600", sockStat.Mode().Perm())
	}
}
