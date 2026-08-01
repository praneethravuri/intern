package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const claimTTL = time.Hour

func mustClaim(t *testing.T, s *Store, ws, key string, pid int, pidStart int64, holder string) Claim {
	t.Helper()
	c, err := s.Claim(context.Background(), ws, key, pid, pidStart, holder, claimTTL)
	if err != nil {
		t.Fatalf("Claim(%s/%s): %v", ws, key, err)
	}
	return c
}

func TestClaimFreshKeySucceeds(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	c, err := s.Claim(ctx, "ws", "src/main.go", 111, 222, "refactoring", claimTTL)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if c.LeaseID == "" {
		t.Fatal("Claim returned an empty lease id")
	}
	if c.Workspace != "ws" || c.Key != "src/main.go" || c.OwnerPID != 111 || c.OwnerPIDStart != 222 {
		t.Fatalf("Claim = %+v, fields do not match input", c)
	}

	got, err := s.GetClaim(ctx, "ws", "src/main.go")
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if got.LeaseID != c.LeaseID {
		t.Errorf("GetClaim lease id = %q, want %q", got.LeaseID, c.LeaseID)
	}
}

func TestClaimAlreadyHeldByLiveOwnerConflicts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	mustClaim(t, s, "ws", "src/main.go", 111, 222, "alice")

	_, err := s.Claim(ctx, "ws", "src/main.go", 333, 444, "bob", claimTTL)
	if !errors.Is(err, ErrClaimHeld) {
		t.Fatalf("Claim over a live owner = %v, want ErrClaimHeld", err)
	}
}

// TestClaimRenewalBySameOwnerMintsAFreshLeaseID is the design's core ABA
// guard: even the same live process re-acquiring its own claim gets a new
// lease id, so an old id it printed earlier can never match again.
func TestClaimRenewalBySameOwnerMintsAFreshLeaseID(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	first := mustClaim(t, s, "ws", "src/main.go", 111, 222, "alice")

	second, err := s.Claim(ctx, "ws", "src/main.go", 111, 222, "alice", claimTTL)
	if err != nil {
		t.Fatalf("renewal Claim: %v", err)
	}
	if second.LeaseID == first.LeaseID {
		t.Fatal("renewal reused the previous lease id; must always mint a fresh one")
	}

	// The old lease id no longer authorizes anything.
	if err := s.Release(ctx, "ws", "src/main.go", first.LeaseID); !errors.Is(err, ErrClaimMismatch) {
		t.Fatalf("Release with the pre-renewal lease id = %v, want ErrClaimMismatch", err)
	}
}

func TestClaimPastTTLIsFreeForAnyone(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now

	mustClaim(t, s, "ws", "src/main.go", 111, 222, "alice")
	clk.advance(claimTTL + time.Minute)

	c, err := s.Claim(ctx, "ws", "src/main.go", 999, 888, "bob", claimTTL)
	if err != nil {
		t.Fatalf("Claim past TTL: %v", err)
	}
	if c.OwnerPID != 999 {
		t.Fatalf("Claim past TTL did not take over: %+v", c)
	}
}

func TestReclaimClaimSucceedsOnMatchingIncumbent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	mustClaim(t, s, "ws", "src/main.go", 111, 222, "alice")

	c, ok, err := s.ReclaimClaim(ctx, "ws", "src/main.go", 111, 222, 333, 444, "bob", claimTTL)
	if err != nil {
		t.Fatalf("ReclaimClaim: %v", err)
	}
	if !ok {
		t.Fatal("ReclaimClaim = false, want true (pid/pid_start matched)")
	}
	if c.OwnerPID != 333 {
		t.Fatalf("ReclaimClaim result = %+v, owner did not change", c)
	}
	got, err := s.GetClaim(ctx, "ws", "src/main.go")
	if err != nil || got.OwnerPID != 333 {
		t.Fatalf("reclaim did not take effect: %+v, err=%v", got, err)
	}
}

func TestReclaimClaimFailsWhenTheRowMoved(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	mustClaim(t, s, "ws", "src/main.go", 111, 222, "alice")

	_, ok, err := s.ReclaimClaim(ctx, "ws", "src/main.go", 999, 888, 333, 444, "bob", claimTTL) // wrong incumbent
	if err != nil {
		t.Fatalf("ReclaimClaim: %v", err)
	}
	if ok {
		t.Fatal("ReclaimClaim = true, want false (observed identity did not match the current row)")
	}
	got, err := s.GetClaim(ctx, "ws", "src/main.go")
	if err != nil || got.OwnerPID != 111 {
		t.Fatalf("a failed reclaim mutated the row: %+v, err=%v", got, err)
	}
}

