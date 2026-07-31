package main

import (
	"testing"
	"time"

	"github.com/praneethravuri/tether/internal/protocol"
)

func statusResult() protocol.StatusResult {
	return protocol.StatusResult{
		Agent: protocol.AgentView{
			Address:      "backend@storefront",
			Name:         "backend",
			Workspace:    "storefront",
			Harness:      "codex",
			State:        "blocked",
			StateSource:  "wait",
			StateAgeMS:   4000,
			StateDetail:  "parked in tether wait",
			Cwd:          "/repos/storefront/api",
			PID:          1234,
			Pending:      2,
			RegisteredAt: ago(2 * time.Hour),
			LastSeen:     ago(9 * time.Second),
		},
	}
}

func TestExplainOfSelf(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(statusResult()))

	mustRun(t, newExplainCmd(), "")

	params := decodeParams[protocol.StatusParams](t, d.registerThen(t, protocol.MethodExplain))
	if params.Name != "frontend" || params.Workspace != "storefront" {
		t.Fatalf("explain of %s@%s, want frontend@storefront (myself)",
			params.Name, params.Workspace)
	}
}

// TestExplainShowsStateSourceSeenAndDetail is the core contract for this
// command: all four fields the artifact's example shows must appear,
// carrying the evidence computeState produced daemon-side.
func TestExplainShowsStateSourceSeenAndDetail(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(statusResult()))

	r := mustRun(t, newExplainCmd(), "", "backend")

	params := decodeParams[protocol.StatusParams](t, d.only(t, protocol.MethodExplain))
	if params.Name != "backend" || params.Workspace != "storefront" {
		t.Fatalf("explain of %s@%s, want backend@storefront", params.Name, params.Workspace)
	}

	out := r.stdout
	requireContains(t, out, "backend@storefront", "stdout")
	requireContains(t, out, "codex", "stdout")
	requireContains(t, out, "state:", "stdout")
	requireContains(t, out, "blocked", "stdout")
	requireContains(t, out, "source:", "stdout")
	requireContains(t, out, "wait", "stdout")
	requireContains(t, out, "seen:", "stdout")
	requireContains(t, out, "4s ago", "stdout")
	requireContains(t, out, "detail:", "stdout")
	requireContains(t, out, "parked in tether wait", "stdout")
	requireContains(t, out, "2 messages", "stdout")
	requireContains(t, out, "registered:", "stdout")
	requireContains(t, out, "2h ago", "stdout")
	requireContains(t, out, "1234", "stdout")
	requireContains(t, out, "/repos/storefront/api", "stdout")
	requireNotContains(t, out, "WARNING", "stdout")
}

// TestExplainShowsDroppedOnlyWhenNonZero checks the "dropped: N" line
// appears when an agent has lost mail to the depth cap, and is omitted
// entirely -- no "dropped: 0" noise -- otherwise.
func TestExplainShowsDroppedOnlyWhenNonZero(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	res := statusResult()
	res.Agent.Dropped = 5
	newFakeDaemon(t, okHandler(res))

	r := mustRun(t, newExplainCmd(), "", "backend")
	requireContains(t, r.stdout, "dropped:", "stdout")
	requireContains(t, r.stdout, "5", "stdout")
}

func TestExplainOmitsDroppedWhenZero(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(statusResult()))

	r := mustRun(t, newExplainCmd(), "", "backend")
	requireNotContains(t, r.stdout, "dropped:", "stdout")
}

func TestExplainOfAnAgentInAnotherWorkspace(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(statusResult()))

	mustRun(t, newExplainCmd(), "", "backend@warehouse")

	params := decodeParams[protocol.StatusParams](t, d.only(t, protocol.MethodExplain))
	if params.Name != "backend" || params.Workspace != "warehouse" {
		t.Fatalf("explain of %s@%s, want backend@warehouse", params.Name, params.Workspace)
	}
}

func TestExplainJSON(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(statusResult()))

	r := mustRun(t, newExplainCmd(), "", "--json", "backend")

	var got protocol.StatusResult
	unmarshalJSON(t, r.stdout, &got)
	if got.Agent.Pending != 2 {
		t.Fatalf("pending = %d, want 2", got.Agent.Pending)
	}
	if got.Agent.Address != "backend@storefront" {
		t.Fatalf("agent = %+v", got.Agent)
	}
}

// TestExplainOfAnotherAgentDoesNotRegister documents a deliberate judgment
// call: checking another agent's state has never required the caller to
// have an identity of its own (see TestExplainRejectsABadAddress below,
// which never even resolves a self identity), so implicit registration only
// applies to the self path -- it must not start demanding --as/$TETHER_NAME
// just to look someone else up.
func TestExplainOfAnotherAgentDoesNotRegister(t *testing.T) {
	setIdentity(t, "", "storefront") // deliberately no self identity at all
	d := newFakeDaemon(t, okHandler(statusResult()))

	mustRun(t, newExplainCmd(), "", "backend")

	d.only(t, protocol.MethodExplain)
}

func TestExplainOfAnUnknownAgent(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, errHandler(protocol.CodeNotFound, "no agent named ghost in storefront"))

	r := run(t, newExplainCmd(), "", "ghost")
	if r.err == nil {
		t.Fatal("explain succeeded for an unknown agent")
	}
	requireContains(t, r.err.Error(), "no agent named ghost", "error")
	if got := r.exitCode(); got != exitGeneral {
		t.Fatalf("exit code = %d, want %d", got, exitGeneral)
	}
}

func TestExplainRejectsABadAddress(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(statusResult()))

	r := run(t, newExplainCmd(), "", "@warehouse")
	if r.err == nil {
		t.Fatal("explain accepted an address with no name")
	}
	if n := len(d.requests()); n != 0 {
		t.Fatalf("a bad request reached the daemon %d times", n)
	}
}

func TestExplainWithoutADaemonExitsThree(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	r := run(t, newExplainCmd(), "")
	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
}

// TestStatusIsAnAliasForExplain proves the old command name still works and
// produces identical output.
func TestStatusIsAnAliasForExplain(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(statusResult()))
	explainOut := mustRun(t, newExplainCmd(), "", "backend").stdout

	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(statusResult()))
	statusOut := mustRun(t, newRootCmd(), "", "status", "backend").stdout

	if explainOut != statusOut {
		t.Fatalf("status alias produced different output:\nexplain:\n%s\nstatus:\n%s", explainOut, statusOut)
	}
}
