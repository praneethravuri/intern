package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

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

func TestAcquireLock_StaleMarkerDoesNotBlockStart(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	if err := os.WriteFile(path, []byte("stale marker\n"), 0o600); err != nil {
		t.Fatalf("write stale marker: %v", err)
	}

	release, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquireLock over a stale marker: %v", err)
	}
	release()
}

func TestAcquireLock_PreservesMarkerSymlink(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")
	target := filepath.Join(dir, "target")
	const contents = "do not mutate\n"
	if err := os.WriteFile(target, []byte(contents), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("make marker symlink: %v", err)
	}

	release, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquireLock with a marker symlink: %v", err)
	}
	release()

	// #nosec G304 -- target is a test-owned file created in shortTempDir.
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != contents {
		t.Fatalf("symlink target = %q, want %q", got, contents)
	}
}

func TestAcquireLock_ReleaseThenReacquire(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.lock")

	release, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	release()

	release2, err := acquireLock(path)
	if err != nil {
		t.Fatalf("second acquireLock after release: %v", err)
	}
	release2()
}

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

func TestAcquireLock_ParentDirMissing(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "no-such-dir", "s.lock")
	if _, err := acquireLock(path); err == nil {
		t.Fatal("acquireLock with a missing parent dir: want error, got nil")
	}
}

func TestAcquireLock_ParentPathIsNotDirectory(t *testing.T) {
	dir := shortTempDir(t)
	parent := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(parent, []byte("file"), 0o600); err != nil {
		t.Fatalf("seed file standing in for the socket directory: %v", err)
	}

	if _, err := acquireLock(filepath.Join(parent, "sock")); err == nil {
		t.Fatal("acquireLock with a file parent: want error, got nil")
	}
}