func TestGetClaimUnknown(t *testing.T) {
	if _, err := newStore(t).GetClaim(context.Background(), "ws", "ghost"); !errors.Is(err, ErrNoSuchClaim) {
		t.Fatalf("GetClaim(ghost) = %v, want ErrNoSuchClaim", err)
	}
}

func TestReleaseWithCorrectLeaseIDSucceeds(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := mustClaim(t, s, "ws", "src/main.go", 111, 222, "alice")

	if err := s.Release(ctx, "ws", "src/main.go", c.LeaseID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := s.GetClaim(ctx, "ws", "src/main.go"); !errors.Is(err, ErrNoSuchClaim) {
		t.Fatalf("GetClaim after release = %v, want ErrNoSuchClaim", err)
	}
}

func TestReleaseUnknownClaimIsErrNoSuchClaim(t *testing.T) {
	err := newStore(t).Release(context.Background(), "ws", "ghost", "deadbeef")
	if !errors.Is(err, ErrNoSuchClaim) {
		t.Fatalf("Release(ghost) = %v, want ErrNoSuchClaim", err)
	}
}

// TestReleaseWithStaleLeaseIDIsRejected is the ABA test: a lease id from an
// earlier acquisition of the same key must not release (or otherwise
// affect) a newer, unrelated acquisition of that same key.
func TestReleaseWithStaleLeaseIDIsRejected(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	stale := mustClaim(t, s, "ws", "src/main.go", 111, 222, "alice")

	if err := s.Release(ctx, "ws", "src/main.go", stale.LeaseID); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	fresh := mustClaim(t, s, "ws", "src/main.go", 333, 444, "bob") // a new, unrelated acquisition

	if err := s.Release(ctx, "ws", "src/main.go", stale.LeaseID); !errors.Is(err, ErrClaimMismatch) {
		t.Fatalf("Release with the old lease id = %v, want ErrClaimMismatch", err)
	}
	got, err := s.GetClaim(ctx, "ws", "src/main.go")
	if err != nil || got.LeaseID != fresh.LeaseID {
		t.Fatalf("a stale release must not have touched the fresh claim: %+v, err=%v", got, err)
	}
}

func TestListClaimsFiltersByWorkspace(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	mustClaim(t, s, "ws1", "a", 1, 1, "")
	mustClaim(t, s, "ws1", "b", 2, 2, "")
	mustClaim(t, s, "ws2", "c", 3, 3, "")

	got, err := s.ListClaims(ctx, "ws1")
	if err != nil {
		t.Fatalf("ListClaims: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListClaims(ws1) = %d claims, want 2", len(got))
	}

	all, err := s.ListClaims(ctx, "")
	if err != nil {
		t.Fatalf("ListClaims(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListClaims(\"\") = %d claims, want 3", len(all))
	}
}

func TestSweepExpiredClaimsRemovesOnlyPastTTL(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now

	mustClaim(t, s, "ws", "old", 1, 1, "")
	clk.advance(claimTTL + time.Minute)
	mustClaim(t, s, "ws", "fresh", 2, 2, "")

	n, err := s.SweepExpiredClaims(ctx)
	if err != nil {
		t.Fatalf("SweepExpiredClaims: %v", err)
	}
	if n != 1 {
		t.Fatalf("SweepExpiredClaims removed %d, want 1", n)
	}
	if _, err := s.GetClaim(ctx, "ws", "old"); !errors.Is(err, ErrNoSuchClaim) {
		t.Errorf("GetClaim(old) after sweep = %v, want ErrNoSuchClaim", err)
	}
	if _, err := s.GetClaim(ctx, "ws", "fresh"); err != nil {
		t.Errorf("GetClaim(fresh) after sweep = %v, want it to survive", err)
	}
}

// TestConcurrentClaimsOnTheSameKeyExactlyOneWins races many goroutines to
// claim the same fresh key under -race; the store's CAS must let exactly one
// through, never zero and never more than one.
func TestConcurrentClaimsOnTheSameKeyExactlyOneWins(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const racers = 50
	var wins atomic.Int32
	var wg sync.WaitGroup
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			// Distinct pid/pid_start per racer: each is a genuinely different
			// owner, not a renewal of the same one.
			_, err := s.Claim(ctx, "ws", "contended", 1000+i, int64(i), "racer", claimTTL)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrClaimHeld) {
				t.Errorf("racer %d: unexpected error %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("%d racers won the claim, want exactly 1", got)
	}
}
