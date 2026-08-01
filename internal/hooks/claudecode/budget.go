package claudecode

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LoadBudget returns how many consecutive Stop-hook blocks key has spent so
// far. Any missing or unreadable file reads as 0, not an error: a corrupt
// counter must never itself block the hook's fail-open path.
func LoadBudget(dir, key string) int {
	raw, err := os.ReadFile(budgetPath(dir, key)) //nolint:gosec // path is our own hashed counter file, never untrusted input
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// IncrementBudget records one more consecutive block for key.
func IncrementBudget(dir, key string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	n := LoadBudget(dir, key) + 1
	if err := os.WriteFile(budgetPath(dir, key), []byte(strconv.Itoa(n)), 0o600); err != nil { //nolint:gosec // see LoadBudget
		return fmt.Errorf("write budget for %s: %w", key, err)
	}
	return nil
}

// ResetBudget clears key's counter, e.g. once a Stop is allowed to proceed
// normally (mail delivered or the cap was reached).
func ResetBudget(dir, key string) {
	_ = os.Remove(budgetPath(dir, key))
}

func budgetPath(dir, key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(dir, fmt.Sprintf("%x.count", sum[:8]))
}
