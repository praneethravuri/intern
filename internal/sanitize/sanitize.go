// Package sanitize strips terminal control bytes from untrusted strings --
// harness names, cwds, message bodies -- before they reach a human's
// terminal or a client-visible error message.
package sanitize

import "strings"

// HasControlBytes reports whether s contains a C0 control code or DEL.
func HasControlBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// Replace maps every C0 control byte and DEL to replacement, except '\n'
// and '\t' when keepWhitespace is true -- callers rendering a single-line
// field want every control byte gone; callers rendering a message body
// want its line breaks kept.
func Replace(s string, replacement rune, keepWhitespace bool) string {
	return strings.Map(func(r rune) rune {
		if keepWhitespace && (r == '\n' || r == '\t') {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return replacement
		}
		return r
	}, s)
}
