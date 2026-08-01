// Package store is intern's persistence layer: a SQLite-backed registry of
// live agents and a durable, ack-based mailbox.
//
// Reading an inbox never removes anything; a message leaves only via an
// explicit ack or the dead sweep.
package store

import (
	"context"
	"database/sql"
	_ "embed" // for //go:embed schema.sql
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	// modernc.org/sqlite is a cgo-free SQLite driver. It registers itself
	// under the name "sqlite" (not "sqlite3").
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// defaultMaxBodyBytes caps a single message body at 64 KiB.
const defaultMaxBodyBytes = 64 << 10

// Store owns the database handles. It is safe for concurrent use. The write
// pool is pinned to one connection so writes queue instead of fighting
// SQLITE_BUSY; reads use a separate, larger pool.
type Store struct {
	// Now supplies the current time; tests override it to skip past
	// staleness thresholds.
	Now func() time.Time

	// MaxBodyBytes is the largest accepted message body.
	MaxBodyBytes int

	// Logger receives diagnostics the Store cannot return as an error (a
	// rollback failure). Nil means silent.
	Logger *log.Logger

	w *sql.DB // writes: SetMaxOpenConns(1)
	r *sql.DB // reads: normal pool
}

// dsn builds the connection string; pragmas travel here because a PRAGMA via
// Exec would land on one arbitrary pooled connection.
func dsn(path string) string {
	return "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)"
}

// Open opens (creating if needed) the database at path and applies the schema.
// It is idempotent: reopening an existing database is a supported no-op.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: create db dir: %w", err)
		}
	}

	conn := dsn(path)

	w, err := sql.Open("sqlite", conn)
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	// One connection == one serialized write queue.
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)

	// Ping forces a real connection so a bad path fails here, not later.
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: ping writer: %w", err)
	}

	r, err := sql.Open("sqlite", conn)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: open reader: %w", err)
	}
	readers := runtime.NumCPU()
	if readers < 4 {
		readers = 4
	}
	r.SetMaxOpenConns(readers)
	r.SetMaxIdleConns(readers)
	r.SetConnMaxLifetime(0)

	if err := r.PingContext(ctx); err != nil {
		_ = w.Close()
		_ = r.Close()
		return nil, fmt.Errorf("store: ping reader: %w", err)
	}

	s := &Store{
		Now:          time.Now,
		MaxBodyBytes: defaultMaxBodyBytes,
		w:            w,
		r:            r,
	}
	if err := s.migrate(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// Close releases both pools. It is safe to call on a partially built Store.
func (s *Store) Close() error {
	var errs []error
	if s.w != nil {
		if err := s.w.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.r != nil {
		if err := s.r.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// currentSchemaVersion is the highest schema version this binary
// understands. Open refuses a database stamped with a newer one rather than
// silently continuing against a shape it doesn't know.
const currentSchemaVersion = 5

// migrate applies the full schema in one statement (modernc.org/sqlite
// executes a multi-statement script directly, so no hand-rolled splitter is
// needed), then reconciles a database created under an older schema, all in
// one transaction: a failure partway through leaves the database exactly as
// it was, not half-migrated with no record of how far it got. Safe to
// re-run on every Open.
func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	defer s.rollback(tx)

	version, err := readSchemaVersion(ctx, tx)
	if err != nil {
		return fmt.Errorf("store: migrate: read schema version: %w", err)
	}
	if version > currentSchemaVersion {
		return fmt.Errorf(
			"store: database schema v%d is newer than this binary understands (v%d) -- upgrade intern",
			version, currentSchemaVersion)
	}

	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("store: migrate: apply schema: %w", err)
	}
	if err := s.migrateV1ToV2(ctx, tx); err != nil {
		return err
	}
	if err := s.migrateV2ToV3(ctx, tx); err != nil {
		return err
	}
	if err := s.migrateV3ToV4(ctx, tx); err != nil {
		return err
	}
	if err := s.migrateV4ToV5(ctx, tx); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, qSetSchemaVersion, currentSchemaVersion); err != nil {
		return fmt.Errorf("store: migrate: record schema version: %w", err)
	}
	return tx.Commit()
}

// readSchemaVersion returns 0 for a database with no meta row yet -- a
// brand-new database (the common case: meta doesn't exist until schemaSQL
// runs, right after this check), or one from before schema_version existed
// at all -- which is exactly "run every migration from scratch".
func readSchemaVersion(ctx context.Context, tx *sql.Tx) (int, error) {
	var metaExists int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'`,
	).Scan(&metaExists); err != nil {
		return 0, err
	}
	if metaExists == 0 {
		return 0, nil
	}

	var raw string
	err := tx.QueryRowContext(ctx, qGetSchemaVersion).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("malformed schema_version %q: %w", raw, err)
	}
	return n, nil
}

