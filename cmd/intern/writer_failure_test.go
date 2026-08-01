package main

import "testing"

// These exercise the io-error branches every render function otherwise never
// takes, using failingWriter (output_test.go) wired directly via SetOut.

func TestRunLs_WriteFailure(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(agents()))

	cmd := newLsCmd()
	cmd.SetOut(failingWriter{})
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("ls with a failing writer: want error, got nil")
	}
}

func TestRunDoctor_WriteFailure(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(agents()))

	cmd := newDoctorCmd()
	cmd.SetOut(failingWriter{})
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("doctor with a failing writer: want error, got nil")
	}
}

func TestRunInbox_WriteFailure(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(twoMessages()))

	cmd := newInboxCmd()
	cmd.SetOut(failingWriter{})
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("inbox with a failing writer: want error, got nil")
	}
}

func TestNormaliseKind_CaseAndWhitespace(t *testing.T) {
	got, err := normaliseKind("  HANDOFF  ")
	if err != nil {
		t.Fatalf("normaliseKind: %v", err)
	}
	if got != kindHandoff {
		t.Fatalf("normaliseKind = %q, want %q", got, kindHandoff)
	}
}

func TestDescribeBodySource(t *testing.T) {
	if got := describeBodySource("-"); got != "stdin" {
		t.Fatalf("describeBodySource(-) = %q, want stdin", got)
	}
	if got := describeBodySource("/tmp/notes.md"); got != "/tmp/notes.md" {
		t.Fatalf("describeBodySource(path) = %q, want the path unchanged", got)
	}
}
