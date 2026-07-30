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
			name:       "a substitution typo has no prefix relationship, suggests nothing",
			target:     "backand",
			candidates: []string{"backend", "frontend", "docs"},
			want:       "",
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
