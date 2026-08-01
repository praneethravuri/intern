package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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

	_, err := Open(context.Background(), filepath.Join(blocker, "sub", "intern.db"))
	if err == nil {
		t.Fatal("Open with a file where a parent directory belongs: want error, got nil")
	}
}

func TestOpenRejectsUnopenableFile(t *testing.T) {
	dir := t.TempDir()
	asDir := filepath.Join(dir, "intern.db")
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
	ctx := context.Background()
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	has, err := hasColumn(ctx, tx, "no_such_table", "no_such_col")
	if err != nil {
		t.Fatalf("hasColumn on a missing table: %v", err)
	}
	if has {
		t.Fatal("hasColumn on a missing table = true, want false")
	}
}
