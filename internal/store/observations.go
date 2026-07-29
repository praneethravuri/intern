package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Observation is one append-only fact about what an agent was doing;
// callers compute state from the latest row.
type Observation struct {
	Workspace, Name string
	Kind            string
	Detail          string
	At              time.Time
}

// Observe appends one observation.
func (s *Store) Observe(ctx context.Context, o Observation) error {
	if o.Workspace == "" || o.Name == "" {
		return fmt.Errorf("%w: observation needs both workspace and name", ErrBadAddress)
	}
	at := o.At
	if at.IsZero() {
		at = s.now()
	}
	if _, err := s.w.ExecContext(ctx, qObserve,
		o.Workspace, o.Name, o.Kind, o.Detail, at.UnixMilli()); err != nil {
		return fmt.Errorf("store: observe %s@%s: %w", o.Name, o.Workspace, err)
	}
	return nil
}

// LastObservation returns the most recent observation for workspace/name.
// No rows returns the zero value, not an error.
func (s *Store) LastObservation(ctx context.Context, ws, name string) (Observation, error) {
	var (
		o  = Observation{Workspace: ws, Name: name}
		at int64
	)
	err := s.r.QueryRowContext(ctx, qLastObservation, ws, name).Scan(&o.Kind, &o.Detail, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Observation{}, nil
	}
	if err != nil {
		return Observation{}, fmt.Errorf("store: last observation %s@%s: %w", name, ws, err)
	}
	o.At = fromMS(at)
	return o, nil
}

// LastObservations returns the most recent observation per agent in a
// workspace, keyed by name.
func (s *Store) LastObservations(ctx context.Context, ws string) (map[string]Observation, error) {
	rows, err := s.r.QueryContext(ctx, qLastObservations, ws)
	if err != nil {
		return nil, fmt.Errorf("store: last observations %s: %w", ws, err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]Observation)
	for rows.Next() {
		var (
			name string
			o    Observation
			at   int64
		)
		if err := rows.Scan(&name, &o.Kind, &o.Detail, &at); err != nil {
			return nil, fmt.Errorf("store: last observations %s: %w", ws, err)
		}
		o.Workspace = ws
		o.Name = name
		o.At = fromMS(at)
		out[name] = o
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: last observations %s: %w", ws, err)
	}
	return out, nil
}

// PendingByWorkspace returns pending message counts per recipient name in a workspace.
func (s *Store) PendingByWorkspace(ctx context.Context, ws string) (map[string]int, error) {
	rows, err := s.r.QueryContext(ctx, qPendingByWorkspace, ws)
	if err != nil {
		return nil, fmt.Errorf("store: pending by workspace %s: %w", ws, err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]int)
	for rows.Next() {
		var (
			name string
			n    int
		)
		if err := rows.Scan(&name, &n); err != nil {
			return nil, fmt.Errorf("store: pending by workspace %s: %w", ws, err)
		}
		out[name] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: pending by workspace %s: %w", ws, err)
	}
	return out, nil
}

// SweepObservations deletes observation rows older than olderThan and
// returns how many were removed.
func (s *Store) SweepObservations(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := s.nowMS()
	if olderThan > 0 {
		cutoff -= olderThan.Milliseconds()
	}

	res, err := s.w.ExecContext(ctx, qSweepObservations, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: sweep observations: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: sweep observations: %w", err)
	}
	return int(n), nil
}
