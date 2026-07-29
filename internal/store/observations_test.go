package store

import (
	"context"
	"testing"
	"time"
)

func TestObserveLastObservationRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now

	mustRegister(t, s, Agent{Workspace: "ws", Name: "alice", Cwd: "/a"})

	if err := s.Observe(ctx, Observation{Workspace: "ws", Name: "alice", Kind: "tool", Detail: "Bash"}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	clk.advance(time.Second)
	if err := s.Observe(ctx, Observation{Workspace: "ws", Name: "alice", Kind: "tool", Detail: "Read"}); err != nil {
		t.Fatalf("Observe #2: %v", err)
	}

	got, err := s.LastObservation(ctx, "ws", "alice")
	if err != nil {
		t.Fatalf("LastObservation: %v", err)
	}
	if got.Kind != "tool" || got.Detail != "Read" {
		t.Fatalf("LastObservation = %+v, want the most recent (Read)", got)
	}
	if got.At.UnixMilli() != clk.Now().UnixMilli() {
		t.Errorf("At = %v, want %v", got.At, clk.Now())
	}
}

func TestLastObservationUnknownAgent(t *testing.T) {
	s := newStore(t)
	got, err := s.LastObservation(context.Background(), "ws", "ghost")
	if err != nil {
		t.Fatalf("LastObservation(ghost) error = %v, want nil", err)
	}
	if got != (Observation{}) {
		t.Fatalf("LastObservation(ghost) = %+v, want the zero value", got)
	}
}

func TestLastObservations(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now

	mustRegister(t, s, Agent{Workspace: "ws", Name: "alice", Cwd: "/a"})
	mustRegister(t, s, Agent{Workspace: "ws", Name: "bob", Cwd: "/b"})
	mustRegister(t, s, Agent{Workspace: "other", Name: "carol", Cwd: "/c"})

	mustObserve := func(ws, name, kind, detail string) {
		t.Helper()
		if err := s.Observe(ctx, Observation{Workspace: ws, Name: name, Kind: kind, Detail: detail}); err != nil {
			t.Fatalf("Observe(%s@%s): %v", name, ws, err)
		}
		clk.advance(time.Second)
	}

	mustObserve("ws", "alice", "tool", "Bash")
	mustObserve("ws", "bob", "idle", "")
	mustObserve("ws", "alice", "tool", "Grep") // alice's latest should win
	mustObserve("other", "carol", "tool", "Edit")

	got, err := s.LastObservations(ctx, "ws")
	if err != nil {
		t.Fatalf("LastObservations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("LastObservations(ws) = %+v, want 2 entries", got)
	}
	if got["alice"].Detail != "Grep" {
		t.Errorf("alice's latest observation = %+v, want detail Grep", got["alice"])
	}
	if got["bob"].Kind != "idle" {
		t.Errorf("bob's observation = %+v, want kind idle", got["bob"])
	}
	if _, ok := got["carol"]; ok {
		t.Errorf("LastObservations(ws) leaked carol from workspace %q", "other")
	}
}

func TestPendingByWorkspace(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	mustRegister(t, s, Agent{Workspace: "ws", Name: "alice", Cwd: "/a"})
	mustRegister(t, s, Agent{Workspace: "ws", Name: "bob", Cwd: "/b"})

	mustSend(t, s, Message{FromName: "alice", FromWS: "ws", ToName: "bob", ToWS: "ws", Kind: KindNote, Body: "one"})
	mustSend(t, s, Message{FromName: "alice", FromWS: "ws", ToName: "bob", ToWS: "ws", Kind: KindNote, Body: "two"})
	mustSend(t, s, Message{FromName: "bob", FromWS: "ws", ToName: "alice", ToWS: "ws", Kind: KindNote, Body: "three"})

	got, err := s.PendingByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("PendingByWorkspace: %v", err)
	}
	if got["bob"] != 2 {
		t.Errorf("pending for bob = %d, want 2", got["bob"])
	}
	if got["alice"] != 1 {
		t.Errorf("pending for alice = %d, want 1", got["alice"])
	}
}

func TestSweepObservations(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now

	mustRegister(t, s, Agent{Workspace: "ws", Name: "alice", Cwd: "/a"})

	if err := s.Observe(ctx, Observation{Workspace: "ws", Name: "alice", Kind: "old"}); err != nil {
		t.Fatalf("Observe(old): %v", err)
	}
	clk.advance(time.Hour)
	if err := s.Observe(ctx, Observation{Workspace: "ws", Name: "alice", Kind: "new"}); err != nil {
		t.Fatalf("Observe(new): %v", err)
	}

	n, err := s.SweepObservations(ctx, 30*time.Minute)
	if err != nil {
		t.Fatalf("SweepObservations: %v", err)
	}
	if n != 1 {
		t.Fatalf("SweepObservations removed = %d, want 1", n)
	}

	got, err := s.LastObservation(ctx, "ws", "alice")
	if err != nil {
		t.Fatalf("LastObservation: %v", err)
	}
	if got.Kind != "new" {
		t.Fatalf("surviving observation = %+v, want the newer one", got)
	}
}
