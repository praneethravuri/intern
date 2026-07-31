package sanitize

import "testing"

func TestHasControlBytes(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"plain text", false},
		{"", false},
		{"has\ttab", true},
		{"has\nnewline", true},
		{"has\x1b[31mescape", true},
		{"has\x7fdel", true},
	}
	for _, tc := range cases {
		if got := HasControlBytes(tc.s); got != tc.want {
			t.Errorf("HasControlBytes(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

func TestReplace_WithoutKeepingWhitespace(t *testing.T) {
	got := Replace("a\tb\nc\x1bd\x7fe", ' ', false)
	want := "a b c d e"
	if got != want {
		t.Errorf("Replace = %q, want %q", got, want)
	}
}

func TestReplace_KeepingWhitespace(t *testing.T) {
	got := Replace("a\tb\nc\x1bd\x7fe", '�', true)
	want := "a\tb\nc�d�e"
	if got != want {
		t.Errorf("Replace = %q, want %q", got, want)
	}
}
