package daemon

import (
	"strings"
	"testing"
)

func TestSuggest(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		candidates []string
		want       string
	}{
		{
			name:       "an ordinary one-letter typo suggests the close match",
			target:     "backand",
			candidates: []string{"backend", "frontend", "docs"},
			want:       "backend",
		},
		{
			name:       "too far away suggests nothing",
			target:     "backxxxyyy",
			candidates: []string{"backend"},
			want:       "",
		},
		{
			name:       "a truncation-style typo matches via the prefix rule",
			target:     "back",
			candidates: []string{"backend"},
			want:       "backend",
		},
		{
			name:       "no candidates at all suggests nothing",
			target:     "backand",
			candidates: nil,
			want:       "",
		},
		{
			name:       "an exact match still returns itself",
			target:     "backend",
			candidates: []string{"backend"},
			want:       "backend",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := suggest(tc.target, tc.candidates); got != tc.want {
				t.Fatalf("suggest(%q, %v) = %q, want %q", tc.target, tc.candidates, got, tc.want)
			}
		})
	}
}

// TestMintNameIsStableForTheSameSession is the core guarantee behind
// resolving an empty-Name register: the same session id must always mint
// the same name, or two invocations from the same shell would mint two
// different names and never see each other's registration as idempotent.
func TestMintNameIsStableForTheSameSession(t *testing.T) {
	a := mintName("claude-code", "session-a")
	b := mintName("claude-code", "session-a")
	if a != b {
		t.Fatalf("mintName() is not stable across calls: %q != %q", a, b)
	}
}

// TestMintNameDiffersForADifferentSession is the flip side: a different
// session id must mint a different name, or two unrelated shells would
// collide on the same identity.
func TestMintNameDiffersForADifferentSession(t *testing.T) {
	a := mintName("claude-code", "session-a")
	b := mintName("claude-code", "session-b")
	if a == b {
		t.Fatalf("mintName() collided for two different sessions: both = %q", a)
	}
}

func TestMintNameHasTheHarnessPrefixedShape(t *testing.T) {
	got := mintName("claude-code", "session-a")
	if !strings.HasPrefix(got, "claude-code-") {
		t.Fatalf("mintName() = %q, want a claude-code-<hex4> shape", got)
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"backand", "backend", 1}, // one substitution
		{"kitten", "sitting", 3},  // the textbook example
	}
	for _, tc := range tests {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := levenshtein(tc.b, tc.a); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d (not symmetric)", tc.b, tc.a, got, tc.want)
		}
	}
}
