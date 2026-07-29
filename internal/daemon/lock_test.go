package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// TestAcquireLock_ExclusiveBetweenTwoCallers is L4: two tetherd processes
// racing to start against the same socket must not both proceed. This is
// the direct, single-threaded version of that guarantee; see
// TestAcquireLock_ConcurrentCallersOnlyOneWins for the version that actually
// races.
func TestAcquireLock_ExclusiveBetweenTwoCallers(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	release, err := acquireLock(path)
	if err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}
	defer release()

	if _, err := acquireLock(path); err == nil {
		t.Fatal("a second acquireLock succeeded while the first still holds the lock")
	}
}

// TestAcquireLock_StaleLockFromADeadProcessIsReclaimed mirrors
// daemonIsLive's own handling of a leftover socket file: a lock file from a
// tetherd that crashed without cleaning up must not permanently block every
// future start.
func TestAcquireLock_StaleLockFromADeadProcessIsReclaimed(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	// A pid essentially guaranteed not to name a live process on any of this
	// project's supported platforms.
	const deadPID = 1 << 30
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", deadPID)), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	release, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquireLock did not reclaim a stale lock: %v", err)
	}
	release()
}

// TestAcquireLock_LiveProcessIsNotReclaimed is the mirror case: a lock file
// naming a pid that IS still running (this test process itself, which is
// certainly alive) must not be reclaimed.
func TestAcquireLock_LiveProcessIsNotReclaimed(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	if _, err := acquireLock(path); err == nil {
		t.Fatal("acquireLock reclaimed a lock naming a live process")
	}
}

// TestAcquireLock_ReleaseThenReacquire proves the released lock file is
// actually gone and a later, unrelated start is unaffected by an earlier one
// that shut down cleanly.
func TestAcquireLock_ReleaseThenReacquire(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	release, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	release()

	if _, err := os.Stat(path); err == nil {
		t.Fatal("release did not remove the lock file")
	}

	release2, err := acquireLock(path)
	if err != nil {
		t.Fatalf("second acquireLock after release: %v", err)
	}
	release2()
}

// TestAcquireLock_ConcurrentCallersOnlyOneWins is the actual L4 scenario:
// several goroutines racing acquireLock against the same path at once, the
// way two tetherd processes starting simultaneously would race the
// underlying O_EXCL create. Exactly one may win.
func TestAcquireLock_ConcurrentCallersOnlyOneWins(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	const n = 20
	var wins int32
	var wg sync.WaitGroup
	releases := make(chan func(), n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := acquireLock(path)
			if err == nil {
				atomic.AddInt32(&wins, 1)
				releases <- release
			}
		}()
	}
	wg.Wait()
	close(releases)

	if wins != 1 {
		t.Fatalf("%d of %d concurrent acquireLock calls succeeded, want exactly 1", wins, n)
	}
	for release := range releases {
		release()
	}
}

func TestReadLockPID_RoundTrip(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	if err := os.WriteFile(path, []byte("4242\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	pid, err := readLockPID(path)
	if err != nil {
		t.Fatalf("readLockPID: %v", err)
	}
	if pid != 4242 {
		t.Fatalf("pid = %d, want 4242", pid)
	}
}

func TestAcquireLock_ParentDirMissing(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "no-such-dir", "s.lock")
	if _, err := acquireLock(path); err == nil {
		t.Fatal("acquireLock with a missing parent dir: want error, got nil")
	}
}

func TestAcquireLock_UnreadableLockFile(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("seed directory standing in for the lock file: %v", err)
	}

	if _, err := acquireLock(path); err == nil {
		t.Fatal("acquireLock over a directory: want error, got nil")
	}
}

func TestReadLockPID_Unreadable(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("seed directory standing in for the lock file: %v", err)
	}

	if _, err := readLockPID(path); err == nil {
		t.Fatal("readLockPID on a directory: want error, got nil")
	}
}

func TestReadLockPID_Malformed(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	if err := os.WriteFile(path, []byte("not a pid"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := readLockPID(path); err == nil {
		t.Fatal("readLockPID accepted a non-numeric lock file")
	}
}
