package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/praneethravuri/helios/pkg/protocol"
	"go.uber.org/zap"
)

// errWriter always fails to write, to exercise Broadcast's per-session error path.
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestRegister_DuplicateID(t *testing.T) {
	sm := NewSessionManager()

	var bufA, bufB bytes.Buffer
	if err := sm.Register("a", &bufA); err != nil {
		t.Fatalf("first Register() = %v; want nil", err)
	}
	if err := sm.Register("a", &bufB); err == nil {
		t.Fatal("second Register() with duplicate id = nil error; want non-nil")
	}
}

func TestRegister_DistinctIDs(t *testing.T) {
	sm := NewSessionManager()

	var bufA, bufB bytes.Buffer
	if err := sm.Register("a", &bufA); err != nil {
		t.Fatalf("Register(a) = %v; want nil", err)
	}
	if err := sm.Register("b", &bufB); err != nil {
		t.Fatalf("Register(b) = %v; want nil", err)
	}

	ids := sm.List()
	if len(ids) != 2 {
		t.Fatalf("List() = %v; want 2 ids", ids)
	}
	found := map[string]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if !found["a"] || !found["b"] {
		t.Fatalf("List() = %v; want both %q and %q present", ids, "a", "b")
	}
}

func TestBroadcast_All(t *testing.T) {
	log := zap.NewNop().Sugar()
	sm := NewSessionManager()

	var bufA, bufB bytes.Buffer
	sm.Register("a", &bufA)
	sm.Register("b", &bufB)

	delivered, matched := sm.Broadcast("hi\n", protocol.BroadcastAll, log)

	if delivered != 2 || matched != 2 {
		t.Fatalf("Broadcast() = delivered=%d, matched=%d; want delivered=2, matched=2", delivered, matched)
	}
	if bufA.String() != "hi\n" {
		t.Errorf("bufA = %q; want %q", bufA.String(), "hi\n")
	}
	if bufB.String() != "hi\n" {
		t.Errorf("bufB = %q; want %q", bufB.String(), "hi\n")
	}
}

func TestBroadcast_Targeted(t *testing.T) {
	log := zap.NewNop().Sugar()
	sm := NewSessionManager()

	var bufA, bufB bytes.Buffer
	sm.Register("a", &bufA)
	sm.Register("b", &bufB)

	delivered, matched := sm.Broadcast("hi\n", "a", log)

	if delivered != 1 || matched != 1 {
		t.Fatalf("Broadcast() = delivered=%d, matched=%d; want delivered=1, matched=1", delivered, matched)
	}
	if bufA.String() != "hi\n" {
		t.Errorf("bufA = %q; want %q", bufA.String(), "hi\n")
	}
	if bufB.String() != "" {
		t.Errorf("bufB = %q; want empty (not targeted)", bufB.String())
	}
}

func TestBroadcast_UnknownTarget(t *testing.T) {
	log := zap.NewNop().Sugar()
	sm := NewSessionManager()

	var bufA bytes.Buffer
	sm.Register("a", &bufA)

	delivered, matched := sm.Broadcast("hi\n", "nonexistent", log)

	if delivered != 0 || matched != 0 {
		t.Fatalf("Broadcast() = delivered=%d, matched=%d; want delivered=0, matched=0", delivered, matched)
	}
}

func TestBroadcast_EmptyManager(t *testing.T) {
	log := zap.NewNop().Sugar()
	sm := NewSessionManager()

	delivered, matched := sm.Broadcast("hi\n", protocol.BroadcastAll, log)

	if delivered != 0 || matched != 0 {
		t.Fatalf("Broadcast() = delivered=%d, matched=%d; want delivered=0, matched=0", delivered, matched)
	}
}

func TestBroadcast_WriterError(t *testing.T) {
	log := zap.NewNop().Sugar()
	sm := NewSessionManager()

	var bufA bytes.Buffer
	sm.Register("a", &bufA)
	sm.Register("bad", errWriter{})

	delivered, matched := sm.Broadcast("hi\n", protocol.BroadcastAll, log)

	if matched != 2 {
		t.Fatalf("matched = %d; want 2", matched)
	}
	if delivered != 1 {
		t.Fatalf("delivered = %d; want 1 (one writer errors)", delivered)
	}
}

