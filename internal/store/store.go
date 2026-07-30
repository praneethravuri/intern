// Package store is tether's persistence layer: a SQLite-backed registry of
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
	"strings"
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
		"&_pragma=foreign_keys(1)" +
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

// migrate applies schema.sql statement by statement, then reconciles a
// database created under an older schema. Safe to re-run on every Open.
func (s *Store) migrate(ctx context.Context) error {
	for _, stmt := range splitStatements(schemaSQL) {
		if _, err := s.w.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: migrate %.60q: %w", stmt, err)
		}
	}

	if err := s.migrateV1ToV2(ctx); err != nil {
		return err
	}

	if _, err := s.w.ExecContext(ctx, qSetSchemaVersion); err != nil {
		return fmt.Errorf("store: record schema version: %w", err)
	}
	return nil
}

// migrateV1ToV2 adds v2's new columns and drops its retired ones; a no-op on
// a database already at v2.
func (s *Store) migrateV1ToV2(ctx context.Context) error {
	for _, add := range []struct{ col, ddl string }{
		{"pid_start", "ALTER TABLE agents ADD COLUMN pid_start INTEGER NOT NULL DEFAULT 0"},
		{"dropped", "ALTER TABLE agents ADD COLUMN dropped INTEGER NOT NULL DEFAULT 0"},
	} {
		has, err := s.hasColumn(ctx, "agents", add.col)
		if err != nil {
			return fmt.Errorf("store: check agents.%s: %w", add.col, err)
		}
		if !has {
			if _, err := s.w.ExecContext(ctx, add.ddl); err != nil {
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
		has, err := s.hasColumn(ctx, drop.table, drop.col)
		if err != nil {
			return fmt.Errorf("store: check %s.%s: %w", drop.table, drop.col, err)
		}
		if !has {
			continue
		}
		// A leftover column with a harmless default isn't worth failing Open over.
		ddl := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", drop.table, drop.col)
		if _, err := s.w.ExecContext(ctx, ddl); err != nil {
			if s.Logger != nil {
				s.Logger.Printf("store: drop %s.%s: %v (leaving column in place)", drop.table, drop.col, err)
			}
		}
	}

	if _, err := s.w.ExecContext(ctx, "DROP INDEX IF EXISTS idx_thread"); err != nil {
		return fmt.Errorf("store: drop idx_thread: %w", err)
	}
	return nil
}

// hasColumn reports whether table has a column named col, via PRAGMA
// table_info. table and col must be compile-time constants from this
// package -- PRAGMA does not accept bound parameters and no SQL here is
// ever built from a runtime string.
func (s *Store) hasColumn(ctx context.Context, table, col string) (bool, error) {
	rows, err := s.w.QueryContext(ctx, "PRAGMA table_info("+table+")")
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

// splitStatements chops a SQL script into individual executable statements,
// dropping "--" line comments and blank chunks.
func splitStatements(script string) []string {
	var out []string
	for _, chunk := range strings.Split(script, ";") {
		var lines []string
		for _, line := range strings.Split(chunk, "\n") {
			if i := strings.Index(line, "--"); i >= 0 {
				line = line[:i]
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines = append(lines, line)
		}
		stmt := strings.TrimSpace(strings.Join(lines, "\n"))
		if stmt == "" {
			continue
		}
		out = append(out, stmt)
	}
	return out
}

// nowMS is the current store time in Unix milliseconds.
func (s *Store) nowMS() int64 {
	return s.now().UnixMilli()
}

// Stats is a row count per table, for doctor's database health line.
type Stats struct {
	Messages     int
	Agents       int
	Observations int
}

// Stats reports how many rows each table holds.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&st.Messages); err != nil {
		return Stats{}, fmt.Errorf("store: stats: %w", err)
	}
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents`).Scan(&st.Agents); err != nil {
		return Stats{}, fmt.Errorf("store: stats: %w", err)
	}
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations`).Scan(&st.Observations); err != nil {
		return Stats{}, fmt.Errorf("store: stats: %w", err)
	}
	return st, nil
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
