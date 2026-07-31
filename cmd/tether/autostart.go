package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/praneethravuri/tether/internal/protocol"
)

const (
	autoStartPollInterval = 20 * time.Millisecond
	autoStartTimeout      = 3 * time.Second
)

// spawnDaemon starts a detached daemon and blocks until sock is dialable.
// Overridden in tests, which must never exec a real subprocess.
var spawnDaemon = autoStartDaemon

// autoStartDaemon re-execs this same binary with no arguments, detached from
// the calling shell, so it keeps running after the short-lived CLI process
// that spawned it exits.
func autoStartDaemon(sock string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find my own executable: %w", err)
	}

	// Only a safety net for anything printed before the daemon's own
	// rotating structured logger takes over -- the daemon owns rotation of
	// this same file once it's running (internal/daemon's sweep loop).
	logPath, err := protocol.LogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()

	devnull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = devnull.Close() }()

	cmd := exec.Command(exe)
	cmd.Stdin = devnull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Setsid detaches the daemon into its own session, so it outlives this
	// short-lived CLI invocation instead of dying with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	go func() { _ = cmd.Wait() }() // reap it; nothing here waits on it directly

	deadline := time.Now().Add(autoStartTimeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(autoStartPollInterval)
	}
	return fmt.Errorf("did not become reachable within %s", autoStartTimeout)
}
