package main

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

	recentObs := store.Observation{Kind: "send", At: now.Add(-5 * time.Second)}
	staleObs := store.Observation{Kind: "inbox", At: now.Add(-5 * time.Minute)}

	tests := []struct {
		name    string
		agent   store.Agent
		last    store.Observation
		blocked bool
		want    string
	}{
		{
			name: "dead pid beats a blocked wait and a recent observation",
			agent: store.Agent{
				PID: implausiblePIDForState, PIDStart: 0, LastSeen: now.Add(-time.Second),
			},
			last:    recentObs,
			blocked: true,
			want:    "gone",
		},
		{
			name:    "recycled pid (wrong start time) reads as gone, not idle",
			agent:   store.Agent{PID: livePID, PIDStart: liveStart + 1},
			last:    staleObs,
			blocked: false,
			want:    "gone",
		},
		{
			name:    "blocked wait beats a stale observation",
			agent:   store.Agent{PID: livePID, PIDStart: liveStart},
			last:    staleObs,
			blocked: true,
			want:    "blocked",
		},
		{
			name:    "recent observation reads as working",
			agent:   store.Agent{PID: livePID, PIDStart: liveStart},
			last:    recentObs,
			blocked: false,
			want:    "working",
		},
		{
			name:    "old observation reads as idle",
			agent:   store.Agent{PID: livePID, PIDStart: liveStart},
			last:    staleObs,
			blocked: false,
			want:    "idle",
		},
		{
			name:    "no observation ever reads as unknown",
			agent:   store.Agent{PID: livePID, PIDStart: liveStart, RegisteredAt: now.Add(-time.Hour)},
			last:    store.Observation{},
			blocked: false,
			want:    "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeState(tc.agent, tc.last, tc.blocked, now)
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

	gone := computeState(store.Agent{PID: implausiblePIDForState}, store.Observation{}, false, now)
	if gone.Source != "pid" {
		t.Fatalf("gone.Source = %q, want pid", gone.Source)
	}

	blocked := computeState(store.Agent{}, store.Observation{}, true, now)
	if blocked.Source != "wait" || blocked.Age != 0 {
		t.Fatalf("blocked = %+v, want source=wait age=0", blocked)
	}

	working := computeState(store.Agent{}, store.Observation{Kind: "send", At: now.Add(-time.Second)}, false, now)
	if working.Source != "observation" || working.Detail != "ran tether send" {
		t.Fatalf("working = %+v", working)
	}

	idle := computeState(store.Agent{}, store.Observation{Kind: "inbox", At: now.Add(-5 * time.Minute)}, false, now)
	if idle.Source != "observation" || idle.Detail != "last ran tether inbox" {
		t.Fatalf("idle = %+v", idle)
	}

	unknown := computeState(store.Agent{RegisteredAt: now.Add(-time.Hour)}, store.Observation{}, false, now)
	if unknown.Source != "registration" {
		t.Fatalf("unknown.Source = %q, want registration", unknown.Source)
	}
}
