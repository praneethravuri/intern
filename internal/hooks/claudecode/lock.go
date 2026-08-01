package claudecode

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/praneethravuri/tether/internal/proc"
)

// TryLock attempts to become the sole runner for key under dir (created if
// needed), mirroring the daemon's own singleton-instance lock (see
// internal/daemon/lock.go): a lock file holds this process's pid and start
// time, so a pid later recycled onto an unrelated process is never mistaken
// for the original holder still running.
//
// ok is false with a nil error when a live holder already owns key -- the
// expected outcome when Claude Code fires the same async hook more than
// once, and the caller's single-flight signal to exit quietly rather than
// double-run. A non-nil error only means the lock's own state could not be
// read or written, in which case the caller should also fail open.
func TryLock(dir, key string) (release func(), ok bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := lockPath(dir, key)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path is our own hashed lock file, never untrusted input
	if err == nil {
		return finishLock(f, path)
	}
	if !errors.Is(err, fs.ErrExist) {
		return nil, false, fmt.Errorf("create lock %s: %w", path, err)
	}

	pid, start, rerr := readLockIdentity(path)
	if rerr != nil {
		return nil, false, nil // uncertain state (e.g. a concurrent writer mid-write): fail open, not our error
	}
	if proc.AliveAt(pid, start) {
		return nil, false, nil // a live holder: this is the expected duplicate-firing case
	}

	_ = os.Remove(path)                                                  // stale: the creator is gone, or a different process now holds that pid
	f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // see above
	if err != nil {
		return nil, false, nil // lost the race to reclaim it: fail open
	}
	return finishLock(f, path)
}

func finishLock(f *os.File, path string) (func(), bool, error) {
	pid := os.Getpid()
	start, _ := proc.StartTime(pid) // 0 on failure is proc.AliveAt's "unknown"
	_, werr := fmt.Fprintf(f, "%d %d\n", pid, start)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(path)
		return nil, false, fmt.Errorf("write lock %s: %w", path, errors.Join(werr, cerr))
	}
	return func() { _ = os.Remove(path) }, true, nil
}

func readLockIdentity(path string) (pid int, start int64, err error) {
	raw, err := os.ReadFile(path) //nolint:gosec // see TryLock
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

// lockPath hashes key so an arbitrary session id -- which this package never
// fully controls -- can neither escape dir nor collide on filesystem-unsafe
// characters.
func lockPath(dir, key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(dir, fmt.Sprintf("%x.lock", sum[:8]))
}
