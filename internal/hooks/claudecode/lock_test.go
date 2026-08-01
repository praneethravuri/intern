package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockFirstCallerSucceeds(t *testing.T) {
	dir := t.TempDir()

	release, ok, err := TryLock(dir, "session-1")
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true for the first caller")
	}
	release()
}

// TestTryLockSecondConcurrentCallerLosesSilently is the single-flight
// contract: a live holder's lock file must make every other caller for the
// same key back off with ok=false and no error, never an error the hook
// runner would have to handle specially.
func TestTryLockSecondConcurrentCallerLosesSilently(t *testing.T) {
	dir := t.TempDir()

	release, ok, err := TryLock(dir, "session-1")
	if err != nil || !ok {
		t.Fatalf("first TryLock = (%v, %v), want (true, nil)", ok, err)
	}
	defer release()

	_, ok2, err2 := TryLock(dir, "session-1")
	if err2 != nil {
		t.Fatalf("second TryLock returned an error, want a quiet false: %v", err2)
	}
	if ok2 {
		t.Fatal("second concurrent TryLock for the same key = true, want false")
	}
}

func TestTryLockDifferentKeysDoNotContend(t *testing.T) {
	dir := t.TempDir()

	release1, ok1, err := TryLock(dir, "session-1")
	if err != nil || !ok1 {
		t.Fatalf("TryLock session-1 = (%v, %v)", ok1, err)
	}
	defer release1()

	release2, ok2, err := TryLock(dir, "session-2")
	if err != nil || !ok2 {
		t.Fatalf("TryLock session-2 = (%v, %v), want (true, nil): a different key must not contend", ok2, err)
	}
	release2()
}

// TestTryLockReclaimsAStaleLock mirrors the daemon's own singleton lock: a
// lock file naming a pid that is no longer running must be reclaimed rather
// than wedging every future hook invocation shut forever.
func TestTryLockReclaimsAStaleLock(t *testing.T) {
	dir := t.TempDir()
	key := "session-1"

	path := lockPath(dir, key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("1073741824 0\n"), 0o600); err != nil { // 1<<30: implausible pid
		t.Fatalf("seed stale lock: %v", err)
	}

	release, ok, err := TryLock(dir, key)
	if err != nil {
		t.Fatalf("TryLock over a stale lock: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true: a stale lock must be reclaimed")
	}
	release()
}

func TestTryLockReleaseThenReacquireSucceeds(t *testing.T) {
	dir := t.TempDir()

	release, ok, err := TryLock(dir, "session-1")
	if err != nil || !ok {
		t.Fatalf("first TryLock = (%v, %v)", ok, err)
	}
	release()

	_, ok2, err := TryLock(dir, "session-1")
	if err != nil || !ok2 {
		t.Fatalf("second TryLock after release = (%v, %v), want (true, nil)", ok2, err)
	}
}

func TestLockPathIsStableAndFilesystemSafe(t *testing.T) {
	dir := t.TempDir()
	p := lockPath(dir, "some/weird session id\nwith control bytes")
	if filepath.Dir(p) != dir {
		t.Fatalf("lockPath escaped dir: %s", p)
	}
	if p != lockPath(dir, "some/weird session id\nwith control bytes") {
		t.Fatal("lockPath is not stable for the same key")
	}
}
