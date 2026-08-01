package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/praneethravuri/tether/internal/hooks/claudecode"
	"github.com/praneethravuri/tether/internal/protocol"
)

// hooksTestSession pins $HOME (so the lock/budget state dir is a temp
// directory, never the real ~/.tether) and $CLAUDE_CODE_SESSION_ID (so the
// single-flight/budget key is deterministic) -- set directly rather than via
// $TETHER_SESSION_ID because detectHarness checks CLAUDE_CODE_SESSION_ID
// first, and this test process's own real ambient value (it is itself
// running under Claude Code) would otherwise win.
func hooksTestSession(t *testing.T) (session string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	const sess = "sess-hooks-test"
	t.Setenv("CLAUDE_CODE_SESSION_ID", sess)
	return sess
}

func TestHooksRunStopDeliversMailAsABlockingExit(t *testing.T) {
	hooksTestSession(t)
	setIdentity(t, "frontend", "storefront")

	d := newFakeDaemon(t, func(req protocol.Request) protocol.Response {
		switch req.Method {
		case protocol.MethodRegister:
			return protocol.OK(req.ID, protocol.RegisterResult{Created: true})
		case protocol.MethodWait:
			return protocol.OK(req.ID, protocol.WaitResult{Pending: 1})
		case protocol.MethodInbox:
			return protocol.OK(req.ID, protocol.InboxResult{
				Messages: []protocol.MessageView{{From: "backend@storefront", Kind: "note", Body: "ship it"}},
			})
		default:
			t.Errorf("unexpected method %s", req.Method)
			return protocol.OK(req.ID, nil)
		}
	})

	r := run(t, newHooksRunStopCmd(), "", "--timeout", "1s")

	if got := r.exitCode(); got != 2 {
		t.Fatalf("exit code = %d, want 2 (block the stop)", got)
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty: a Stop-hook block carries its reason on stderr", r.stdout)
	}
	kind, body, ok := claudecode.ParseEnvelope(r.stderr)
	if !ok {
		t.Fatalf("stderr is not a tether envelope: %q", r.stderr)
	}
	if kind != claudecode.KindMail {
		t.Fatalf("envelope kind = %q, want %q", kind, claudecode.KindMail)
	}
	if !strings.Contains(body, "ship it") || !strings.Contains(body, "backend@storefront") {
		t.Fatalf("envelope body = %q, want it to carry the delivered message", body)
	}

	if n := d.countMethod(protocol.MethodInbox); n != 1 {
		t.Fatalf("daemon received %d inbox calls, want exactly 1 (mail must be drained, not left pending)", n)
	}
}

func TestHooksRunStopFailsOpenWithoutADaemon(t *testing.T) {
	hooksTestSession(t)
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	r := run(t, newHooksRunStopCmd(), "", "--timeout", "200ms")

	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0: an unreachable daemon must fail open, not block the stop", got)
	}
	if r.stderr != "" {
		t.Fatalf("stderr = %q, want empty", r.stderr)
	}
}

func TestHooksRunStopFailsOpenWhenNoMailIsPending(t *testing.T) {
	hooksTestSession(t)
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.WaitResult{TimedOut: true}))

	r := run(t, newHooksRunStopCmd(), "", "--timeout", "200ms")

	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0: nothing to deliver", got)
	}
}

// TestHooksRunStopSecondConcurrentFiringExitsQuietly is the single-flight
// contract end to end: a lock already held for this session must make a
// second invocation back off without even contacting the daemon.
func TestHooksRunStopSecondConcurrentFiringExitsQuietly(t *testing.T) {
	session := hooksTestSession(t)
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.WaitResult{Pending: 1}))

	stateDir, err := hooksStateDir()
	if err != nil {
		t.Fatalf("hooksStateDir: %v", err)
	}
	release, ok, err := claudecode.TryLock(filepath.Join(stateDir, "locks"), "stop-"+session)
	if err != nil || !ok {
		t.Fatalf("seed the lock: ok=%v err=%v", ok, err)
	}
	defer release()

	r := run(t, newHooksRunStopCmd(), "", "--timeout", "200ms")

	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0", got)
	}
	if n := len(d.requests()); n != 0 {
		t.Fatalf("daemon received %d requests, want 0: a locked-out invocation must never call out", n)
	}
}

