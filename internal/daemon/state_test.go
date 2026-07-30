package daemon

import (
	"os"
	"testing"
	"time"

	"github.com/praneethravuri/tether/internal/proc"
	"github.com/praneethravuri/tether/internal/store"
)

// implausiblePIDForState mirrors server_test.go's implausiblePID: large
// enough that no supported platform will ever hand it to a real process.
const implausiblePIDForState = 1 << 30

// TestComputeState_PriorityOrder is table-driven and proves precedence, not
// just that each rung individually produces the right label: every case sets
// up conditions for MULTIPLE rungs at once and asserts the higher-priority
// one wins.
func TestComputeState_PriorityOrder(t *testing.T) {
	now := time.Now()
	livePID := os.Getpid()
	liveStart, startErr := proc.StartTime(livePID)
	if startErr != nil {
		t.Skipf("StartTime unsupported in this environment: %v", startErr)
	}

	tests := []struct {
		name    string
		agent   store.Agent
		blocked bool
		want    string
	}{
		{
			name: "dead pid beats a blocked wait and recent activity",
			agent: store.Agent{
				PID: implausiblePIDForState, PIDStart: 0, LastSeen: now.Add(-time.Second),
				LastKind: "send",
			},
			blocked: true,
			want:    "gone",
		},
		{
			name: "recycled pid (wrong start time) reads as gone, not quiet",
			agent: store.Agent{
				PID: livePID, PIDStart: liveStart + 1,
				LastKind: "inbox", LastSeen: now.Add(-5 * time.Minute),
			},
			blocked: false,
			want:    "gone",
		},
		{
			name: "blocked wait beats stale activity",
			agent: store.Agent{
				PID: livePID, PIDStart: liveStart,
				LastKind: "inbox", LastSeen: now.Add(-5 * time.Minute),
			},
			blocked: true,
			want:    "blocked",
		},
		{
			name: "recent activity reads as working",
			agent: store.Agent{
				PID: livePID, PIDStart: liveStart,
				LastKind: "send", LastSeen: now.Add(-5 * time.Second),
			},
			blocked: false,
			want:    "working",
		},
		{
			name: "old activity reads as quiet",
			agent: store.Agent{
				PID: livePID, PIDStart: liveStart,
				LastKind: "inbox", LastSeen: now.Add(-5 * time.Minute),
			},
			blocked: false,
			want:    "quiet",
		},
		{
			name: "nothing ever recorded reads as unknown",
			agent: store.Agent{
				PID: livePID, PIDStart: liveStart, RegisteredAt: now.Add(-time.Hour),
			},
			blocked: false,
			want:    "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeState(tc.agent, tc.blocked, now)
			if got.State != tc.want {
				t.Fatalf("computeState = %+v, want State %q", got, tc.want)
			}
		})
	}
}

// TestComputeState_SourcesAndDetail spot-checks that Source and Detail carry
// the evidence they claim to, not just that State is right.
func TestComputeState_SourcesAndDetail(t *testing.T) {
	now := time.Now()

	gone := computeState(store.Agent{PID: implausiblePIDForState}, false, now)
	if gone.Source != "pid" {
		t.Fatalf("gone.Source = %q, want pid", gone.Source)
	}

	blocked := computeState(store.Agent{}, true, now)
	if blocked.Source != "wait" || blocked.Age != 0 {
		t.Fatalf("blocked = %+v, want source=wait age=0", blocked)
	}

	working := computeState(store.Agent{LastKind: "send", LastSeen: now.Add(-time.Second)}, false, now)
	if working.Source != "heartbeat" || working.Detail != "ran tether send" {
		t.Fatalf("working = %+v", working)
	}

	quiet := computeState(store.Agent{LastKind: "inbox", LastSeen: now.Add(-5 * time.Minute)}, false, now)
	if quiet.Source != "heartbeat" || quiet.Detail != "last ran tether inbox" {
		t.Fatalf("quiet = %+v", quiet)
	}

	unknown := computeState(store.Agent{RegisteredAt: now.Add(-time.Hour)}, false, now)
	if unknown.Source != "registration" {
		t.Fatalf("unknown.Source = %q, want registration", unknown.Source)
	}
}

// TestComputeState_LastNoteOverridesDetail proves a `register --doing` note
// takes over the generic "ran tether X" line once it's set, in both the
// working and quiet branches.
func TestComputeState_LastNoteOverridesDetail(t *testing.T) {
	now := time.Now()

	working := computeState(store.Agent{
		LastKind: "send", LastSeen: now.Add(-time.Second), LastNote: "compiling tests",
	}, false, now)
	if working.Detail != "compiling tests" {
		t.Fatalf("working.Detail = %q, want the note", working.Detail)
	}

	quiet := computeState(store.Agent{
		LastKind: "inbox", LastSeen: now.Add(-5 * time.Minute), LastNote: "compiling tests",
	}, false, now)
	if quiet.Detail != "compiling tests" {
		t.Fatalf("quiet.Detail = %q, want the note", quiet.Detail)
	}
}
