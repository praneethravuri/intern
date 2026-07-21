package protocol

import (
	"testing"
)

func TestFormatBroadcast(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		msg     string
		want    string
		wantErr bool
	}{
		{
			name:   "broadcast all with simple message",
			target: BroadcastAll,
			msg:    "hello",
			want:   "BROADCAST * hello\n",
		},
		{
			name:   "broadcast to named session",
			target: "session-1",
			msg:    "hello",
			want:   "BROADCAST session-1 hello\n",
		},
		{
			name:   "message with trailing newline",
			target: BroadcastAll,
			msg:    "hello\n",
			want:   "BROADCAST * hello\n",
		},
		{
			name:   "empty target defaults to BroadcastAll",
			target: "",
			msg:    "hello",
			want:   "BROADCAST * hello\n",
		},
		{
			name:    "message with embedded newline",
			target:  BroadcastAll,
			msg:     "line1\nline2",
			wantErr: true,
		},
		{
			name:    "target with a space",
			target:  "my session",
			msg:     "hello",
			wantErr: true,
		},
		{
			name:    "target with an embedded newline",
			target:  "session\n1",
			msg:     "hello",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatBroadcast(tt.target, tt.msg)
			if tt.wantErr {
				if err == nil {
					t.Errorf("FormatBroadcast(%q, %q) error = nil, want error", tt.target, tt.msg)
				}
				return
			}
			if err != nil {
				t.Fatalf("FormatBroadcast(%q, %q) unexpected error: %v", tt.target, tt.msg, err)
			}
			if got != tt.want {
				t.Errorf("FormatBroadcast(%q, %q) = %q, want %q", tt.target, tt.msg, got, tt.want)
			}
		})
	}
}

func TestParseBroadcast(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantOk     bool
		wantTarget string
		wantMsg    string
	}{
		{
			name:       "valid broadcast all",
			line:       "BROADCAST * hello\n",
			wantOk:     true,
			wantTarget: "*",
			wantMsg:    "hello",
		},
		{
			name:       "valid broadcast to named session",
			line:       "BROADCAST session-1 world\n",
			wantOk:     true,
			wantTarget: "session-1",
			wantMsg:    "world",
		},
		{
			name:       "message with spaces",
			line:       "BROADCAST * hello world test\n",
			wantOk:     true,
			wantTarget: "*",
			wantMsg:    "hello world test",
		},
		{
			name:   "missing BROADCAST prefix",
			line:   "* hello\n",
			wantOk: false,
		},
		{
			name:   "no space after BROADCAST",
			line:   "BROADCASTsession-1 hello\n",
			wantOk: false,
		},
		{
			name:   "empty line",
			line:   "",
			wantOk: false,
		},
		{
			name:   "only BROADCAST",
			line:   "BROADCAST\n",
			wantOk: false,
		},
		{
			name:   "only BROADCAST with space",
			line:   "BROADCAST \n",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, msg, ok := ParseBroadcast(tt.line)
			if ok != tt.wantOk {
				t.Errorf("ParseBroadcast(%q) ok = %v, want %v", tt.line, ok, tt.wantOk)
				return
			}
			if !tt.wantOk {
				return
			}
			if target != tt.wantTarget {
				t.Errorf("ParseBroadcast(%q) target = %q, want %q", tt.line, target, tt.wantTarget)
			}
			if msg != tt.wantMsg {
				t.Errorf("ParseBroadcast(%q) msg = %q, want %q", tt.line, msg, tt.wantMsg)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		target string
		msg    string
	}{
		{"*", "hello"},
		{"session-1", "world"},
		{"my-session", "echo test"},
		{BroadcastAll, "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.target+"-"+tt.msg, func(t *testing.T) {
			formatted, err := FormatBroadcast(tt.target, tt.msg)
			if err != nil {
				t.Fatalf("FormatBroadcast(%q, %q) unexpected error: %v", tt.target, tt.msg, err)
			}
			target, msg, ok := ParseBroadcast(formatted)
			if !ok {
				t.Errorf("ParseBroadcast(%q) failed", formatted)
				return
			}
			if target != tt.target {
				t.Errorf("round-trip target mismatch: %q -> %q", tt.target, target)
			}
			if msg != tt.msg {
				t.Errorf("round-trip msg mismatch: %q -> %q", tt.msg, msg)
			}
		})
	}
}
