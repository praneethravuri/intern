package main

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/praneethravuri/helios/pkg/protocol"
)

func TestParseBroadcast(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantTarget string
		wantMsg    string
		wantOK     bool
	}{
		{
			name:       "message only defaults to broadcast all",
			args:       []string{"hello world"},
			wantTarget: protocol.BroadcastAll,
			wantMsg:    "hello world",
			wantOK:     true,
		},
		{
			name:       "session id and message",
			args:       []string{"session-1", "hello world"},
			wantTarget: "session-1",
			wantMsg:    "hello world",
			wantOK:     true,
		},
		{
			name:   "no args is not ok",
			args:   []string{},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, msg, ok := parseBroadcast(tt.args)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if target != tt.wantTarget {
				t.Errorf("target = %q, want %q", target, tt.wantTarget)
			}
			if msg != tt.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tt.wantMsg)
			}
		})
	}
}

func TestValidSessionID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"claude-492", true},
		{"my-session", true},
		{"", false},
		{"my session", false},
		{"session\ttab", false},
		{"session\nline", false},
	}
	for _, tt := range tests {
		if got := validSessionID(tt.id); got != tt.want {
			t.Errorf("validSessionID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestRunInteractive_ReturnsWhenServerCloses(t *testing.T) {
	client, server := net.Pipe()

	var stdout bytes.Buffer
	// stdin that never produces data or EOF, standing in for a user who isn't typing.
	stdinR, _ := io.Pipe()

	done := make(chan struct{})
	go func() {
		runInteractive(client, stdinR, &stdout)
		close(done)
	}()

	// net.Pipe is synchronous/unbuffered: Write blocks until a matching Read is
	// in flight, so this must happen after runInteractive's copy goroutine starts.
	if _, err := server.Write([]byte("hello\n")); err != nil {
		t.Fatalf("server.Write() = %v", err)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("server.Close() = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runInteractive did not return after the server closed its side")
	}

	if stdout.String() != "hello\n" {
		t.Errorf("stdout = %q; want %q", stdout.String(), "hello\n")
	}
}
