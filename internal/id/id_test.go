package id

import (
	"sync"
	"testing"

	"github.com/oklog/ulid/v2"
)

// mustNew is New() with the test-fatal boilerplate factored out, since every
// test below expects success and only wants the string.
func mustNew(t *testing.T) string {
	t.Helper()
	got, err := New()
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	return got
}

func TestNewLength(t *testing.T) {
	for i := 0; i < 100; i++ {
		got := mustNew(t)
		if len(got) != 26 {
			t.Fatalf("New() = %q, length %d, want length 26", got, len(got))
		}
	}
}

func TestNewErrorIsNilInTheNormalCase(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
}

func TestNewIsParseableULID(t *testing.T) {
	got := mustNew(t)
	if _, err := ulid.ParseStrict(got); err != nil {
		t.Fatalf("ulid.ParseStrict(%q) = %v, want nil", got, err)
	}
}

func TestNewNoDuplicates(t *testing.T) {
	const n = 10_000

	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		got := mustNew(t)
		if _, dup := seen[got]; dup {
			t.Fatalf("New() produced duplicate %q on iteration %d", got, i)
		}
		seen[got] = struct{}{}
	}

	if len(seen) != n {
		t.Fatalf("collected %d unique ids, want %d", len(seen), n)
	}
}

// Runs with no sleeps so thousands of ids land in the same millisecond,
// where only monotonic entropy (not random) keeps them ordered.
func TestNewStrictlyIncreasing(t *testing.T) {
	const n = 10_000

	ids := make([]string, n)
	for i := range ids {
		ids[i] = mustNew(t)
	}

	sameMillisecond := 0
	for i := 1; i < n; i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("id %d (%q) does not sort strictly after id %d (%q)",
				i, ids[i], i-1, ids[i-1])
		}
		// The first 10 characters are the millisecond timestamp.
		if ids[i][:10] == ids[i-1][:10] {
			sameMillisecond++
		}
	}

	// Zero shared milliseconds would make this test vacuous.
	if sameMillisecond == 0 {
		t.Fatalf("no two of %d ids shared a millisecond, so this test proved "+
			"nothing about sub-millisecond ordering", n)
	}
	t.Logf("%d of %d consecutive pairs shared a millisecond", sameMillisecond, n-1)
}

func TestNewOrderedAcrossMillisecondBoundaries(t *testing.T) {
	const n = 2_000

	prev := mustNew(t)
	prefixChanges := 0
	for i := 1; i < n; i++ {
		got := mustNew(t)
		if got <= prev {
			t.Fatalf("id %d (%q) does not sort strictly after %q", i, got, prev)
		}
		if got[:10] != prev[:10] {
			prefixChanges++
		}
		prev = got
	}

	if prefixChanges == 0 {
		t.Skip("the whole run fitted in one millisecond; nothing to say about boundaries")
	}
}

// Asserts uniqueness only, not global ordering: goroutines sample the clock
// before the reader's lock, so timestamp order across goroutines isn't guaranteed.
func TestNewConcurrent(t *testing.T) {
	const (
		goroutines = 100
		perRoutine = 100
	)

	var wg sync.WaitGroup
	results := make([][]string, goroutines)
	errs := make([][]error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			local := make([]string, perRoutine)
			localErrs := make([]error, perRoutine)
			for i := range local {
				local[i], localErrs[i] = New()
			}
			results[g] = local
			errs[g] = localErrs
		}(g)
	}
	wg.Wait()

	seen := make(map[string]struct{}, goroutines*perRoutine)
	for g, local := range results {
		for i, got := range local {
			if err := errs[g][i]; err != nil {
				t.Fatalf("goroutine %d id %d: New() error = %v, want nil", g, i, err)
			}
			if len(got) != 26 {
				t.Fatalf("goroutine %d id %d: New() = %q, length %d, want length 26",
					g, i, got, len(got))
			}
			if _, err := ulid.ParseStrict(got); err != nil {
				t.Fatalf("goroutine %d id %d: %q is not a valid ULID: %v", g, i, got, err)
			}
			if _, dup := seen[got]; dup {
				t.Fatalf("goroutine %d id %d: duplicate id %q", g, i, got)
			}
			seen[got] = struct{}{}
		}
	}

	if want := goroutines * perRoutine; len(seen) != want {
		t.Fatalf("collected %d unique ids, want %d", len(seen), want)
	}
}

func BenchmarkNew(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = New()
	}
}
