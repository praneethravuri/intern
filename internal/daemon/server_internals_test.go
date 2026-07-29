package daemon

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/praneethravuri/tether/internal/store"
)

func TestAuthenticate_SessionMismatchIsRejected(t *testing.T) {
	ts := newTestServer(t, nil)
	ctx := context.Background()

	if err := ts.store.Register(ctx, store.Agent{Workspace: "ws", Name: "alice", SessionID: "s1", Cwd: "/a"}, time.Time{}); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := ts.srv.authenticate(ctx, "ws", "alice", "s2"); err == nil {
		t.Fatal("authenticate with a mismatched session: want error, got nil")
	}
	if err := ts.srv.authenticate(ctx, "ws", "alice", "s1"); err != nil {
		t.Fatalf("authenticate with the matching session: %v", err)
	}
	if err := ts.srv.authenticate(ctx, "ws", "alice", ""); err != nil {
		t.Fatalf("authenticate with an empty claimed session: %v", err)
	}
	if err := ts.srv.authenticate(ctx, "ws", "ghost", "anything"); err != nil {
		t.Fatalf("authenticate against an unregistered agent: %v", err)
	}
}

func TestAuthenticate_StoreErrorPropagates(t *testing.T) {
	ts := newTestServer(t, nil)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if err := ts.srv.authenticate(canceled, "ws", "alice", "s1"); err == nil {
		t.Fatal("authenticate with a canceled context: want error, got nil")
	}
}

func TestTouch_LogsObserveAndHeartbeatFailuresWithoutPropagating(t *testing.T) {
	var buf bytes.Buffer
	ts := newTestServer(t, func(c *Config) { c.Logger = log.New(&buf, "", 0) })

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	ts.srv.touch(canceled, "ws", "alice", "register", "")

	if !strings.Contains(buf.String(), "touch:") {
		t.Fatalf("touch did not log its failures: %q", buf.String())
	}
}

func TestSweepOnce_LogsStoreFailuresWithoutPanicking(t *testing.T) {
	var buf bytes.Buffer
	ts := newTestServer(t, func(c *Config) { c.Logger = log.New(&buf, "", 0) })

	if err := ts.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	ts.srv.sweepOnce(context.Background())

	if !strings.Contains(buf.String(), "failed") {
		t.Fatalf("sweepOnce did not log the store failure: %q", buf.String())
	}
}

func TestSweepOnce_SweepsDeadMessagesAndOldObservations(t *testing.T) {
	var buf bytes.Buffer
	clk := newFakeClock()
	ts := newTestServer(t, func(c *Config) {
		c.Logger = log.New(&buf, "", 0)
		c.DeadAfter = time.Minute
	})
	ts.store.Now = clk.Now
	ctx := context.Background()

	if err := ts.store.Register(ctx, store.Agent{Workspace: "ws", Name: "alice", Cwd: "/a"}, time.Time{}); err != nil {
		t.Fatalf("register alice: %v", err)
	}
	if err := ts.store.Register(ctx, store.Agent{Workspace: "ws", Name: "bob", Cwd: "/b"}, time.Time{}); err != nil {
		t.Fatalf("register bob: %v", err)
	}
	if _, err := ts.store.Send(ctx, store.Message{FromName: "alice", FromWS: "ws", ToName: "bob", ToWS: "ws", Kind: store.KindNote, Body: "hi"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := ts.store.Observe(ctx, store.Observation{Workspace: "ws", Name: "alice", Kind: "tool"}); err != nil {
		t.Fatalf("observe: %v", err)
	}

	clk.advance(2 * time.Minute)
	ts.srv.sweepOnce(ctx)

	got := buf.String()
	if !strings.Contains(got, "swept 1 dead message") {
		t.Errorf("sweepOnce did not report sweeping the dead message: %q", got)
	}
	if !strings.Contains(got, "swept 1 observation") {
		t.Errorf("sweepOnce did not report sweeping the old observation: %q", got)
	}
}

func TestSweepDeadAgents_RemovesOnlyStaleAndDeadRows(t *testing.T) {
	var buf bytes.Buffer
	ts := newTestServer(t, func(c *Config) {
		c.Logger = log.New(&buf, "", 0)
		c.DeadAfter = time.Minute
	})
	ctx := context.Background()

	// sweepDeadAgents' cutoff is time.Now().Add(-DeadAfter), using the real
	// wall clock rather than the store's overridable one, so LastSeen has to
	// predate it by actually being written far in the past.
	ts.store.Now = func() time.Time { return time.Now().Add(-time.Hour) }

	// Dead: stale and its pid is not alive.
	if err := ts.store.Register(ctx, store.Agent{Workspace: "ws", Name: "gone", Cwd: "/g", PID: 1 << 30}, time.Time{}); err != nil {
		t.Fatalf("register gone: %v", err)
	}
	// Stale by time but still alive: must survive the sweep.
	if err := ts.store.Register(ctx, store.Agent{Workspace: "ws", Name: "alive", Cwd: "/a", PID: os.Getpid()}, time.Time{}); err != nil {
		t.Fatalf("register alive: %v", err)
	}

	ts.srv.sweepDeadAgents(ctx)

	if _, err := ts.store.GetAgent(ctx, "ws", "gone"); err == nil {
		t.Error("dead agent survived the sweep")
	}
	if _, err := ts.store.GetAgent(ctx, "ws", "alive"); err != nil {
		t.Errorf("live agent was swept away: %v", err)
	}
	if !strings.Contains(buf.String(), "swept 1 dead agent") {
		t.Errorf("sweepDeadAgents did not report the removal: %q", buf.String())
	}
}

func TestSweepDeadAgents_LogsListFailureWithoutPanicking(t *testing.T) {
	var buf bytes.Buffer
	ts := newTestServer(t, func(c *Config) { c.Logger = log.New(&buf, "", 0) })

	if err := ts.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	ts.srv.sweepDeadAgents(context.Background())

	if !strings.Contains(buf.String(), "sweep dead agents") {
		t.Fatalf("sweepDeadAgents did not log the list failure: %q", buf.String())
	}
}
