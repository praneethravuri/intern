package main

import (
	"strings"
	"testing"
	"time"

	"github.com/praneethravuri/tether/pkg/protocol"
)

func agents() protocol.WhoResult {
	return protocol.WhoResult{Agents: []protocol.AgentView{
		{
			Address:      "frontend@storefront",
			Name:         "frontend",
			Workspace:    "storefront",
			Harness:      "claude-code",
			State:        "quiet",
			StateSource:  "heartbeat",
			StateDetail:  "last ran tether inbox",
			Cwd:          "/repos/storefront/web",
			PID:          4242,
			Pending:      1,
			RegisteredAt: ago(2 * time.Hour),
			LastSeen:     ago(12 * time.Second),
		},
		{
			Address:      "legacy@storefront",
			Name:         "legacy",
			Workspace:    "storefront",
			Harness:      "unknown",
			State:        "blocked",
			StateSource:  "wait",
			StateDetail:  "parked in tether wait",
			PID:          77,
			RegisteredAt: ago(time.Hour),
			LastSeen:     ago(3 * time.Minute),
		},
	}}
}

func TestLsHappyPath(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(agents()))

	r := mustRun(t, newLsCmd(), "")

	params := decodeParams[protocol.WhoParams](t, d.only(t, protocol.MethodLs))
	if params.Workspace != "storefront" {
		t.Fatalf("params = %+v, want workspace storefront", params)
	}

	out := r.stdout
	requireContains(t, out, "2 agents", "stdout")
	requireContains(t, out, "1 blocked", "stdout")
	requireContains(t, out, "1 quiet", "stdout")
	requireContains(t, out, "NAME", "stdout")
	requireContains(t, out, "HARNESS", "stdout")
	requireContains(t, out, "STATE", "stdout")
	requireContains(t, out, "PENDING", "stdout")
	requireContains(t, out, "LAST SEEN", "stdout")
	requireContains(t, out, "frontend@storefront", "stdout")
	requireContains(t, out, "12s ago", "stdout")
	requireContains(t, out, "3m ago", "stdout")
	requireContains(t, out, "Next: tether send frontend@storefront", "stdout")

	assertColumnsAligned(t, out)
}

// TestLsAggregateOmitsZeroCounts confirms the summary line only names states
// that actually occur, matching "3 agents · 1 blocked · 1 quiet" (no "0
// working", no "0 gone").
func TestLsAggregateOmitsZeroCounts(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(agents()))

	r := mustRun(t, newLsCmd(), "")
	requireContains(t, r.stdout, "2 agents · 1 blocked · 1 quiet", "stdout")
	requireNotContains(t, r.stdout, "0 working", "stdout")
	requireNotContains(t, r.stdout, "0 gone", "stdout")
	requireNotContains(t, r.stdout, "0 unknown", "stdout")
}

// assertColumnsAligned checks the table really is a table: the header and
// every row start their second column at the same offset. It stops at the
// first blank line after the header, so a later "Next: ..." suggestion line
// (which also contains an "@" address) is never mistaken for a table row.
func assertColumnsAligned(t *testing.T, out string) {
	t.Helper()

	var rows []string
	inTable := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "NAME") {
			inTable = true
			rows = append(rows, line)
			continue
		}
		if !inTable {
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		rows = append(rows, line)
	}
	if len(rows) < 2 {
		t.Fatalf("expected a header and at least one row in:\n%s", out)
	}

	want := secondColumnStart(rows[0])
	for _, row := range rows[1:] {
		if got := secondColumnStart(row); got != want {
			t.Fatalf("second column starts at %d in %q but at %d in the header:\n%s",
				got, row, want, out)
		}
	}
}

// secondColumnStart is the index at which the second column of a tabwriter row
// begins, which is the first non-space after the first run of two or more.
func secondColumnStart(line string) int {
	i := strings.Index(line, "  ")
	if i < 0 {
		return -1
	}
	for i < len(line) && line[i] == ' ' {
		i++
	}
	return i
}