// migrateV1ToV2 adds v2's new columns and drops its retired ones; a no-op on
// a database already at v2.
func (s *Store) migrateV1ToV2(ctx context.Context, tx *sql.Tx) error {
	for _, add := range []struct{ col, ddl string }{
		{"pid_start", "ALTER TABLE agents ADD COLUMN pid_start INTEGER NOT NULL DEFAULT 0"},
		{"dropped", "ALTER TABLE agents ADD COLUMN dropped INTEGER NOT NULL DEFAULT 0"},
	} {
		has, err := hasColumn(ctx, tx, "agents", add.col)
		if err != nil {
			return fmt.Errorf("store: check agents.%s: %w", add.col, err)
		}
		if !has {
			if _, err := tx.ExecContext(ctx, add.ddl); err != nil {
				return fmt.Errorf("store: add agents.%s: %w", add.col, err)
			}
		}
	}

	for _, drop := range []struct{ table, col string }{
		{"agents", "notifier"},
		{"agents", "tier"},
		{"agents", "state"},
		{"agents", "last_tool"},
		{"messages", "attachments"},
	} {
		has, err := hasColumn(ctx, tx, drop.table, drop.col)
		if err != nil {
			return fmt.Errorf("store: check %s.%s: %w", drop.table, drop.col, err)
		}
		if !has {
			continue
		}
		// A leftover column with a harmless default isn't worth failing Open over.
		ddl := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", drop.table, drop.col)
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			if s.Logger != nil {
				s.Logger.Printf("store: drop %s.%s: %v (leaving column in place)", drop.table, drop.col, err)
			}
		}
	}

	if _, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS idx_thread"); err != nil {
		return fmt.Errorf("store: drop idx_thread: %w", err)
	}
	return nil
}

// migrateV2ToV3 adds v3's agents columns and drops the observations table,
// whose one live datum (last command kind) now lives on the agent row
// itself. A no-op on a database already at v3.
func (s *Store) migrateV2ToV3(ctx context.Context, tx *sql.Tx) error {
	for _, add := range []struct{ col, ddl string }{
		{"last_kind", "ALTER TABLE agents ADD COLUMN last_kind TEXT NOT NULL DEFAULT ''"},
		{"last_note", "ALTER TABLE agents ADD COLUMN last_note TEXT NOT NULL DEFAULT ''"},
	} {
		has, err := hasColumn(ctx, tx, "agents", add.col)
		if err != nil {
			return fmt.Errorf("store: check agents.%s: %w", add.col, err)
		}
		if !has {
			if _, err := tx.ExecContext(ctx, add.ddl); err != nil {
				return fmt.Errorf("store: add agents.%s: %w", add.col, err)
			}
		}
	}

	if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS observations"); err != nil {
		return fmt.Errorf("store: drop observations: %w", err)
	}
	return nil
}

// migrateV3ToV4 adds the (workspace, session_id) uniqueness index --
// deduping any pre-existing collision first, since CREATE UNIQUE INDEX
// fails outright over one -- and widens idx_inbox to cover dead, plus an
// index on created_at, so the two 5-minute sweeps and every inbox/pending
// lookup stop scanning rows they immediately discard. A no-op on a database
// already at v4.
func (s *Store) migrateV3ToV4(ctx context.Context, tx *sql.Tx) error {
	// Keep the highest rowid (the most recently inserted row) per colliding
	// (workspace, session_id) pair; a real collision is a pre-existing bug
	// this index is closing, not an expected case.
	if _, err := tx.ExecContext(ctx, `
DELETE FROM agents
WHERE session_id <> '' AND rowid NOT IN (
    SELECT MAX(rowid) FROM agents WHERE session_id <> '' GROUP BY workspace, session_id
)`); err != nil {
		return fmt.Errorf("store: dedupe agent sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_session ON agents (workspace, session_id) WHERE session_id <> ''`,
	); err != nil {
		return fmt.Errorf("store: create idx_agents_session: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS idx_inbox"); err != nil {
		return fmt.Errorf("store: drop idx_inbox: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"CREATE INDEX IF NOT EXISTS idx_inbox ON messages (to_ws, to_name, acked_at, dead, id)",
	); err != nil {
		return fmt.Errorf("store: recreate idx_inbox: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages (created_at)",
	); err != nil {
		return fmt.Errorf("store: create idx_messages_created_at: %w", err)
	}
	return nil
}

// migrateV4ToV5 adds the claims table (see schema.sql), coordinating
// exclusive access to a caller-supplied key within a workspace. schemaSQL
// already creates it for a fresh database in the same transaction; this is
// the explicit upgrade path for a database opened at v4, and a no-op on one
// already at v5.
func (s *Store) migrateV4ToV5(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS claims (
    workspace       TEXT    NOT NULL,
    key             TEXT    NOT NULL,
    owner_pid       INTEGER NOT NULL DEFAULT 0,
    owner_pid_start INTEGER NOT NULL DEFAULT 0,
    lease_id        TEXT    NOT NULL,
    lease_holder    TEXT    NOT NULL DEFAULT '',
    leased_at       INTEGER NOT NULL,
    expires_at      INTEGER NOT NULL,
    PRIMARY KEY (workspace, key)
) STRICT`); err != nil {
		return fmt.Errorf("store: create claims: %w", err)
	}
	return nil
}

// hasColumn reports whether table has a column named col, via PRAGMA
// table_info. table and col must be compile-time constants from this
// package -- PRAGMA does not accept bound parameters and no SQL here is
// ever built from a runtime string.
func hasColumn(ctx context.Context, tx *sql.Tx, table, col string) (bool, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()

	// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk.
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

// nowMS is the current store time in Unix milliseconds.
func (s *Store) nowMS() int64 {
	return s.now().UnixMilli()
}

// now reads Now defensively so a zero-valued Store still works.
func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// maxBody returns the effective body cap.
func (s *Store) maxBody() int {
	if s.MaxBodyBytes > 0 {
		return s.MaxBodyBytes
	}
	return defaultMaxBodyBytes
}

// rollbacker is satisfied by *sql.Tx, and by a fake in tests.
type rollbacker interface {
	Rollback() error
}

// rollback discards a transaction, logging anything but the expected
// already-finished case.
func (s *Store) rollback(tx rollbacker) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		if s.Logger != nil {
			s.Logger.Printf("store: rollback failed: %v", err)
		}
	}
}
