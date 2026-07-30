package daemon

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
// process's pid and start time, so two tetherd processes can't both start
// against the same socket, and a pid recycled onto a crashed daemon's number
// is never mistaken for it still running. A stale lock is reclaimed and
// retried once. Returns a release func to remove the lock file.
func acquireLock(path string) (release func(), err error) {
	const maxAttempts = 2

	for attempt := 0; attempt < maxAttempts; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			pid := os.Getpid()
			start, _ := proc.StartTime(pid) // 0 on failure is proc.AliveAt's "unknown"
			_, werr := fmt.Fprintf(f, "%d %d\n", pid, start)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("tether: write lock file %s: %w", path, errors.Join(werr, cerr))
			}
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("tether: create lock file %s: %w", path, err)
		}

		pid, start, rerr := readLockIdentity(path)
		if rerr != nil {
			// Uncertain state (e.g. a concurrent writer mid-write) is treated as held.
			return nil, fmt.Errorf("tether: lock file %s exists and could not be read: %w", path, rerr)
		}
		if proc.AliveAt(pid, start) {
			return nil, fmt.Errorf(
				"tether: another daemon (pid %d) is already starting up or running against %s",
				pid, path)
		}
		_ = os.Remove(path) // stale: creator is gone, or a different process now holds that pid
	}

	return nil, fmt.Errorf("tether: could not acquire lock file %s", path)
}

// readLockIdentity parses the pid, and start time if present, out of a lock
// file written by acquireLock. A legacy single-field file (pid only, written
// by an older tether) parses with start 0, which proc.AliveAt treats as
// "unknown" and falls back to a plain pid check.
func readLockIdentity(path string) (pid int, start int64, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, 0, fmt.Errorf("malformed lock file: empty")
	}
	pid, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, fmt.Errorf("malformed lock file: %w", err)
	}
	if len(fields) > 1 {
		start, err = strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("malformed lock file: %w", err)
		}
	}
	return pid, start, nil
}
