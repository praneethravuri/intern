package kind

import "testing"

func TestValid(t *testing.T) {
	for _, k := range All {
		if !Valid(k) {
			t.Errorf("Valid(%q) = false, want true (listed in All)", k)
		}
	}
	if Valid("shout") {
		t.Error("Valid(\"shout\") = true, want false")
	}
	if Valid("") {
		t.Error("Valid(\"\") = true, want false")
	}
}
