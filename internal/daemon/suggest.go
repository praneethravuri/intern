package daemon

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// mintName synthesises "<harness>-<hex4>" from a hash of session, for a
// register with no chosen name and no existing registration to resolve to.
func mintName(harness, session string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(session))
	return fmt.Sprintf("%s-%04x", harness, h.Sum32()&0xffff)
}

// suggest returns the closest name in candidates to target (edit distance
// <=2, or one is a prefix of the other), or "" if nothing is close enough.
func suggest(target string, candidates []string) string {
	const maxDistance = 2

	best := ""
	bestDist := -1
	for _, c := range candidates {
		isPrefixMatch := strings.HasPrefix(c, target) || strings.HasPrefix(target, c)
		d := levenshtein(target, c)
		if !isPrefixMatch && d > maxDistance {
			continue
		}
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// levenshtein returns the edit distance between a and b (two-row DP).
func levenshtein(a, b string) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
