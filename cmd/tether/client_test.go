package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/praneethravuri/tether/pkg/protocol"
)

// fakeDaemon listens on a temp socket and replies with resp to every request
// it receives, once. It returns the socket path for TETHER_SOCK.
func fakeDaemon(t *testing.T, resp protocol.Response) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		var req protocol.Request
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		_ = json.NewEncoder(conn).Encode(resp)
	}()

	return sockPath
}

func TestCallDaemon_NoDaemonRunning(t *testing.T) {
	t.Setenv("TETHER_SOCK", filepath.Join(t.TempDir(), "sock"))

	_, err := callDaemon("list")
	if err == nil {
		t.Fatal("expected error when no daemon is listening, got nil")
	}
}

func TestCallDaemon_Success(t *testing.T) {
	t.Setenv("TETHER_SOCK", fakeDaemon(t, protocol.Response{ID: "cli-1", Result: "registered"}))

	res, err := callDaemon("register")
	if err != nil {
		t.Fatalf("callDaemon() error = %v", err)
	}
	if res.Result != "registered" {
		t.Errorf("got result %v, want %q", res.Result, "registered")
	}
}

func TestCallDaemon_DaemonError(t *testing.T) {
	t.Setenv("TETHER_SOCK", fakeDaemon(t, protocol.Response{
		ID:    "cli-1",
		Error: &protocol.Error{Code: 2, Message: "unknown method: bogus"},
	}))

	_, err := callDaemon("bogus")
	if err == nil {
		t.Fatal("expected error from daemon response, got nil")
	}
	want := "daemon error (code 2): unknown method: bogus"
	if err.Error() != want {
		t.Errorf("got error %q, want %q", err.Error(), want)
	}
}
