package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// scanClaim reads one claims row in claimCols order.
func scanClaim(sc rowScanner) (Claim, error) {
	var (
		c        Claim
		ownerPID int64
		leased   int64
		expires  int64
	)
	err := sc.Scan(&c.Workspace, &c.Key, &ownerPID, &c.OwnerPIDStart,
		&c.LeaseID, &c.LeaseHolder, &leased, &expires)
	if err != nil {
		return Claim{}, err
	}
	c.OwnerPID = int(ownerPID)
	c.LeasedAt = fromMS(leased)
	c.ExpiresAt = fromMS(expires)
	return c, nil
}

// newLeaseID mints a fresh, unguessable 128-bit lease id, hex-encoded. Not
// internal/id.New (ULID): a lease id must be opaque and unpredictable, never
// sortable or timestamped the way a ULID deliberately is.
func newLeaseID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: generate lease id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// newClaim builds a fresh Claim with a freshly minted lease id, shared by
// Claim and ReclaimClaim -- both write the same shape, just under different
// WHERE guards.
func (s *Store) newClaim(workspace, key string, ownerPID int, ownerPIDStart int64, holder string, ttl time.Duration) (Claim, error) {
	leaseID, err := newLeaseID()
	if err != nil {
		return Claim{}, err
	}
	now := s.now()
	return Claim{
		Workspace: workspace, Key: key,
		OwnerPID: ownerPID, OwnerPIDStart: ownerPIDStart,
		LeaseID: leaseID, LeaseHolder: holder,
		LeasedAt: now, ExpiresAt: now.Add(ttl),
	}, nil
}

// Claim acquires workspace/key for (ownerPID, ownerPIDStart), or renews it
// if that is already the live owner. It always mints and stores a fresh
// lease id, even on a renewal -- a stale id from a prior acquisition of the
// same key must never match. ErrClaimHeld means a different, live owner
// holds it; the caller should check whether that owner is provably dead and
// call ReclaimClaim instead.
func (s *Store) Claim(ctx context.Context, workspace, key string, ownerPID int, ownerPIDStart int64, holder string, ttl time.Duration) (Claim, error) {
	if workspace == "" || key == "" {
		return Claim{}, fmt.Errorf("%w: claim needs both workspace and key", ErrBadAddress)
	}
	if ownerPID <= 0 {
		return Claim{}, fmt.Errorf("%w: claim needs a live owner pid", ErrBadAddress)
	}

	c, err := s.newClaim(workspace, key, ownerPID, ownerPIDStart, holder, ttl)
	if err != nil {
		return Claim{}, err
	}

	res, err := s.w.ExecContext(ctx, qClaim,
		c.Workspace, c.Key, c.OwnerPID, c.OwnerPIDStart, c.LeaseID, c.LeaseHolder,
		c.LeasedAt.UnixMilli(), c.ExpiresAt.UnixMilli(),
		c.LeasedAt.UnixMilli(), // "now" for the expiry comparison, same instant as LeasedAt
	)
	if err != nil {
		return Claim{}, fmt.Errorf("store: claim %s/%s: %w", workspace, key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Claim{}, fmt.Errorf("store: claim %s/%s: %w", workspace, key, err)
	}
	if n == 0 {
		return Claim{}, fmt.Errorf("%w: %s/%s", ErrClaimHeld, workspace, key)
	}
	return c, nil
}

// ReclaimClaim overwrites workspace/key only if it still holds exactly the
// incumbent identity the caller observed (owner_pid, owner_pid_start) -- the
// same compare-and-swap ReclaimAgent uses to close the gap between
// confirming an incumbent dead and acting on it. A false return means the
// row moved; the caller's conflict is real.
func (s *Store) ReclaimClaim(ctx context.Context, workspace, key string, incumbentPID int, incumbentPIDStart int64, ownerPID int, ownerPIDStart int64, holder string, ttl time.Duration) (Claim, bool, error) {
	c, err := s.newClaim(workspace, key, ownerPID, ownerPIDStart, holder, ttl)
	if err != nil {
		return Claim{}, false, err
	}

	res, err := s.w.ExecContext(ctx, qReclaimClaim,
		c.OwnerPID, c.OwnerPIDStart, c.LeaseID, c.LeaseHolder, c.LeasedAt.UnixMilli(), c.ExpiresAt.UnixMilli(),
		workspace, key, incumbentPID, incumbentPIDStart,
	)
	if err != nil {
		return Claim{}, false, fmt.Errorf("store: reclaim claim %s/%s: %w", workspace, key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Claim{}, false, fmt.Errorf("store: reclaim claim %s/%s: %w", workspace, key, err)
	}
	if n == 0 {
		return Claim{}, false, nil
	}
	return c, true, nil
}

// GetClaim returns a single claim regardless of expiry or owner liveness --
// callers decide what "still held" means for their purpose.
func (s *Store) GetClaim(ctx context.Context, workspace, key string) (Claim, error) {
	c, err := scanClaim(s.r.QueryRowContext(ctx, qGetClaim, workspace, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Claim{}, fmt.Errorf("%w: %s/%s", ErrNoSuchClaim, workspace, key)
	}
	if err != nil {
		return Claim{}, fmt.Errorf("store: get claim %s/%s: %w", workspace, key, err)
	}
	return c, nil
}

// Release removes workspace/key's claim if ifClaimID matches its current
// lease exactly, in one statement -- the authorization decision and the
// mutation are the same atomic compare-and-swap, not a check then a
// separate act. ErrNoSuchClaim and ErrClaimMismatch are distinguished by a
// read-only follow-up lookup purely for a better error message; that lookup
// can race and report the wrong one under concurrent modification, the same
// accepted cosmetic ceiling registerOrReclaim's "created" bool has.
func (s *Store) Release(ctx context.Context, workspace, key, ifClaimID string) error {
	if workspace == "" || key == "" {
		return fmt.Errorf("%w: release needs both workspace and key", ErrBadAddress)
	}
	if ifClaimID == "" {
		return fmt.Errorf("%w: release needs --if-claim-id", ErrBadAddress)
	}

	res, err := s.w.ExecContext(ctx, qReleaseClaim, workspace, key, ifClaimID)
	if err != nil {
		return fmt.Errorf("store: release %s/%s: %w", workspace, key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: release %s/%s: %w", workspace, key, err)
	}
	if n > 0 {
		return nil
	}

	if _, err := s.GetClaim(ctx, workspace, key); errors.Is(err, ErrNoSuchClaim) {
		return fmt.Errorf("%w: %s/%s", ErrNoSuchClaim, workspace, key)
	}
	return fmt.Errorf("%w: %s/%s", ErrClaimMismatch, workspace, key)
}

// ListClaims returns claims ordered by workspace then key. An empty ws means
// every workspace.
func (s *Store) ListClaims(ctx context.Context, ws string) ([]Claim, error) {
	rows, err := s.r.QueryContext(ctx, qListClaims, ws, ws)
	if err != nil {
		return nil, fmt.Errorf("store: list claims: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Claim, 0, 8)
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list claims: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list claims: %w", err)
	}
	return out, nil
}

// SweepExpiredClaims deletes claims whose TTL has elapsed, so the table
// plateaus instead of growing forever -- unlike messages, a claim past its
// TTL has no read/unread state worth retaining.
func (s *Store) SweepExpiredClaims(ctx context.Context) (int, error) {
	res, err := s.w.ExecContext(ctx, qSweepExpiredClaims, s.nowMS())
	if err != nil {
		return 0, fmt.Errorf("store: sweep expired claims: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: sweep expired claims: %w", err)
	}
	return int(n), nil
}
