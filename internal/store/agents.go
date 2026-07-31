package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanAgent reads one agents row in agentCols order.
func scanAgent(sc rowScanner) (Agent, error) {
	var (
		a          Agent
		registered int64
		lastSeen   int64
		pid        int64
		dropped    int64
	)
	err := sc.Scan(
		&a.Workspace, &a.Name, &a.Harness, &a.SessionID, &a.Cwd, &pid, &a.PIDStart,
		&dropped, &registered, &lastSeen, &a.LastKind, &a.LastNote,
	)
	if err != nil {
		return Agent{}, err
	}
	a.PID = int(pid)
	a.Dropped = int(dropped)
	a.RegisteredAt = fromMS(registered)
	a.LastSeen = fromMS(lastSeen)
	return a, nil
}

// normalize fills in schema defaults for empty fields.
func normalize(a *Agent) error {
	if a.Workspace == "" || a.Name == "" {
		return fmt.Errorf("%w: agent needs both workspace and name", ErrBadAddress)
	}
	if a.Harness == "" {
		a.Harness = "unknown"
	}
	return nil
}

// Register claims workspace/name via a guarded upsert: it succeeds when the
// name is free, stale (before staleCutoff), or re-claimed by the same
// session_id; otherwise it returns ErrNameTaken.
func (s *Store) Register(ctx context.Context, a Agent, staleCutoff time.Time) error {
	if err := normalize(&a); err != nil {
		return err
	}

	nowMS := s.nowMS()

	res, err := s.w.ExecContext(ctx, qRegister,
		a.Workspace, a.Name, a.Harness, a.SessionID, a.Cwd, a.PID, a.PIDStart,
		nowMS, nowMS,
		staleCutoff.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("store: register %s: %w", a.Address(), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: register %s: %w", a.Address(), err)
	}
	if n == 0 { // WHERE guard suppressed the upsert: a live agent holds the name.
		return fmt.Errorf("%w: %s", ErrNameTaken, a.Address())
	}
	return nil
}

// ReclaimAgent overwrites workspace/name only if it still holds exactly the
// incumbent identity the caller observed (pid, pid_start) -- a compare-and-
// swap that closes the gap between confirming an incumbent dead and acting
// on it, where a third party could otherwise have already reclaimed or
// revived the row. A false return means the row moved; the caller's
// conflict is real and should be reported as such, not silently ignored.
func (s *Store) ReclaimAgent(ctx context.Context, a Agent, incumbentPID int, incumbentPIDStart int64) (bool, error) {
	if err := normalize(&a); err != nil {
		return false, err
	}

	nowMS := s.nowMS()
	res, err := s.w.ExecContext(ctx, qReclaimAgent,
		a.Harness, a.SessionID, a.Cwd, a.PID, a.PIDStart, nowMS, nowMS,
		a.Workspace, a.Name, incumbentPID, incumbentPIDStart,
	)
	if err != nil {
		return false, fmt.Errorf("store: reclaim %s: %w", a.Address(), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: reclaim %s: %w", a.Address(), err)
	}
	return n > 0, nil
}

// Heartbeat refreshes last_seen and last_kind, then reports how many
// messages are waiting. note is only applied when non-empty -- an empty note
// leaves last_note as it was, so a caller with nothing to say about "what are
// you doing" can't accidentally clear a previous one.
func (s *Store) Heartbeat(ctx context.Context, ws, name, kind, note string) (pending int, err error) {
	if ws == "" || name == "" {
		return 0, fmt.Errorf("%w: heartbeat needs workspace and name", ErrBadAddress)
	}

	res, err := s.w.ExecContext(ctx, qHeartbeat, s.nowMS(), kind, note, note, ws, name)
	if err != nil {
		return 0, fmt.Errorf("store: heartbeat %s@%s: %w", name, ws, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: heartbeat %s@%s: %w", name, ws, err)
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: %s@%s", ErrNoSuchAgent, name, ws)
	}
	return s.PendingCount(ctx, ws, name)
}

// GetAgent returns a single registered agent regardless of staleness.
func (s *Store) GetAgent(ctx context.Context, ws, name string) (Agent, error) {
	a, err := scanAgent(s.r.QueryRowContext(ctx, qGetAgent, ws, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, fmt.Errorf("%w: %s@%s", ErrNoSuchAgent, name, ws)
	}
	if err != nil {
		return Agent{}, fmt.Errorf("store: get agent %s@%s: %w", name, ws, err)
	}
	return a, nil
}

// FindNameBySession returns the name session already holds in ws, so a
// caller with no chosen name can resolve to its existing registration
// instead of minting a new one.
func (s *Store) FindNameBySession(ctx context.Context, ws, session string) (string, error) {
	var name string
	err := s.r.QueryRowContext(ctx, qFindNameBySession, ws, session).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: no agent in %s for this session", ErrNoSuchAgent, ws)
	}
	if err != nil {
		return "", fmt.Errorf("store: find name by session: %w", err)
	}
	return name, nil
}

// Rename changes the name of the agent a.SessionID already holds in
// a.Workspace to a.Name, refreshing its other fields the same way Register
// would, and moves its pending mail's to_name along in the same transaction
// -- so no orphan row is left holding mail nobody will read. It returns the
// name the session held before the rename. ErrNoSuchAgent means the session
// holds nothing to rename; ErrNameTaken means a different, still-fresh
// session holds a.Name (staleCutoff gives a dead holder's name back, the
// same reclaim path Register already has).
func (s *Store) Rename(ctx context.Context, a Agent, staleCutoff time.Time) (oldName string, err error) {
	if err := normalize(&a); err != nil {
		return "", err
	}
	if a.SessionID == "" {
		return "", fmt.Errorf("%w: rename needs a session id", ErrBadAddress)
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("store: rename: %w", err)
	}
	defer s.rollback(tx)

	if err := tx.QueryRowContext(ctx, qFindNameBySession, a.Workspace, a.SessionID).Scan(&oldName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: no agent in %s for this session", ErrNoSuchAgent, a.Workspace)
		}
		return "", fmt.Errorf("store: rename: find: %w", err)
	}

	if a.Name != oldName {
		target, getErr := scanAgent(tx.QueryRowContext(ctx, qGetAgent, a.Workspace, a.Name))
		switch {
		case errors.Is(getErr, sql.ErrNoRows):
			// target name free
		case getErr != nil:
			return "", fmt.Errorf("store: rename: check target: %w", getErr)
		case target.SessionID != a.SessionID && target.LastSeen.Before(staleCutoff):
			// Evicted here, inside the transaction, so qRenameAgent's UPDATE
			// below never collides with the row it's about to replace.
			if _, err := tx.ExecContext(ctx, qDeleteAgent, a.Workspace, a.Name); err != nil {
				return "", fmt.Errorf("store: rename: evict stale target: %w", err)
			}
		}
	}

	nowMS := s.nowMS()
	res, err := tx.ExecContext(ctx, qRenameAgent,
		a.Name, a.Harness, a.Cwd, a.PID, a.PIDStart, nowMS,
		a.Workspace, a.SessionID,
		a.Workspace, a.Name, a.SessionID)
	if err != nil {
		return "", fmt.Errorf("store: rename: update agent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("store: rename: update agent: %w", err)
	}
	if n == 0 {
		return "", fmt.Errorf("%w: %s@%s", ErrNameTaken, a.Name, a.Workspace)
	}

	if oldName != a.Name {
		if _, err := tx.ExecContext(ctx, qRenameMessages, a.Name, a.Workspace, oldName); err != nil {
			return "", fmt.Errorf("store: rename: update messages: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: rename: commit: %w", err)
	}
	return oldName, nil
}

// ListAgents returns agents ordered by workspace then name. An empty ws means
// every workspace; a staleAfter of zero or less disables the staleness filter.
func (s *Store) ListAgents(ctx context.Context, ws string, staleAfter time.Duration) ([]Agent, error) {
	var cutoff int64
	if staleAfter > 0 {
		cutoff = s.nowMS() - staleAfter.Milliseconds()
	}

	rows, err := s.r.QueryContext(ctx, qListAgents, ws, ws, cutoff)
	if err != nil {
		return nil, fmt.Errorf("store: list agents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Agent, 0, 8)
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list agents: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list agents: %w", err)
	}
	return out, nil
}

// deleteAgentsChunk bounds how many keys go into one statement -- 2 bound
// params per key, well under even a conservative SQLITE_MAX_VARIABLE_NUMBER.
// Unlike Inbox/Drain's message ids, sweepDeadAgents' key count isn't bounded
// by any request-facing limit, so this one genuinely needs chunking.
const deleteAgentsChunk = 400

// DeleteAgents removes the given agent rows, chunked to stay under SQLite's
// bound-parameter limit, and reports how many were removed in total.
func (s *Store) DeleteAgents(ctx context.Context, keys []AgentKey) (int, error) {
	total := 0
	for len(keys) > 0 {
		n := min(len(keys), deleteAgentsChunk)
		deleted, err := s.deleteAgentsOnce(ctx, keys[:n])
		if err != nil {
			return total, err
		}
		total += deleted
		keys = keys[n:]
	}
	return total, nil
}

func (s *Store) deleteAgentsOnce(ctx context.Context, keys []AgentKey) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}

	var sb strings.Builder
	sb.WriteString("DELETE FROM agents WHERE ")
	args := make([]any, 0, len(keys)*2)
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(" OR ")
		}
		sb.WriteString("(workspace = ? AND name = ?)")
		args = append(args, k.Workspace, k.Name)
	}

	res, err := s.w.ExecContext(ctx, sb.String(), args...)
	if err != nil {
		return 0, fmt.Errorf("store: delete agents: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: delete agents: %w", err)
	}
	return int(n), nil
}
