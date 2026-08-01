package main

import (
	"testing"

	"github.com/praneethravuri/intern/internal/protocol"
)

// rootHandler answers MethodLs with agents, MethodRegister with name, and
// MethodInbox (a --peek call, since bare intern never drains) with pending.
func rootHandler(agents []protocol.AgentView, name string, pending int) handlerFunc {
	return func(req protocol.Request) protocol.Response {
		switch req.Method {
		case protocol.MethodLs:
			return protocol.OK(req.ID, protocol.LsResult{Agents: agents})
		case protocol.MethodRegister:
			return protocol.OK(req.ID, protocol.RegisterResult{Name: name, Address: address(name, "storefront")})
		case protocol.MethodInbox:
			return protocol.OK(req.ID, protocol.InboxResult{Pending: pending})
		default:
			return protocol.Fail(req.ID, protocol.CodeBadRequest, "unexpected method "+req.Method)
		}
	}
}

func TestBareInternShowsUnreadCount(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, rootHandler(agents().Agents, "frontend", 3))

	r := mustRun(t, newRootCmd(), "")

	requireContains(t, r.stdout, "3 unread for frontend@storefront", "stdout")
	requireContains(t, r.stdout, "Next: intern inbox", "stdout")

	inboxReq := d.requests()[2]
	params := decodeParams[protocol.InboxParams](t, inboxReq)
	if !params.Peek {
		t.Fatal("bare intern drained the inbox instead of peeking -- it must never clear mail")
	}
}

func TestBareInternShowsZeroUnread(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, rootHandler(agents().Agents, "frontend", 0))

	r := mustRun(t, newRootCmd(), "")

	requireContains(t, r.stdout, "0 unread for frontend@storefront", "stdout")
	requireContains(t, r.stdout, "Next: intern wait --timeout 5m", "stdout")
}

// TestBareInternDoesNotRegisterInAnEmptyWorkspace is the deliberate decision
// behind principle 9 here: a workspace nobody has ever registered in stays
// that way after a bare `intern` glance -- it is a read, not a side effect.
func TestBareInternDoesNotRegisterInAnEmptyWorkspace(t *testing.T) {
	setIdentity(t, "", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.LsResult{}))

	r := mustRun(t, newRootCmd(), "")

	requireContains(t, r.stdout, "no agent registered in storefront yet", "stdout")
	requireContains(t, r.stdout, "intern register --as <name>", "stdout")
	d.only(t, protocol.MethodLs)
}

func TestBareInternWithoutADaemonExitsThree(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	r := run(t, newRootCmd(), "")
	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
	requireNotContains(t, r.stdout, "Usage:", "stdout")
}
