package main

import (
	"testing"

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
