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

// TestAcquireLock_RecycledPIDIsReclaimed is the headline fix: a lock file
// naming this test's own pid but a start time that does not match it is a
// pid the crashed daemon held that the OS has since handed to an unrelated
// process (this one) -- proc.AliveAt must see through that and let the lock
// be reclaimed, rather than refusing to start forever.
func TestAcquireLock_RecycledPIDIsReclaimed(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	wrongStart := int64(1) // this process's real start time is certainly not 1
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d %d\n", os.Getpid(), wrongStart)), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	release, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquireLock did not reclaim a lock with a mismatched start time: %v", err)
	}
	release()
}

// TestAcquireLock_LegacySingleFieldFileStillHonoured proves a lock file
// written by an older tether (pid only, no start time) still blocks a
// second start when that pid is genuinely alive -- start 0 is proc.AliveAt's
// "unknown," which falls back to a plain pid check.
func TestAcquireLock_LegacySingleFieldFileStillHonoured(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatalf("write legacy lock: %v", err)
	}

	if _, err := acquireLock(path); err == nil {
		t.Fatal("acquireLock reclaimed a legacy lock naming a live process")
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

func TestReadLockIdentity_RoundTrip(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	if err := os.WriteFile(path, []byte("4242 99999\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	pid, start, err := readLockIdentity(path)
	if err != nil {
		t.Fatalf("readLockIdentity: %v", err)
	}
	if pid != 4242 || start != 99999 {
		t.Fatalf("pid, start = %d, %d, want 4242, 99999", pid, start)
	}
}

// TestReadLockIdentity_LegacySingleField proves a pid-only file (written by
// an older tether) parses with start 0, rather than failing.
func TestReadLockIdentity_LegacySingleField(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	if err := os.WriteFile(path, []byte("4242\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	pid, start, err := readLockIdentity(path)
	if err != nil {
		t.Fatalf("readLockIdentity: %v", err)
	}
	if pid != 4242 || start != 0 {
		t.Fatalf("pid, start = %d, %d, want 4242, 0", pid, start)
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

func TestReadLockIdentity_Unreadable(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("seed directory standing in for the lock file: %v", err)
	}

	if _, _, err := readLockIdentity(path); err == nil {
		t.Fatal("readLockIdentity on a directory: want error, got nil")
	}
}

func TestReadLockIdentity_Malformed(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	if err := os.WriteFile(path, []byte("not a pid"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := readLockIdentity(path); err == nil {
		t.Fatal("readLockIdentity accepted a non-numeric lock file")
	}
}
