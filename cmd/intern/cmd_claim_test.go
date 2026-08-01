package main

import (
	"testing"
	"time"

	"github.com/praneethravuri/intern/internal/protocol"
)

// future renders a timestamp d from now, the way the daemon would.
func future(d time.Duration) string {
	return time.Now().Add(d).UTC().Format(time.RFC3339)
}

func TestClaimHappyPath(t *testing.T) {
	setIdentity(t, "", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.ClaimResult{
		LeaseID: "abc123", Holder: "alice", ExpiresAt: future(15 * time.Minute),
	}))

	r := mustRun(t, newClaimCmd(), "", "src/main.go", "--holder", "alice")

	params := decodeParams[protocol.ClaimParams](t, d.only(t, protocol.MethodClaim))
	if params.Workspace != "storefront" || params.Key != "src/main.go" || params.Holder != "alice" {
		t.Fatalf("params = %+v", params)
	}
	if params.OwnerPID <= 0 {
		t.Fatalf("params.OwnerPID = %d, want a positive pid", params.OwnerPID)
	}

	requireContains(t, r.stdout, "claimed src/main.go in storefront", "stdout")
	requireContains(t, r.stdout, "abc123", "stdout")
	requireContains(t, r.stdout, "Next: intern release src/main.go --if-claim-id abc123", "stdout")
}

func TestClaimRenewedAndReclaimedVerbs(t *testing.T) {
	setIdentity(t, "", "storefront")
	newFakeDaemon(t, okHandler(protocol.ClaimResult{
		LeaseID: "l1", ExpiresAt: future(time.Minute), Renewed: true,
	}))
	r := mustRun(t, newClaimCmd(), "", "k")
	requireContains(t, r.stdout, "renewed k in storefront", "stdout")

	setIdentity(t, "", "storefront")
	newFakeDaemon(t, okHandler(protocol.ClaimResult{
		LeaseID: "l2", ExpiresAt: future(time.Minute), Reclaimed: true,
	}))
	r2 := mustRun(t, newClaimCmd(), "", "k")
	requireContains(t, r2.stdout, "reclaimed", "stdout")
	requireContains(t, r2.stdout, "k in storefront", "stdout")
}

func TestClaimJSONOutput(t *testing.T) {
	setIdentity(t, "", "storefront")
	newFakeDaemon(t, okHandler(protocol.ClaimResult{
		LeaseID: "l1", ExpiresAt: future(time.Minute),
	}))

	r := mustRun(t, newClaimCmd(), "", "k", "--json")
	var got protocol.ClaimResult
	unmarshalJSON(t, r.stdout, &got)
	if got.LeaseID != "l1" {
		t.Fatalf("got = %+v", got)
	}
}

func TestClaimConflictExitsFive(t *testing.T) {
	setIdentity(t, "", "storefront")
	newFakeDaemon(t, errHandler(protocol.CodeConflict, "claim already held by a live owner: storefront/k"))

	r := run(t, newClaimCmd(), "", "k")
	if r.exitCode() != exitConflict {
		t.Fatalf("exit code = %d, want %d (exitConflict)", r.exitCode(), exitConflict)
	}
	requireContains(t, r.err.Error(), "held by a live process", "error")
	requireContains(t, r.err.Error(), "intern claims", "error")
}

func TestReleaseHappyPath(t *testing.T) {
	setIdentity(t, "", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.ReleaseResult{}))

	r := mustRun(t, newReleaseCmd(), "", "k", "--if-claim-id", "abc123")

	params := decodeParams[protocol.ReleaseParams](t, d.only(t, protocol.MethodRelease))
	if params.Workspace != "storefront" || params.Key != "k" || params.LeaseID != "abc123" {
		t.Fatalf("params = %+v", params)
	}
	requireContains(t, r.stdout, "released k in storefront", "stdout")
}

func TestReleaseMissingLeaseIDFailsWithoutCallingTheDaemon(t *testing.T) {
	setIdentity(t, "", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.ReleaseResult{}))

	r := run(t, newReleaseCmd(), "", "k")
	if r.exitCode() != exitGeneral {
		t.Fatalf("exit code = %d, want %d (exitGeneral)", r.exitCode(), exitGeneral)
	}
	requireContains(t, r.err.Error(), "--if-claim-id is required", "error")
	if len(d.requests()) != 0 {
		t.Fatalf("daemon received %d requests, want 0 (validation should fail client-side)", len(d.requests()))
	}
}

