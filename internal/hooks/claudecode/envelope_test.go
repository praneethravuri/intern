package claudecode

import "testing"

func TestFormatEnvelopeRoundTrips(t *testing.T) {
	got := FormatEnvelope(KindMail, "2 messages delivered")

	kind, body, ok := ParseEnvelope(got)
	if !ok {
		t.Fatalf("ParseEnvelope(%q) = ok false, want true", got)
	}
	if kind != KindMail || body != "2 messages delivered" {
		t.Fatalf("ParseEnvelope(%q) = (%q, %q), want (%q, %q)", got, kind, body, KindMail, "2 messages delivered")
	}
}

func TestFormatEnvelopeStartsWithInvisibleSeparator(t *testing.T) {
	got := FormatEnvelope(KindMail, "hi")
	if r := []rune(got)[0]; r != '\u2063' {
		t.Fatalf("first rune = %U, want U+2063", r)
	}
}

func TestParseEnvelopeRejectsPlainText(t *testing.T) {
	cases := []string{
		"",
		"a normal message body",
		"INTERN_OP: v1 mail: no marker prefix",
		"\u2063not an op line at all",
	}
	for _, s := range cases {
		if _, _, ok := ParseEnvelope(s); ok {
			t.Fatalf("ParseEnvelope(%q) = ok true, want false", s)
		}
	}
}
