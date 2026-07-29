package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/praneethravuri/tether/internal/proc"
)

// acquireLock creates an exclusive marker file at path holding this
// process's pid, so two tetherd processes can't both start against the same
// socket. A stale lock (pid no longer running) is reclaimed and retried once.
// Returns a release func to remove the lock file.
func acquireLock(path string) (release func(), err error) {
	const maxAttempts = 2

	for attempt := 0; attempt < maxAttempts; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d\n", os.Getpid())
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("tetherd: write lock file %s: %w", path, errors.Join(werr, cerr))
			}
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("tetherd: create lock file %s: %w", path, err)
		}

		pid, rerr := readLockPID(path)
		if rerr != nil {
			// Uncertain state (e.g. a concurrent writer mid-write) is treated as held.
			return nil, fmt.Errorf("tetherd: lock file %s exists and could not be read: %w", path, rerr)
		}
		if proc.Alive(pid) {
			return nil, fmt.Errorf(
				"tetherd: another tetherd (pid %d) is already starting up or running against %s",
				pid, path)
		}
		_ = os.Remove(path) // stale: creator is gone
	}

	return nil, fmt.Errorf("tetherd: could not acquire lock file %s", path)
}

// readLockPID parses the pid out of a lock file written by acquireLock.
func readLockPID(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("malformed lock file: %w", err)
	}
	return pid, nil
}
