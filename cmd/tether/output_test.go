package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestFlagNamesOnACommandWithNoFlagsAtAll exercises the empty-list branch
// directly: cobra only adds --help once a command actually executes, so a
// bare, never-run *cobra.Command really can have zero flags.
func TestFlagNamesOnACommandWithNoFlagsAtAll(t *testing.T) {
	if got := flagNames(&cobra.Command{}); got != "(none)" {
		t.Fatalf("flagNames = %q, want (none)", got)
	}
}

// failingWriter always errors, to exercise the io error branches every
// output helper here otherwise never takes.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// TestSanitizeTerminalNeutralisesControlBytes is H1: every C0 control byte
// and DEL that is not a newline or tab must not survive sanitizeTerminal
// unchanged, since those are exactly the bytes a terminal could act on
// instead of print (most importantly ESC, which begins every ANSI escape
// sequence).
func TestSanitizeTerminalNeutralisesControlBytes(t *testing.T) {
	hostile := "clear\x1b[2Jscreen\rhome\x07bell\x7fdel\x00nul"
	got := sanitizeTerminal(hostile)
	if strings.ContainsAny(got, "\x1b\r\x07\x7f\x00") {
		t.Fatalf("sanitizeTerminal left a control byte in %q", got)
	}
}

// TestSanitizeTerminalPreservesFormatting is the flip side: a legitimate
// multi-line message body must keep its newlines and tabs, since those are
// ordinary formatting rather than something a terminal would act on, and
// indent() (cmd_inbox.go) relies on the newlines surviving to indent each
// line of the body.
func TestSanitizeTerminalPreservesFormatting(t *testing.T) {
	body := "line one\n\tindented line two\nline three"
	if got := sanitizeTerminal(body); got != body {
		t.Fatalf("sanitizeTerminal altered ordinary formatting:\n got:  %q\nwant: %q", got, body)
	}
}

func TestSanitizeTerminalLeavesOrdinaryTextAlone(t *testing.T) {
	plain := "frontend@storefront, claude-code, tool tier, café"
	if got := sanitizeTerminal(plain); got != plain {
		t.Fatalf("sanitizeTerminal altered plain text:\n got:  %q\nwant: %q", got, plain)
	}
}

// TestDashSanitisesBeforeCollapsing is H1: dash() is the single point nearly
// every optional daemon-provided field (harness, tier, state, cwd, an
// address) passes through on the way to a human terminal, so it has to
// neutralise control bytes itself rather than trust every call site to have
// done it already. A lone ESC is not whitespace, so without sanitising first
// it would have sailed through unmodified instead of collapsing to "-" or
// being replaced.
func TestDashSanitisesBeforeCollapsing(t *testing.T) {
	got := dash("tool\x1b[31m")
	if strings.Contains(got, "\x1b") {
		t.Fatalf("dash left a control byte in %q", got)
	}

	// A value that is nothing BUT control bytes still collapses to "-" once
	// sanitised, same as an empty or whitespace-only value always has.
	if got := dash(""); got != "-" {
		t.Fatalf(`dash("") = %q, want "-"`, got)
	}
}

func TestAggregateJoinsWithMiddleDot(t *testing.T) {
	var buf bytes.Buffer
	if err := aggregate(&buf, "3 agents", "1 blocked", "1 idle"); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if got, want := buf.String(), "3 agents · 1 blocked · 1 idle\n"; got != want {
		t.Fatalf("aggregate output = %q, want %q", got, want)
	}
}

// TestAggregateOmitsEmptyParts is the whole reason aggregate takes variadic
// parts rather than a caller-joined string: a conditional part a caller
// could not cleanly omit ahead of time (e.g. "" when there is nothing to
// say about it) must not leave a stray " · " in the line.
func TestAggregateOmitsEmptyParts(t *testing.T) {
	var buf bytes.Buffer
	if err := aggregate(&buf, "3 agents", "", "1 idle"); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if got, want := buf.String(), "3 agents · 1 idle\n"; got != want {
		t.Fatalf("aggregate output = %q, want %q", got, want)
	}
}

func TestAggregateNoParts(t *testing.T) {
	var buf bytes.Buffer
	if err := aggregate(&buf); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if got, want := buf.String(), "\n"; got != want {
		t.Fatalf("aggregate output = %q, want %q", got, want)
	}
}