// TestReleaseStaleLeaseIDExitsConflict is the CLI-facing half of the ABA
// guard: a daemon-reported mismatch (CodeConflict) must exit 5 with an
// actionable message, not a generic failure.
func TestReleaseStaleLeaseIDExitsConflict(t *testing.T) {
	setIdentity(t, "", "storefront")
	newFakeDaemon(t, errHandler(protocol.CodeConflict, "claim lease id does not match the current lease: storefront/k"))

	r := run(t, newReleaseCmd(), "", "k", "--if-claim-id", "stale")
	if r.exitCode() != exitConflict {
		t.Fatalf("exit code = %d, want %d (exitConflict)", r.exitCode(), exitConflict)
	}
	requireContains(t, r.err.Error(), "does not match its current lease", "error")
}

func TestReleaseUnknownClaimExitsGeneral(t *testing.T) {
	setIdentity(t, "", "storefront")
	newFakeDaemon(t, errHandler(protocol.CodeNotFound, "no such claim: storefront/k"))

	r := run(t, newReleaseCmd(), "", "k", "--if-claim-id", "abc123")
	if r.exitCode() != exitGeneral {
		t.Fatalf("exit code = %d, want %d (exitGeneral)", r.exitCode(), exitGeneral)
	}
	requireContains(t, r.err.Error(), "no such claim", "error")
}

func claims() protocol.ClaimsResult {
	return protocol.ClaimsResult{Claims: []protocol.ClaimView{
		{
			Workspace: "storefront", Key: "src/main.go", OwnerPID: 4242,
			Holder: "alice", Status: "held",
			LeasedAt: future(-time.Minute), ExpiresAt: future(14 * time.Minute),
		},
		{
			Workspace: "storefront", Key: "README.md", OwnerPID: 77,
			Status:   "gone",
			LeasedAt: future(-time.Hour), ExpiresAt: future(-45 * time.Minute),
		},
	}}
}

func TestClaimsHappyPath(t *testing.T) {
	setIdentity(t, "", "storefront")
	d := newFakeDaemon(t, okHandler(claims()))

	r := mustRun(t, newClaimsCmd(), "")

	params := decodeParams[protocol.ClaimsParams](t, d.only(t, protocol.MethodClaims))
	if params.Workspace != "storefront" {
		t.Fatalf("params = %+v, want workspace storefront", params)
	}

	out := r.stdout
	requireContains(t, out, "WORKSPACE", "stdout")
	requireContains(t, out, "STATUS", "stdout")
	requireContains(t, out, "src/main.go", "stdout")
	requireContains(t, out, "held", "stdout")
	requireContains(t, out, "gone", "stdout")
	requireContains(t, out, "alice", "stdout")
}

func TestClaimsEmpty(t *testing.T) {
	setIdentity(t, "", "storefront")
	newFakeDaemon(t, okHandler(protocol.ClaimsResult{}))

	r := mustRun(t, newClaimsCmd(), "")
	requireContains(t, r.stdout, "0 claims", "stdout")
	requireContains(t, r.stdout, "Next: intern claim <key>", "stdout")
}

func TestClaimsJSONOutputIsNeverNull(t *testing.T) {
	setIdentity(t, "", "storefront")
	newFakeDaemon(t, okHandler(protocol.ClaimsResult{}))

	r := mustRun(t, newClaimsCmd(), "", "--json")
	requireContains(t, r.stdout, `"claims": []`, "stdout")
}

func TestClaimsAllPassesEmptyWorkspace(t *testing.T) {
	setIdentity(t, "", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.ClaimsResult{}))

	mustRun(t, newClaimsCmd(), "", "--all")

	params := decodeParams[protocol.ClaimsParams](t, d.only(t, protocol.MethodClaims))
	if params.Workspace != "" {
		t.Fatalf("params.Workspace = %q, want empty (--all)", params.Workspace)
	}
}
