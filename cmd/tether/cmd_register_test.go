package main

import (
	"os"
	"testing"

	"github.com/praneethravuri/tether/pkg/protocol"
)

func TestRegisterHappyPath(t *testing.T) {
	setIdentity(t, "", "storefront")
	clearHarnessEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-123")

	d := newFakeDaemon(t, okHandler(protocol.RegisterResult{
		Address: "frontend@storefront",
		Harness: "claude-code",
		Created: true,
	}))

	r := mustRun(t, newRegisterCmd(), "", "--as", "frontend")

	got := d.only(t, protocol.MethodRegister)
	params := decodeParams[protocol.RegisterParams](t, got)

	if params.Name != "frontend" || params.Workspace != "storefront" {
		t.Fatalf("registered %s@%s, want frontend@storefront", params.Name, params.Workspace)
	}
	if params.Harness != harnessClaudeCode || params.SessionID != "sess-123" {
		t.Fatalf("harness = %q session = %q, want claude-code / sess-123",
			params.Harness, params.SessionID)
	}
	if params.PID != os.Getppid() {
		t.Fatalf("pid = %d, want the parent pid %d (our own pid is useless: the CLI exits)",
			params.PID, os.Getppid())
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if params.Cwd != cwd {
		t.Fatalf("cwd = %q, want %q", params.Cwd, cwd)
	}

	requireContains(t, r.stdout, "registered frontend@storefront", "stdout")
	requireContains(t, r.stdout, "claude-code", "stdout")
}

func TestRegisterUsesTheNameFromTheEnvironment(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)

	d := newFakeDaemon(t, okHandler(protocol.RegisterResult{
		Address: "frontend@storefront", Harness: "unknown", Created: true,
	}))

	mustRun(t, newRegisterCmd(), "")

	params := decodeParams[protocol.RegisterParams](t, d.only(t, protocol.MethodRegister))
	if params.Name != "frontend" {
		t.Fatalf("name = %q, want frontend", params.Name)
	}
}

// A refresh of an already-registered name (Created=false) is worded
// differently from a brand-new registration, so the caller can tell the two
// apart without reading --json.
func TestRegisterRefreshWording(t *testing.T) {
	setIdentity(t, "", "storefront")
	clearHarnessEnv(t)

	newFakeDaemon(t, okHandler(protocol.RegisterResult{
		Address: "frontend@storefront", Harness: "unknown", Created: false,
	}))

	r := mustRun(t, newRegisterCmd(), "", "--as", "frontend")
	requireContains(t, r.stdout, "refreshed registration for frontend@storefront", "stdout")
	requireNotContains(t, r.stdout, "registered frontend", "stdout")
}

func TestRegisterJSON(t *testing.T) {
	setIdentity(t, "", "storefront")
	clearHarnessEnv(t)

	want := protocol.RegisterResult{
		Address: "frontend@storefront", Harness: "codex", Created: true,
	}
	newFakeDaemon(t, okHandler(want))

	r := mustRun(t, newRegisterCmd(), "", "--as", "frontend", "--json")

	var got protocol.RegisterResult
	unmarshalJSON(t, r.stdout, &got)
	if got != want {
		t.Fatalf("json result = %+v, want %+v", got, want)
	}
}

func TestRegisterConflictExitsFive(t *testing.T) {
	setIdentity(t, "", "storefront")
	clearHarnessEnv(t)

	newFakeDaemon(t, errHandler(protocol.CodeConflict,
		"frontend@storefront is held by pid 4242 (claude-code, last seen 2s ago)"))

	r := run(t, newRegisterCmd(), "", "--as", "frontend")
	if r.err == nil {
		t.Fatal("register succeeded despite a conflict")
	}
	if got := r.exitCode(); got != exitConflict {
		t.Fatalf("exit code = %d, want %d", got, exitConflict)
	}

	msg := r.err.Error()
	requireContains(t, msg, "frontend@storefront", "error")
	requireContains(t, msg, "pid 4242", "error")
	requireContains(t, msg, "tether ls", "error")
}

func TestRegisterWithoutADaemonExitsThree(t *testing.T) {
	setIdentity(t, "", "storefront")
	clearHarnessEnv(t)
	noDaemon(t)

	r := run(t, newRegisterCmd(), "", "--as", "frontend")
	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
	requireContains(t, r.err.Error(), "no daemon running", "error")
}

// resolveSelf no longer errors, or derives anything, when no name is given
// anywhere -- register sends an empty Name over the wire, and the daemon
// resolves or mints one (see internal/daemon's mintName). The CLI reports
// whatever name comes back.
func TestRegisterWithoutANameLetsTheDaemonResolveOne(t *testing.T) {
	setIdentity(t, "", "storefront")
	clearHarnessEnv(t)
	d := newFakeDaemon(t, okHandler(protocol.RegisterResult{
		Address: "claude-code-3f1a@storefront", Name: "claude-code-3f1a", Created: true,
	}))

	r := mustRun(t, newRegisterCmd(), "")

	params := decodeParams[protocol.RegisterParams](t, d.only(t, protocol.MethodRegister))
	if params.Name != "" {
		t.Fatalf("register sent Name %q, want empty (resolve-or-mint)", params.Name)
	}
	requireContains(t, r.stdout, "registered claude-code-3f1a@storefront", "stdout")
}
