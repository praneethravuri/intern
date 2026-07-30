package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// schemaV1 is tether's pre-v2 schema, reconstructed by hand for migration tests.
const schemaV1 = `
CREATE TABLE IF NOT EXISTS agents (
    workspace     TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    harness       TEXT    NOT NULL DEFAULT 'unknown',
    session_id    TEXT    NOT NULL DEFAULT '',
    cwd           TEXT    NOT NULL,
    pid           INTEGER NOT NULL DEFAULT 0,
    notifier      TEXT    NOT NULL DEFAULT 'universal',
    tier          TEXT    NOT NULL DEFAULT 'universal'
                  CHECK (tier IN ('push','turn','tool','universal')),
    state         TEXT    NOT NULL DEFAULT 'idle'
                  CHECK (state IN ('idle','working','waiting','gone')),
    last_tool     TEXT    NOT NULL DEFAULT '',
    registered_at INTEGER NOT NULL,
    last_seen     INTEGER NOT NULL,
    PRIMARY KEY (workspace, name)
) STRICT;

CREATE TABLE IF NOT EXISTS messages (
    id           TEXT    PRIMARY KEY,
    thread_id    TEXT    NOT NULL,
    reply_to     TEXT    NOT NULL DEFAULT '',
    from_name    TEXT    NOT NULL,
    from_ws      TEXT    NOT NULL,
    to_name      TEXT    NOT NULL,
    to_ws        TEXT    NOT NULL,
    kind         TEXT    NOT NULL DEFAULT 'note'
                 CHECK (kind IN ('note','handoff','question','answer')),
    body         TEXT    NOT NULL,
    attachments  TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    delivered_at INTEGER,
    acked_at     INTEGER,
    dead         INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX IF NOT EXISTS idx_inbox
    ON messages (to_ws, to_name, acked_at, id);

CREATE INDEX IF NOT EXISTS idx_thread
    ON messages (thread_id, id);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
`

// tableInfoCols returns the column names PRAGMA table_info(table) reports,
// in declaration order.
func tableInfoCols(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	var cols []string
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, typ        string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		cols = append(cols, name)
	}
	return cols
}

