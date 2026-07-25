package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/praneethravuri/tether/pkg/protocol"
)

// run executes the root command with args and returns combined stdout.
// It points TETHER_SOCK at a path with no daemon listening, so tests never
// depend on (or interfere with) a real tetherd running on the machine.
func run(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("TETHER_SOCK", filepath.Join(t.TempDir(), "sock"))
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

func TestRegisterCommand(t *testing.T) {
	t.Setenv("TETHER_SOCK", fakeDaemon(t, protocol.Response{ID: "cli-1", Result: "registered"}))

	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"register"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute register: %v", err)
	}

	if !strings.Contains(out.String(), "Successfully registered") {
		t.Errorf("register output = %q, want it to confirm registration", out.String())
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
