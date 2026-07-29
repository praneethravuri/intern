package main

import "testing"

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