func TestLsAll(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(agents()))

	mustRun(t, newLsCmd(), "", "--all")

	// --all skips workspace resolution entirely, so the daemon sees every
	// workspace rather than being told to filter by one.
	params := decodeParams[protocol.WhoParams](t, d.only(t, protocol.MethodLs))
	if params.Workspace != "" {
		t.Fatalf("workspace = %q, want empty", params.Workspace)
	}
}

func TestLsEmpty(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.WhoResult{}))

	r := mustRun(t, newLsCmd(), "")

	if strings.TrimSpace(r.stdout) != "0 agents.\nNext: tether register --as <name>" {
		t.Fatalf("stdout = %q, want the empty-fleet message with a Next suggestion", r.stdout)
	}
	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0", got)
	}
}

func TestLsJSON(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(agents()))

	r := mustRun(t, newLsCmd(), "", "--json")

	var got protocol.WhoResult
	unmarshalJSON(t, r.stdout, &got)
	if len(got.Agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(got.Agents))
	}
	if got.Agents[0].Address != "frontend@storefront" || got.Agents[0].PID != 4242 {
		t.Fatalf("agent = %+v", got.Agents[0])
	}
}

func TestLsJSONEmptyIsAnArray(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.WhoResult{}))

	r := mustRun(t, newLsCmd(), "", "--json")
	requireContains(t, r.stdout, `"agents": []`, "stdout")
}

func TestLsUsesTheWorkspaceFlag(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.WhoResult{}))

	mustRun(t, newLsCmd(), "", "--workspace", "warehouse")

	params := decodeParams[protocol.WhoParams](t, d.only(t, protocol.MethodLs))
	if params.Workspace != "warehouse" {
		t.Fatalf("workspace = %q, want warehouse", params.Workspace)
	}
}

// ls does not need a registered name: it must work before you register.
func TestLsWorksWithoutAName(t *testing.T) {
	setIdentity(t, "", "storefront")
	newFakeDaemon(t, okHandler(agents()))

	r := mustRun(t, newLsCmd(), "")
	requireContains(t, r.stdout, "frontend@storefront", "stdout")
}

func TestLsWithoutADaemonExitsThree(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	r := run(t, newLsCmd(), "")
	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
	requireContains(t, r.err.Error(), "no daemon running", "error")
}

func TestLsSurvivesAMalformedResponse(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newRawDaemon(t, []byte("{{{"))

	r := run(t, newLsCmd(), "")
	if r.err == nil {
		t.Fatal("ls succeeded against a daemon speaking garbage")
	}
	requireContains(t, r.err.Error(), "malformed response", "error")
}

// Missing fields must not collapse the table.
func TestLsRendersSparseAgents(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.WhoResult{Agents: []protocol.AgentView{
		{Name: "bare", Workspace: "storefront"},
	}}))

	r := mustRun(t, newLsCmd(), "")
	requireContains(t, r.stdout, "bare@storefront", "stdout")
	requireContains(t, r.stdout, "unknown", "stdout")
}

// TestLsRendersDroppedInPendingColumn checks the PENDING column shows
// "N (+M dropped)" once an agent has actually lost mail to the depth cap,
// and plain "N" for everyone else, in the same table.
func TestLsRendersDroppedInPendingColumn(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	res := agents()
	res.Agents[0].Pending = 500
	res.Agents[0].Dropped = 5
	newFakeDaemon(t, okHandler(res))

	r := mustRun(t, newLsCmd(), "")
	requireContains(t, r.stdout, "500 (+5 dropped)", "stdout")

	// bob (legacy) has no drops: it must render as a plain count, not
	// "0 (+0 dropped)".
	requireNotContains(t, r.stdout, "(+0 dropped)", "stdout")
}

// TestWhoIsGone proves the old command name is not silently accepted as an
// alias any more -- tether ls is the only spelling now.
func TestWhoIsGone(t *testing.T) {
	r := run(t, newRootCmd(), "", "who")
	if r.err == nil {
		t.Fatal("who was accepted; it should be an unknown command")
	}
	requireContains(t, r.err.Error(), "unknown command", "error")
}