func TestNextPrintsTheSuggestion(t *testing.T) {
	var buf bytes.Buffer
	if err := next(&buf, `tether send backend "..."`); err != nil {
		t.Fatalf("next: %v", err)
	}
	if got, want := buf.String(), "Next: tether send backend \"...\"\n"; got != want {
		t.Fatalf("next output = %q, want %q", got, want)
	}
}

func TestEmptyPrintsCountAndNext(t *testing.T) {
	var buf bytes.Buffer
	if err := empty(&buf, "agents", "tether register --as <name>"); err != nil {
		t.Fatalf("empty: %v", err)
	}
	want := "0 agents.\nNext: tether register --as <name>\n"
	if got := buf.String(); got != want {
		t.Fatalf("empty output = %q, want %q", got, want)
	}
}

func TestTruncateUnderLimitIsUnchanged(t *testing.T) {
	got, cut := truncate("short", 10)
	if cut {
		t.Fatal("truncate reported cutting a string shorter than the limit")
	}
	if got != "short" {
		t.Fatalf("truncate = %q, want %q", got, "short")
	}
}

func TestTruncateAtExactLimitIsUnchanged(t *testing.T) {
	got, cut := truncate("abcde", 5)
	if cut {
		t.Fatal("truncate reported cutting a string exactly at the limit")
	}
	if got != "abcde" {
		t.Fatalf("truncate = %q, want %q", got, "abcde")
	}
}

func TestTruncateOverLimitCuts(t *testing.T) {
	got, cut := truncate("abcdefghij", 4)
	if !cut {
		t.Fatal("truncate did not report cutting a string over the limit")
	}
	if got != "abcd" {
		t.Fatalf("truncate = %q, want %q", got, "abcd")
	}
}

// TestTruncateIsRuneSafe is the reason truncate cannot just slice bytes: a
// naive s[:max] on a string full of multi-byte runes can split one in half,
// producing invalid UTF-8. truncate must always cut on a rune boundary.
func TestTruncateIsRuneSafe(t *testing.T) {
	// Each "café" 'é' is two bytes but one rune; a byte-based cut at 5 would
	// split it. A rune-based cut at 5 must not.
	s := "café café café"
	got, cut := truncate(s, 5)
	if !cut {
		t.Fatal("truncate did not report cutting")
	}
	if !strings.HasPrefix(got, "café ") {
		t.Fatalf("truncate cut mid-rune: got %q", got)
	}
	if n := len([]rune(got)); n != 5 {
		t.Fatalf("truncate returned %d runes, want 5", n)
	}
}

func TestPrintJSONWriteFailure(t *testing.T) {
	if err := printJSON(failingWriter{}, map[string]string{"a": "b"}); err == nil {
		t.Fatal("printJSON with a failing writer: want error, got nil")
	}
}

func TestAggregateWriteFailure(t *testing.T) {
	if err := aggregate(failingWriter{}, "a"); err == nil {
		t.Fatal("aggregate with a failing writer: want error, got nil")
	}
}

func TestNextWriteFailure(t *testing.T) {
	if err := next(failingWriter{}, "tether ls"); err == nil {
		t.Fatal("next with a failing writer: want error, got nil")
	}
}

func TestEmptyWriteFailure(t *testing.T) {
	if err := empty(failingWriter{}, "agents", "tether register --as x"); err == nil {
		t.Fatal("empty with a failing writer: want error, got nil")
	}
}

func TestKeyValuesWriteFailure(t *testing.T) {
	if err := keyValues(failingWriter{}, [][2]string{{"state", "idle"}}); err == nil {
		t.Fatal("keyValues with a failing writer: want error, got nil")
	}
}

func TestIndentSkipsEmptyLines(t *testing.T) {
	got := indent("first\n\nthird", "  ")
	if want := "  first\n\n  third"; got != want {
		t.Fatalf("indent = %q, want %q", got, want)
	}
}

func TestUntrustedContentNoticeIsFactualNotImperative(t *testing.T) {
	// A light guard against the notice drifting into a command aimed at the
	// reader ("ignore", "do not", "you must") rather than a statement of
	// fact about where the content came from -- see the H1 requirement that
	// it read as data provenance, not an instruction.
	lower := strings.ToLower(untrustedContentNotice)
	for _, imperative := range []string{"ignore", "do not", "you must", "never "} {
		if strings.Contains(lower, imperative) {
			t.Fatalf("untrustedContentNotice reads as an instruction (%q found): %q", imperative, untrustedContentNotice)
		}
	}
	if untrustedContentNotice == "" {
		t.Fatal("untrustedContentNotice is empty")
	}
}
