package main

import (
	"bytes"
	"strings"
	"testing"
)

// run executes the root command with args and returns combined stdout.
func run(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}
	return out.String()
}

func TestBareCommandReportsState(t *testing.T) {
	// AXI principle 8: no args shows data, not usage. Principle 5: the empty
	// state is explicit, never silence.
	got := run(t)
	if !strings.Contains(got, "0 agents") {
		t.Errorf("bare command = %q, want it to report agent count", got)
	}
	if strings.Contains(got, "Usage:") {
		t.Errorf("bare command dumped usage text, want live state:\n%s", got)
	}
}

func TestVersion(t *testing.T) {
	if got := strings.TrimSpace(run(t, "version")); got != version {
		t.Errorf("version = %q, want %q", got, version)
	}
}

func TestUnknownFlagFails(t *testing.T) {
	// AXI principle 6: fail loud on unknown flags, never ignore one.
	cmd := newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--nope"})
	if err := cmd.Execute(); err == nil {
		t.Error("unknown flag accepted, want an error")
	}
}