// TestRunSession_ClientDisconnect_Deregisters reproduces a real leak: runSession
// must notice a dead client and clean up even when the PTY produces no further
// output. It runs a real "cat" child process (via runSession's normal pty.Start
// path) hooked up to a net.Pipe() standing in for the client socket, closes the
// client side, and polls (bounded, no fixed sleep) for the session to disappear
// from the SessionManager.
func TestRunSession_ClientDisconnect_Deregisters(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not found on PATH")
	}

	log := zap.NewNop().Sugar()
	sm := NewSessionManager()
	sessionID := "disconnect-leak-test"

	client, server := net.Pipe()

	done := make(chan struct{})
	go func() {
		runSession(server, sessionID, "cat", sm, log)
		close(done)
	}()

	// Wait for the session to register before disconnecting.
	waitUntil(t, 2*time.Second, func() bool { return containsID(sm.List(), sessionID) })

	// Simulate the client process dying: close its end of the connection.
	if err := client.Close(); err != nil {
		t.Fatalf("client.Close() = %v; want nil", err)
	}

	// runSession must notice and deregister within a bounded time, without the
	// PTY needing to produce any further output.
	waitUntil(t, 3*time.Second, func() bool { return !containsID(sm.List(), sessionID) })

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("runSession did not return after client disconnect")
	}
}

// TestSessionClose_ConcurrentIdempotent calls session.Close() concurrently from two
// goroutines (run with -race) and asserts it neither panics nor double-acts: the child
// process must actually terminate (cmd.Wait() returns) after Close().
func TestSessionClose_ConcurrentIdempotent(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not found on PATH")
	}

	cmd := exec.Command("cat")
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() = %v", err)
	}

	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("os.Open(os.DevNull) = %v", err)
	}

	sess := &session{ptyMaster: f, cmd: cmd}

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			sess.Close()
		}()
	}
	wg.Wait()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cmd.Wait() did not return within 3s after Close()")
	}
}

// blockingWriter simulates a wedged PTY: Write never returns until unblocked.
type blockingWriter struct {
	unblock chan struct{}
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	<-w.unblock
	return len(p), nil
}

func TestBroadcast_StuckWriterDoesNotHangOthers(t *testing.T) {
	log := zap.NewNop().Sugar()
	sm := NewSessionManager()

	stuck := &blockingWriter{unblock: make(chan struct{})}
	defer close(stuck.unblock) // let the goroutine finish so the test process can exit cleanly

	var bufB bytes.Buffer
	sm.Register("stuck", stuck)
	sm.Register("b", &bufB)

	done := make(chan struct{})
	var delivered, matched int
	go func() {
		delivered, matched = sm.Broadcast("hi\n", protocol.BroadcastAll, log)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Broadcast() did not return within 3s; a stuck writer blocked the whole call")
	}

	if matched != 2 {
		t.Fatalf("matched = %d; want 2", matched)
	}
	if delivered != 1 {
		t.Fatalf("delivered = %d; want 1 (stuck writer must not count as delivered)", delivered)
	}
	if bufB.String() != "hi\n" {
		t.Errorf("bufB = %q; want %q", bufB.String(), "hi\n")
	}
}

func TestHandleConnection_UnknownCommand_WritesError(t *testing.T) {
	log := zap.NewNop().Sugar()
	sm := NewSessionManager()

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleConnection(server, sm, log)
		close(done)
	}()

	if _, err := client.Write([]byte("GARBAGE\n")); err != nil {
		t.Fatalf("client.Write() = %v", err)
	}

	buf := make([]byte, 256)
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("expected an error response from the daemon, got read error: %v", err)
	}
	if n == 0 {
		t.Fatal("expected a non-empty error response, got 0 bytes")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConnection did not return")
	}
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// waitUntil polls cond every 10ms until it returns true or timeout elapses,
// failing the test in the latter case. Used instead of a fixed sleep to avoid
// flakiness while keeping the test bounded.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
