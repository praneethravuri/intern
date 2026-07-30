package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	autoStartPollInterval = 20 * time.Millisecond
	autoStartTimeout      = 3 * time.Second

	// daemonLogMaxBytes caps the auto-started daemon's log; past this it is
	// truncated on the next start rather than rotated, since it is a
	// diagnostic tail, not an audit trail.
	daemonLogMaxBytes = 1 << 20
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

	logPath, err := daemonLogPath()
	if err != nil {
		return err
	}
	logFile, err := openDaemonLog(logPath)
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

// daemonLogPath is where an auto-started daemon's stdout/stderr go, since it
// has no terminal of its own to log to.
func daemonLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tether", "daemon.log"), nil
}

// openDaemonLog appends to path, truncating first if it has grown past
// daemonLogMaxBytes.
func openDaemonLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > daemonLogMaxBytes {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