// TestHooksRunStopBudgetCapStopsBlockingWithoutContactingTheDaemon proves
// the self-tracked budget, not stop_hook_active, is what bounds consecutive
// blocks: once the cap is already spent, run-stop gives up before even
// asking the daemon whether mail is pending.
func TestHooksRunStopBudgetCapStopsBlockingWithoutContactingTheDaemon(t *testing.T) {
	session := hooksTestSession(t)
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.WaitResult{Pending: 1}))

	stateDir, err := hooksStateDir()
	if err != nil {
		t.Fatalf("hooksStateDir: %v", err)
	}
	budgetDir := filepath.Join(stateDir, "budget")
	key := "stop-" + session
	for range hookStopBudgetCap {
		if err := claudecode.IncrementBudget(budgetDir, key); err != nil {
			t.Fatalf("seed budget: %v", err)
		}
	}

	r := run(t, newHooksRunStopCmd(), "", "--timeout", "200ms")

	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0: the budget is spent, so the stop must proceed", got)
	}
	if n := len(d.requests()); n != 0 {
		t.Fatalf("daemon received %d requests, want 0: a spent budget must give up before asking", n)
	}
	if got := claudecode.LoadBudget(budgetDir, key); got != 0 {
		t.Fatalf("budget after giving up = %d, want reset to 0", got)
	}
}

// TestHooksRunStopResetsBudgetOnceMailStopsArriving proves the counter is
// per-chain, not permanent: once a stop is genuinely allowed through, the
// next mail-triggered chain gets the full budget again.
func TestHooksRunStopResetsBudgetOnceMailStopsArriving(t *testing.T) {
	session := hooksTestSession(t)
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.WaitResult{TimedOut: true}))

	stateDir, err := hooksStateDir()
	if err != nil {
		t.Fatalf("hooksStateDir: %v", err)
	}
	budgetDir := filepath.Join(stateDir, "budget")
	key := "stop-" + session
	if err := claudecode.IncrementBudget(budgetDir, key); err != nil {
		t.Fatalf("seed budget: %v", err)
	}

	run(t, newHooksRunStopCmd(), "", "--timeout", "200ms")

	if got := claudecode.LoadBudget(budgetDir, key); got != 0 {
		t.Fatalf("budget after a no-mail stop = %d, want reset to 0", got)
	}
}

func TestHooksRunSessionStartInjectsPendingMail(t *testing.T) {
	hooksTestSession(t)
	setIdentity(t, "frontend", "storefront")

	newFakeDaemon(t, func(req protocol.Request) protocol.Response {
		switch req.Method {
		case protocol.MethodRegister:
			return protocol.OK(req.ID, protocol.RegisterResult{Created: true})
		case protocol.MethodInbox:
			return protocol.OK(req.ID, protocol.InboxResult{
				Messages: []protocol.MessageView{{From: "backend@storefront", Kind: "note", Body: "welcome back"}},
			})
		default:
			t.Errorf("unexpected method %s", req.Method)
			return protocol.OK(req.ID, nil)
		}
	})

	r := mustRun(t, newHooksRunSessionStartCmd(), "")

	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	unmarshalJSON(t, r.stdout, &out)
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName = %q, want %q", out.HookSpecificOutput.HookEventName, "SessionStart")
	}
	kind, body, ok := claudecode.ParseEnvelope(out.HookSpecificOutput.AdditionalContext)
	if !ok {
		t.Fatalf("additionalContext is not a tether envelope: %q", out.HookSpecificOutput.AdditionalContext)
	}
	if kind != claudecode.KindMail || !strings.Contains(body, "welcome back") {
		t.Fatalf("additionalContext = %q, want the delivered mail", out.HookSpecificOutput.AdditionalContext)
	}
}

func TestHooksRunSessionStartSilentWhenNothingIsPending(t *testing.T) {
	hooksTestSession(t)
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{}))

	r := mustRun(t, newHooksRunSessionStartCmd(), "")

	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty: nothing to inject", r.stdout)
	}
}

func TestHooksRunSessionStartFailsOpenWithoutADaemon(t *testing.T) {
	hooksTestSession(t)
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	r := run(t, newHooksRunSessionStartCmd(), "")

	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0", got)
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty", r.stdout)
	}
}
