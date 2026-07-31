package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// canceled returns a context that is already canceled, used to force a
// deterministic error out of any *Context call without a mock driver.
func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestAgentMethodsReportCanceledContext(t *testing.T) {
	s := newStore(t)
	mustRegister(t, s, Agent{Workspace: "ws", Name: "alice", Cwd: "/a"})

	ctx := canceled()

	if err := s.Register(ctx, Agent{Workspace: "ws", Name: "bob", Cwd: "/b"}, s.now()); err == nil {
		t.Error("Register with canceled context: want error, got nil")
	}
	if _, err := s.Heartbeat(ctx, "ws", "alice", "register", ""); err == nil {
		t.Error("Heartbeat with canceled context: want error, got nil")
	}
	if _, err := s.GetAgent(ctx, "ws", "alice"); err == nil {
		t.Error("GetAgent with canceled context: want error, got nil")
	}
	if _, err := s.ListAgents(ctx, "ws", 0); err == nil {
		t.Error("ListAgents with canceled context: want error, got nil")
	}
	if _, err := s.DeleteAgents(ctx, []AgentKey{{Workspace: "ws", Name: "alice"}}); err == nil {
		t.Error("DeleteAgents with canceled context: want error, got nil")
	}
}

func TestMessageMethodsReportCanceledContext(t *testing.T) {
	s := newStore(t)
	pair(t, s)
	id := mustSend(t, s, note("hello"))

	ctx := canceled()

	if _, err := s.Send(ctx, note("hi")); err == nil {
		t.Error("Send with canceled context: want error, got nil")
	}
	if _, err := s.Inbox(ctx, "ws", "bob", 10); err == nil {
		t.Error("Inbox with canceled context: want error, got nil")
	}
	if _, err := s.Replay(ctx, "ws", "bob", 10, 0); err == nil {
		t.Error("Replay with canceled context: want error, got nil")
	}
	if _, err := s.Ack(ctx, "ws", "bob", []string{id}); err == nil {
		t.Error("Ack with canceled context: want error, got nil")
	}
	if _, _, err := s.Drain(ctx, "ws", "bob", 10); err == nil {
		t.Error("Drain with canceled context: want error, got nil")
	}
	if _, err := s.PendingCount(ctx, "ws", "bob"); err == nil {
		t.Error("PendingCount with canceled context: want error, got nil")
	}
	if _, err := s.SweepDead(ctx, 0); err == nil {
		t.Error("SweepDead with canceled context: want error, got nil")
	}
}

func TestPendingByWorkspaceReportsCanceledContext(t *testing.T) {
	s := newStore(t)
	mustRegister(t, s, Agent{Workspace: "ws", Name: "alice", Cwd: "/a"})

	ctx := canceled()

	if _, err := s.PendingByWorkspace(ctx, "ws"); err == nil {
		t.Error("PendingByWorkspace with canceled context: want error, got nil")
	}
}

func TestSendReplyToUnknownMessage(t *testing.T) {
	s := newStore(t)
	pair(t, s)

	m := note("re: what?")
	m.ReplyTo = "01DOESNOTEXIST"
	if _, err := s.Send(context.Background(), m); !errors.Is(err, ErrNoSuchMessage) {
		t.Fatalf("Send(reply to unknown) = %v, want ErrNoSuchMessage", err)
	}
}

func TestSendInvalidKind(t *testing.T) {
	s := newStore(t)
	pair(t, s)

	m := note("hi")
	m.Kind = "sonnet"
	if _, err := s.Send(context.Background(), m); err == nil {
		t.Fatal("Send with an invalid kind: want error, got nil")
	}
}

func TestAckUnknownIDsIsANoOp(t *testing.T) {
	s := newStore(t)
	n, err := s.Ack(context.Background(), "ws", "bob", nil)
	if err != nil || n != 0 {
		t.Fatalf("Ack(nil ids) = (%d, %v), want (0, nil)", n, err)
	}
}

func TestDrainWithNothingPending(t *testing.T) {
	s := newStore(t)
	pair(t, s)

	msgs, dropped, err := s.Drain(context.Background(), "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Drain(empty inbox): %v", err)
	}
	if len(msgs) != 0 || dropped != 0 {
		t.Fatalf("Drain(empty inbox) = (%v, %d), want (nil, 0)", msgs, dropped)
	}
}

// ---------------------------------------------------------------- models ---

func TestMessageFrom(t *testing.T) {
	m := Message{FromName: "alice", FromWS: "ws"}
	if got := m.From(); got != "alice@ws" {
		t.Errorf("From() = %q, want %q", got, "alice@ws")
	}
}

func TestValidKindRejectsUnknown(t *testing.T) {
	for _, k := range []string{"", "sonnet", "NOTE", " note"} {
		if ValidKind(k) {
			t.Errorf("ValidKind(%q) = true, want false", k)
		}
	}
}

// ------------------------------------------------------------------ open ---

func TestOpenEmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("Open(\"\"): want error, got nil")
	}
}

func TestOpenParentIsAFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}

	_, err := Open(context.Background(), filepath.Join(blocker, "sub", "tether.db"))
	if err == nil {
		t.Fatal("Open with a file where a parent directory belongs: want error, got nil")
	}
}

func TestOpenRejectsUnopenableFile(t *testing.T) {
	dir := t.TempDir()
	asDir := filepath.Join(dir, "tether.db")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatalf("seed directory standing in for the db file: %v", err)
	}

	if _, err := Open(context.Background(), asDir); err == nil {
		t.Fatal("Open against a directory: want error, got nil")
	}
}

func TestCloseOnPartiallyBuiltStore(t *testing.T) {
	var s Store
	if err := s.Close(); err != nil {
		t.Fatalf("Close on zero-value Store: %v", err)
	}

	path := filepath.Join(t.TempDir(), "half.db")
	conn := dsn(path)
	w, err := sql.Open("sqlite", conn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	partial := &Store{w: w}
	if err := partial.Close(); err != nil {
		t.Fatalf("Close with only the writer set: %v", err)
	}
}

func TestValidationRejectsEmptyAddresses(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.Register(ctx, Agent{Workspace: "", Name: "alice", Cwd: "/a"}, s.now()); !errors.Is(err, ErrBadAddress) {
		t.Errorf("Register(empty workspace) = %v, want ErrBadAddress", err)
	}
	if err := s.Register(ctx, Agent{Workspace: "ws", Name: "", Cwd: "/a"}, s.now()); !errors.Is(err, ErrBadAddress) {
		t.Errorf("Register(empty name) = %v, want ErrBadAddress", err)
	}
	if _, err := s.Heartbeat(ctx, "", "alice", "register", ""); !errors.Is(err, ErrBadAddress) {
		t.Errorf("Heartbeat(empty workspace) = %v, want ErrBadAddress", err)
	}
}

func TestStoreDefaultsOnZeroValue(t *testing.T) {
	var s Store
	if s.now().IsZero() {
		t.Error("zero-value Store.now() returned the zero time, want the real clock")
	}
	if got := s.maxBody(); got != defaultMaxBodyBytes {
		t.Errorf("zero-value Store.maxBody() = %d, want %d", got, defaultMaxBodyBytes)
	}
}

func TestHasColumnOnMissingTable(t *testing.T) {
	s := newStore(t)
	has, err := s.hasColumn(context.Background(), "no_such_table", "no_such_col")
	if err != nil {
		t.Fatalf("hasColumn on a missing table: %v", err)
	}
	if has {
		t.Fatal("hasColumn on a missing table = true, want false")
	}
}
