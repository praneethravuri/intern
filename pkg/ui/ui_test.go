package ui

import (
	"strings"
	"testing"

	"github.com/praneethravuri/helios/pkg/protocol"
)

func TestPaneWidths(t *testing.T) {
	tests := []struct {
		total            int
		wantLeft         int
		wantRightAtLeast int
	}{
		{total: 0, wantLeft: 30, wantRightAtLeast: 0},  // pre-resize fallback
		{total: 40, wantLeft: 30, wantRightAtLeast: 0}, // below minimum, clamp to fallback
		{total: 200, wantLeft: 90, wantRightAtLeast: 90},
	}
	for _, tt := range tests {
		left, right := paneWidths(tt.total)
		if left != tt.wantLeft {
			t.Errorf("paneWidths(%d) left = %d, want %d", tt.total, left, tt.wantLeft)
		}
		if right < tt.wantRightAtLeast {
			t.Errorf("paneWidths(%d) right = %d, want at least %d", tt.total, right, tt.wantRightAtLeast)
		}
	}
}

func TestBroadcastTarget(t *testing.T) {
	sessions := []string{"a", "b"}
	if got := broadcastTarget(sessions, 0); got != protocol.BroadcastAll {
		t.Errorf("broadcastTarget(cursor=0) = %q, want %q", got, protocol.BroadcastAll)
	}
	if got := broadcastTarget(sessions, 1); got != "a" {
		t.Errorf("broadcastTarget(cursor=1) = %q, want %q", got, "a")
	}
	if got := broadcastTarget(sessions, 2); got != "b" {
		t.Errorf("broadcastTarget(cursor=2) = %q, want %q", got, "b")
	}
}

func TestRenderSessionList(t *testing.T) {
	sessions := []string{"a", "b", "c"}

	out := renderSessionList(sessions, 1, 10)
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[1], ">") {
		t.Errorf("cursor row = %q, want prefix %q", lines[1], ">")
	}

	// maxRows smaller than the list (including the synthetic "all" row) truncates with a trailer.
	out = renderSessionList(sessions, 0, 2)
	if !strings.Contains(out, "more") {
		t.Errorf("renderSessionList with maxRows=2 over 4 rows = %q, want a truncation trailer", out)
	}
}
