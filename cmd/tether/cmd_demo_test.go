package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDemoEnvIsolatesFromRealHome(t *testing.T) {
	dir := "/tmp/tether-demo-test-xyz"
	env := demoEnv(dir)

	want := []string{
		"HOME=" + dir,
		"TETHER_SOCK=" + dir + "/sock",
		"TETHER_DB=" + dir + "/tether.db",
	}
	for _, w := range want {
		found := false
		for _, e := range env {
			if e == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("demoEnv(%q) = %v, missing %q", dir, env, w)
		}
	}
	if realHome := os.Getenv("HOME"); realHome != "" {
		for _, e := range env {
			if e == "HOME="+realHome {
				t.Fatalf("demoEnv leaked the real $HOME (%q) into the isolated environment", realHome)
			}
		}
	}
}

// demoSteps must register both agents before either sends, and every send
// must be answered by a wait+inbox on the receiving side -- otherwise the
// transcript prints a handoff nobody ever picks up.
func TestDemoStepsShapeIsARealHandoff(t *testing.T) {
	steps := demoSteps()
	if len(steps) == 0 {
		t.Fatal("demoSteps returned no steps")
	}

	registered := map[string]bool{}
	for i, s := range steps {
		if s.args[0] == "register" {
			registered[s.agent] = true
		}
		if s.args[0] == "send" && !registered[s.agent] {
			t.Fatalf("step %d: %s sends before registering", i, s.agent)
		}
	}
	if !registered["frontend"] || !registered["backend"] {
		t.Fatalf("both agents must register: %+v", registered)
	}

	var sawHandoff, sawAnswer bool
	for _, s := range steps {
		if s.args[0] != "send" {
			continue
		}
		for i, a := range s.args {
			if a != "--kind" || i+1 >= len(s.args) {
				continue
			}
			switch s.args[i+1] {
			case "handoff":
				sawHandoff = true
			case "answer":
				sawAnswer = true
			}
		}
	}
	if !sawHandoff || !sawAnswer {
		t.Fatalf("expected both a handoff and an answer send, got handoff=%v answer=%v", sawHandoff, sawAnswer)
	}
}

func TestDisplayArgsQuotesOnlyArgsWithSpaces(t *testing.T) {
	got := displayArgs([]string{"send", "backend", "hello there", "--kind", "note"})
	want := `send backend "hello there" --kind note`
	if got != want {
		t.Fatalf("displayArgs = %q, want %q", got, want)
	}
}

func TestDialableWithinTimesOutOnADeadSocket(t *testing.T) {
	start := time.Now()
	ok := dialableWithin("/tmp/tether-demo-test-no-such-socket", 60*time.Millisecond)
	elapsed := time.Since(start)

	if ok {
		t.Fatal("dialableWithin reported a dead socket as dialable")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("dialableWithin took %s to give up on a 60ms timeout", elapsed)
	}
}

// A cancelled context (Ctrl-C) must surface as context.Canceled, never a
// silent nil that would let runDemo print "demo complete" after an
// interruption -- see runDemo's errors.Is(err, context.Canceled) branch.
func TestRunDemoStepsReturnsContextErrorWhenAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runDemoSteps(ctx, io.Discard, "/nonexistent-tether-binary-for-test", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runDemoSteps error = %v, want context.Canceled", err)
	}
}

func TestDemoLongDoesNotMentionAnySpecificHarness(t *testing.T) {
	// This tool stays harness-agnostic: the demo's own two agents must not
	// be described as Claude Code, Codex, or any other real harness.
	for _, bad := range []string{"Claude", "Codex", "Gemini", "Copilot"} {
		if strings.Contains(demoLong, bad) {
			t.Fatalf("demoLong mentions %q; demo must stay harness-agnostic", bad)
		}
	}
}
