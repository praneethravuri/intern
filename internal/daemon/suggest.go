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

// suggest returns the first candidate that is a prefix of target or vice
// versa, or "" if none is.
func suggest(target string, candidates []string) string {
	for _, c := range candidates {
		if strings.HasPrefix(c, target) || strings.HasPrefix(target, c) {
			return c
		}
	}
	return ""
}