func hasIndex(t *testing.T, db *sql.DB, table, index string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA index_list(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			seq     int
			name    string
			unique  int
			origin  string
			partial int
		)
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index_list(%s): %v", table, err)
		}
		if name == index {
			return true
		}
	}
	return false
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestMigrateV1ToV2(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v1.db")

	// Build the v1 database directly, bypassing this package's migrate.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw v1 db: %v", err)
	}
	for _, stmt := range splitStatements(schemaV1) {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("apply v1 schema %.60q: %v", stmt, err)
		}
	}
	if _, err := raw.ExecContext(ctx, `
INSERT INTO agents (workspace, name, harness, session_id, cwd, pid, notifier, tier, state, last_tool, registered_at, last_seen)
VALUES ('ws', 'alice', 'claude-code', 'sess-1', '/repo', 42, 'universal', 'universal', 'working', 'Bash', 1000, 2000)`); err != nil {
		t.Fatalf("insert v1 agent: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
INSERT INTO messages (id, thread_id, reply_to, from_name, from_ws, to_name, to_ws, kind, body, attachments, created_at)
VALUES ('01AAAA', '01AAAA', '', 'bob', 'ws', 'alice', 'ws', 'note', 'hello', '', 1500)`); err != nil {
		t.Fatalf("insert v1 message: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw v1 db: %v", err)
	}

	// Now open it through the package under test.
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v1 db: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Opening a v1 database today runs the full v1->v2->v3 chain in one Open,
	// so both migrations' columns must be present.
	agentCols := tableInfoCols(t, s.w, "agents")
	for _, want := range []string{"pid_start", "dropped", "last_kind", "last_note"} {
		if !contains(agentCols, want) {
			t.Errorf("agents columns %v missing %q", agentCols, want)
		}
	}
	for _, gone := range []string{"notifier", "tier", "state", "last_tool"} {
		if contains(agentCols, gone) {
			t.Errorf("agents columns %v still have %q, want dropped", agentCols, gone)
		}
	}

	msgCols := tableInfoCols(t, s.w, "messages")
	if contains(msgCols, "attachments") {
		t.Errorf("messages columns %v still have attachments, want dropped", msgCols)
	}

	if hasIndex(t, s.w, "messages", "idx_thread") {
		t.Error("idx_thread still present, want dropped")
	}

	if cols := tableInfoCols(t, s.w, "observations"); len(cols) != 0 {
		t.Errorf("observations table still present, columns = %v, want dropped", cols)
	}

	var version string
	if err := s.w.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "3" {
		t.Errorf("schema_version = %q, want %q", version, "3")
	}

	// The pre-existing rows must have survived the migration, defaults and
	// all.
	a, err := s.GetAgent(ctx, "ws", "alice")
	if err != nil {
		t.Fatalf("GetAgent(alice) after migration: %v", err)
	}
	if a.Harness != "claude-code" || a.SessionID != "sess-1" || a.Cwd != "/repo" || a.PID != 42 {
		t.Errorf("migrated agent = %+v, fields lost", a)
	}
	if a.PIDStart != 0 || a.Dropped != 0 {
		t.Errorf("migrated agent new columns = %+v, want zero defaults", a)
	}

	msgs, err := s.Inbox(ctx, "ws", "alice", 10)
	if err != nil {
		t.Fatalf("Inbox(alice) after migration: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "hello" || msgs[0].FromName != "bob" {
		t.Fatalf("migrated inbox = %+v, want the one pre-existing message", msgs)
	}
}

// schemaV2 is tether's pre-v3 schema, reconstructed by hand for migration
// tests -- the shape schema.sql had before observations was dropped.
const schemaV2 = `
CREATE TABLE IF NOT EXISTS agents (
    workspace     TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    harness       TEXT    NOT NULL DEFAULT 'unknown',
    session_id    TEXT    NOT NULL DEFAULT '',
    cwd           TEXT    NOT NULL,
    pid           INTEGER NOT NULL DEFAULT 0,
    pid_start     INTEGER NOT NULL DEFAULT 0,
    dropped       INTEGER NOT NULL DEFAULT 0,
    registered_at INTEGER NOT NULL,
    last_seen     INTEGER NOT NULL,
    PRIMARY KEY (workspace, name)
) STRICT;

CREATE TABLE IF NOT EXISTS messages (
    id           TEXT    PRIMARY KEY,
    thread_id    TEXT    NOT NULL,
    reply_to     TEXT    NOT NULL DEFAULT '',
    from_name    TEXT    NOT NULL,
    from_ws      TEXT    NOT NULL,
    to_name      TEXT    NOT NULL,
    to_ws        TEXT    NOT NULL,
    kind         TEXT    NOT NULL DEFAULT 'note'
                 CHECK (kind IN ('note','handoff','question','answer')),
    body         TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    delivered_at INTEGER,
    acked_at     INTEGER,
    dead         INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX IF NOT EXISTS idx_inbox
    ON messages (to_ws, to_name, acked_at, id);

CREATE TABLE IF NOT EXISTS observations (
    id        INTEGER PRIMARY KEY,
    workspace TEXT    NOT NULL,
    name      TEXT    NOT NULL,
    kind      TEXT    NOT NULL,
    detail    TEXT    NOT NULL DEFAULT '',
    at        INTEGER NOT NULL
) STRICT;
CREATE INDEX IF NOT EXISTS idx_obs_latest ON observations (workspace, name, id DESC);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
`

func TestMigrateV2ToV3(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v2.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw v2 db: %v", err)
	}
	for _, stmt := range splitStatements(schemaV2) {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("apply v2 schema %.60q: %v", stmt, err)
		}
	}
	if _, err := raw.ExecContext(ctx, `
INSERT INTO agents (workspace, name, harness, session_id, cwd, pid, pid_start, dropped, registered_at, last_seen)
VALUES ('ws', 'alice', 'claude-code', 'sess-1', '/repo', 42, 0, 0, 1000, 2000)`); err != nil {
		t.Fatalf("insert v2 agent: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
INSERT INTO messages (id, thread_id, reply_to, from_name, from_ws, to_name, to_ws, kind, body, created_at)
VALUES ('01AAAA', '01AAAA', '', 'bob', 'ws', 'alice', 'ws', 'note', 'hello', 1500)`); err != nil {
		t.Fatalf("insert v2 message: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
INSERT INTO observations (workspace, name, kind, detail, at)
VALUES ('ws', 'alice', 'send', 'bob@ws', 1800)`); err != nil {
		t.Fatalf("insert v2 observation: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw v2 db: %v", err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v2 db: %v", err)
	}
	defer func() { _ = s.Close() }()

	agentCols := tableInfoCols(t, s.w, "agents")
	for _, want := range []string{"last_kind", "last_note"} {
		if !contains(agentCols, want) {
			t.Errorf("agents columns %v missing %q", agentCols, want)
		}
	}

	if cols := tableInfoCols(t, s.w, "observations"); len(cols) != 0 {
		t.Errorf("observations table still present, columns = %v, want dropped", cols)
	}

	var version string
	if err := s.w.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "3" {
		t.Errorf("schema_version = %q, want %q", version, "3")
	}

	a, err := s.GetAgent(ctx, "ws", "alice")
	if err != nil {
		t.Fatalf("GetAgent(alice) after migration: %v", err)
	}
	if a.Harness != "claude-code" || a.SessionID != "sess-1" || a.Cwd != "/repo" || a.PID != 42 {
		t.Errorf("migrated agent = %+v, fields lost", a)
	}
	if a.LastKind != "" || a.LastNote != "" {
		t.Errorf("migrated agent new columns = %+v, want zero defaults", a)
	}

	msgs, err := s.Inbox(ctx, "ws", "alice", 10)
	if err != nil {
		t.Fatalf("Inbox(alice) after migration: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "hello" || msgs[0].FromName != "bob" {
		t.Fatalf("migrated inbox = %+v, want the one pre-existing message", msgs)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reopen.db")

	for i := 0; i < 3; i++ {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		cols := tableInfoCols(t, s.w, "agents")
		if !contains(cols, "pid_start") || !contains(cols, "dropped") || !contains(cols, "last_kind") || !contains(cols, "last_note") {
			t.Fatalf("Open #%d: agents columns = %v, missing v2/v3 columns", i+1, cols)
		}
		var version string
		if err := s.w.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
			t.Fatalf("Open #%d: read schema_version: %v", i+1, err)
		}
		if version != "3" {
			t.Fatalf("Open #%d: schema_version = %q, want %q", i+1, version, "3")
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
}

func TestRegisterStaleCutoffInFuture(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now

	incumbent := Agent{Workspace: "ws", Name: "alice", SessionID: "sess-1", Cwd: "/a"}
	if err := s.Register(ctx, incumbent, clk.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("first Register: %v", err)
	}

	intruder := Agent{Workspace: "ws", Name: "alice", SessionID: "sess-2", Cwd: "/b"}

	// A cutoff in the past (today's normal behaviour) must still fail: the
	// incumbent's last_seen is not before it.
	if err := s.Register(ctx, intruder, clk.Now().Add(-time.Minute)); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("Register with past cutoff = %v, want ErrNameTaken", err)
	}

	// A cutoff in the future is always after the incumbent's last_seen, so
	// the claim succeeds immediately without waiting out any window.
	if err := s.Register(ctx, intruder, clk.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Register with future cutoff: %v", err)
	}
	got, err := s.GetAgent(ctx, "ws", "alice")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.SessionID != "sess-2" {
		t.Errorf("future cutoff did not force takeover: %+v", got)
	}
}
