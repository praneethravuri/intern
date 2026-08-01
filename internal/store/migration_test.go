package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// schemaV1 is intern's pre-v2 schema, reconstructed by hand for migration tests.
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
	if _, err := raw.ExecContext(ctx, schemaV1); err != nil {
		t.Fatalf("apply v1 schema: %v", err)
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
	if version != "5" {
		t.Errorf("schema_version = %q, want %q", version, "5")
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

// schemaV2 is intern's pre-v3 schema, reconstructed by hand for migration
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
	if _, err := raw.ExecContext(ctx, schemaV2); err != nil {
		t.Fatalf("apply v2 schema: %v", err)
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
	if version != "5" {
		t.Errorf("schema_version = %q, want %q", version, "5")
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
		if version != "5" {
			t.Fatalf("Open #%d: schema_version = %q, want %q", i+1, version, "5")
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

// TestOpen_RefusesNewerSchemaVersion is 6.13's version-check half: an older
// binary opening a database a newer one already migrated must refuse
// outright, not silently continue against a shape it doesn't know.
func TestOpen_RefusesNewerSchemaVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT`); err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('schema_version', '999')`); err != nil {
		t.Fatalf("seed future version: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	if _, err := Open(ctx, path); err == nil {
		t.Fatal("Open on a database stamped with a future schema version: want error, got nil")
	} else if !strings.Contains(err.Error(), "999") {
		t.Errorf("error %q does not name the offending version", err)
	}

	// The refusal happens before any DDL runs, inside one transaction that
	// then rolls back -- nothing else should exist in this database.
	raw2, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("reopen raw db: %v", err)
	}
	defer func() { _ = raw2.Close() }()
	var n int
	if err := raw2.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agents'`).Scan(&n); err != nil {
		t.Fatalf("check agents table: %v", err)
	}
	if n != 0 {
		t.Error("agents table exists even though the migration was refused -- it did not roll back cleanly")
	}
}

// TestMigrateV3ToV4_DedupesCollidingSessionsBeforeIndexing is 6.15: the new
// unique index on (workspace, session_id) would fail outright over a
// pre-existing collision, so migration must dedupe first. The row with the
// higher rowid (the one inserted more recently) survives.
func TestMigrateV3ToV4_DedupesCollidingSessionsBeforeIndexing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "collide.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.ExecContext(ctx, schemaV2); err != nil {
		t.Fatalf("apply v2 schema: %v", err)
	}
	// Two different names, same workspace and session_id -- the bug 6.15
	// describes: qFindNameBySession could already return either one.
	if _, err := raw.ExecContext(ctx, `
INSERT INTO agents (workspace, name, harness, session_id, cwd, pid, pid_start, dropped, registered_at, last_seen)
VALUES ('ws', 'stale-name', 'claude-code', 'sess-1', '/a', 1, 0, 0, 1000, 1000)`); err != nil {
		t.Fatalf("insert first colliding row: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
INSERT INTO agents (workspace, name, harness, session_id, cwd, pid, pid_start, dropped, registered_at, last_seen)
VALUES ('ws', 'fresh-name', 'claude-code', 'sess-1', '/b', 2, 0, 0, 2000, 2000)`); err != nil {
		t.Fatalf("insert second colliding row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (should dedupe and succeed): %v", err)
	}
	defer func() { _ = s.Close() }()

	if !hasIndex(t, s.w, "agents", "idx_agents_session") {
		t.Error("idx_agents_session was not created")
	}

	if _, err := s.GetAgent(ctx, "ws", "stale-name"); !errors.Is(err, ErrNoSuchAgent) {
		t.Errorf("GetAgent(stale-name) = %v, want ErrNoSuchAgent (the lower-rowid duplicate should be gone)", err)
	}
	if _, err := s.GetAgent(ctx, "ws", "fresh-name"); err != nil {
		t.Errorf("GetAgent(fresh-name) = %v, want the surviving row", err)
	}

	// The index actually enforces uniqueness going forward, not just exists.
	if err := s.Register(ctx, Agent{Workspace: "ws", Name: "third-name", SessionID: "sess-1", Cwd: "/c"},
		time.Time{}); err == nil {
		t.Error("Register with a session_id already held under a different name: want error, got nil")
	}
}

// TestMigrateV3ToV4_WidensIdxInbox proves idx_inbox now covers dead, and
// idx_messages_created_at exists for the two sweep queries.
func TestMigrateV3ToV4_WidensIdxInbox(t *testing.T) {
	s := newStore(t)
	if !hasIndex(t, s.w, "messages", "idx_inbox") {
		t.Error("idx_inbox is missing")
	}
	if !hasIndex(t, s.w, "messages", "idx_messages_created_at") {
		t.Error("idx_messages_created_at was not created")
	}
}

// schemaV4 is intern's pre-v5 schema, reconstructed by hand for migration
// tests -- the shape the database had before the claims table existed.
const schemaV4 = `
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
    last_kind     TEXT    NOT NULL DEFAULT '',
    last_note     TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (workspace, name)
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_session ON agents (workspace, session_id) WHERE session_id <> '';

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
CREATE INDEX IF NOT EXISTS idx_inbox ON messages (to_ws, to_name, acked_at, dead, id);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages (created_at);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
`

// TestMigrateV4ToV5_AddsClaimsTable opens a database stamped at v4, with no
// claims table, and verifies Open brings it fully up to v5: the table
// exists with the expected shape, the version is recorded, and a claim can
// actually round-trip through it.
func TestMigrateV4ToV5_AddsClaimsTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v4.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw v4 db: %v", err)
	}
	if _, err := raw.ExecContext(ctx, schemaV4); err != nil {
		t.Fatalf("apply v4 schema: %v", err)
	}
	if _, err := raw.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '4')`); err != nil {
		t.Fatalf("seed v4 version: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (should migrate v4 -> v5): %v", err)
	}
	defer func() { _ = s.Close() }()

	cols := tableInfoCols(t, s.w, "claims")
	for _, want := range []string{
		"workspace", "key", "owner_pid", "owner_pid_start",
		"lease_id", "lease_holder", "leased_at", "expires_at",
	} {
		if !contains(cols, want) {
			t.Errorf("claims columns = %v, missing %q", cols, want)
		}
	}

	var version string
	if err := s.w.QueryRowContext(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "5" {
		t.Fatalf("schema_version = %q, want %q", version, "5")
	}

	if _, err := s.Claim(ctx, "ws", "src/main.go", 111, 222, "alice", time.Hour); err != nil {
		t.Fatalf("Claim on the migrated database: %v", err)
	}
}
