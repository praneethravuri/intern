// Package claudecode is Claude Code's hook implementation: writing its
// settings.json hook entries and running as the hook commands themselves.
// Every other harness keeps using intern wait; nothing outside this package
// or the CLI's "hooks" command tree knows Claude Code hooks exist.
package claudecode

import "strings"

// EnvelopeMarker prefixes every intern-injected block of mail so the
// receiving agent can tell operational context from a human instruction or
// another agent's message body structurally, not by inferring it from
// prose. U+2063 (invisible separator) never appears in normal text.
const EnvelopeMarker = "\u2063INTERN_OP: v1 "

// KindMail tags a delivered-mail envelope, the only kind this phase emits.
const KindMail = "mail"

// FormatEnvelope wraps body in the typed envelope.
func FormatEnvelope(kind, body string) string {
	return EnvelopeMarker + kind + ": " + body
}

// ParseEnvelope reports whether s is a intern envelope, and if so its kind
// and body.
func ParseEnvelope(s string) (kind, body string, ok bool) {
	rest, found := strings.CutPrefix(s, EnvelopeMarker)
	if !found {
		return "", "", false
	}
	kind, body, found = strings.Cut(rest, ": ")
	if !found {
		return "", "", false
	}
	return kind, body, true
}
