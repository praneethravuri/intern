package claudecode

import (
	"os"
	"testing"
)

func TestLoadBudgetIsZeroWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if n := LoadBudget(dir, "session-1"); n != 0 {
		t.Fatalf("LoadBudget on a missing file = %d, want 0", n)
	}
}

func TestIncrementBudgetCounts(t *testing.T) {
	dir := t.TempDir()

	for want := 1; want <= 3; want++ {
		if err := IncrementBudget(dir, "session-1"); err != nil {
			t.Fatalf("IncrementBudget: %v", err)
		}
		if got := LoadBudget(dir, "session-1"); got != want {
			t.Fatalf("LoadBudget after %d increments = %d, want %d", want, got, want)
		}
	}
}

func TestResetBudgetClearsTheCounter(t *testing.T) {
	dir := t.TempDir()
	_ = IncrementBudget(dir, "session-1")
	_ = IncrementBudget(dir, "session-1")

	ResetBudget(dir, "session-1")

	if got := LoadBudget(dir, "session-1"); got != 0 {
		t.Fatalf("LoadBudget after reset = %d, want 0", got)
	}
}

func TestBudgetKeysAreIndependent(t *testing.T) {
	dir := t.TempDir()
	_ = IncrementBudget(dir, "session-1")
	_ = IncrementBudget(dir, "session-1")
	_ = IncrementBudget(dir, "session-2")

	if got := LoadBudget(dir, "session-1"); got != 2 {
		t.Fatalf("session-1 budget = %d, want 2", got)
	}
	if got := LoadBudget(dir, "session-2"); got != 1 {
		t.Fatalf("session-2 budget = %d, want 1", got)
	}
}

func TestLoadBudgetIgnoresACorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := IncrementBudget(dir, "session-1"); err != nil {
		t.Fatalf("IncrementBudget: %v", err)
	}
	// Corrupt it directly, bypassing the package's own writer.
	if err := os.WriteFile(budgetPath(dir, "session-1"), []byte("not-a-number"), 0o600); err != nil {
		t.Fatalf("corrupt budget file: %v", err)
	}
	if got := LoadBudget(dir, "session-1"); got != 0 {
		t.Fatalf("LoadBudget on a corrupt file = %d, want 0 (fail open)", got)
	}
}
