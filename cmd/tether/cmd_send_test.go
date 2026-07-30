package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/praneethravuri/tether/pkg/protocol"
)

// hostileBody is the kind of text a language model actually writes: quotes,
// backticks, dollar signs, backslashes and newlines. Passed through a shell as
// an argument it would be mangled or executed; through --body-file it must
// arrive byte for byte.
const hostileBody = "Run `make build` first.\n" +
	"Set $HOME and \"QUOTED\" and 'single' values.\n" +
	"Cost: $5.00 100%\n" +
	"A backslash \\ and a tab\there.\n" +
	"$(rm -rf /) must be literal text.\n"

func TestSendWithAPositionalBody(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K1QW8Z3M4T7V9XBCDEF2GH"}))

	r := mustRun(t, newSendCmd(), "", "backend", "the API contract changed")

	params := decodeParams[protocol.SendParams](t, d.registerThen(t, protocol.MethodSend))
	if params.FromName != "frontend" || params.FromWorkspace != "storefront" {
		t.Fatalf("from = %s@%s, want frontend@storefront", params.FromName, params.FromWorkspace)
	}
	if params.ToName != "backend" || params.ToWorkspace != "storefront" {
		t.Fatalf("to = %s@%s, want backend@storefront (a bare name uses my workspace)",
			params.ToName, params.ToWorkspace)
	}
	if params.Kind != kindNote {
		t.Fatalf("kind = %q, want %q by default", params.Kind, kindNote)
	}
	if params.Body != "the API contract changed" {
		t.Fatalf("body = %q", params.Body)
	}

	requireContains(t, r.stdout, "01K1QW8Z3M4T7V9XBCDEF2GH", "stdout")
	requireContains(t, r.stdout, "backend@storefront", "stdout")
}

func TestSendToAnotherWorkspace(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

	mustRun(t, newSendCmd(), "", "backend@warehouse", "--kind", "handoff",
		"--reply-to", "01KPARENT", "hello")

	params := decodeParams[protocol.SendParams](t, d.registerThen(t, protocol.MethodSend))
	if params.ToName != "backend" || params.ToWorkspace != "warehouse" {
		t.Fatalf("to = %s@%s, want backend@warehouse", params.ToName, params.ToWorkspace)
	}
	if params.Kind != kindHandoff {
		t.Fatalf("kind = %q, want handoff", params.Kind)
	}
	if params.ReplyTo != "01KPARENT" {
		t.Fatalf("reply_to = %q, want 01KPARENT", params.ReplyTo)
	}
}

// This is the bug this CLI exists to avoid: a body full of shell metacharacters
// must survive unchanged when read from a file.
func TestSendBodyFileSurvivesByteForByte(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

	dir := t.TempDir()
	path := filepath.Join(dir, "body.md")
	if err := os.WriteFile(path, []byte(hostileBody), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mustRun(t, newSendCmd(), "", "backend", "--body-file", path)

	params := decodeParams[protocol.SendParams](t, d.registerThen(t, protocol.MethodSend))
	if params.Body != hostileBody {
		t.Fatalf("body was altered in transit.\n got: %q\nwant: %q", params.Body, hostileBody)
	}
}

// The same, from stdin, which is how a piped heredoc arrives.
func TestSendBodyFileDashReadsStdinByteForByte(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

	mustRun(t, newSendCmd(), hostileBody, "backend", "--body-file", "-")

	params := decodeParams[protocol.SendParams](t, d.registerThen(t, protocol.MethodSend))
	if params.Body != hostileBody {
		t.Fatalf("body read from stdin was altered.\n got: %q\nwant: %q",
			params.Body, hostileBody)
	}
}

// Trailing newlines are part of the body: no trimming, ever.
func TestSendDoesNotTrimTheBody(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

	const body = "  leading and trailing whitespace  \n\n"
	mustRun(t, newSendCmd(), body, "backend", "--body-file", "-")

	params := decodeParams[protocol.SendParams](t, d.registerThen(t, protocol.MethodSend))
	if params.Body != body {
		t.Fatalf("body = %q, want %q unchanged", params.Body, body)
	}
}

func TestSendRejectsAmbiguousAndMissingBodies(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "both a positional body and a body file",
			args: []string{"backend", "--body-file", "-", "inline body"},
			want: "not both",
		},
		{
			name: "no body at all",
			args: []string{"backend"},
			want: "no message body",
		},
		{
			name: "an empty positional body",
			args: []string{"backend", ""},
			want: "empty",
		},
		{
			name: "an empty recipient",
			args: []string{"", "a body"},
			want: "empty address",
		},
		{
			name: "an unknown kind",
			args: []string{"backend", "--kind", "shout", "a body"},
			want: "unknown --kind",
		},
		{
			name: "a body file that does not exist",
			args: []string{"backend", "--body-file", "/no/such/file/anywhere"},
			want: "cannot read the message body",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setIdentity(t, "frontend", "storefront")
			d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

			r := run(t, newSendCmd(), "", tc.args...)
			if r.err == nil {
				t.Fatalf("send succeeded with args %v", tc.args)
			}
			requireContains(t, r.err.Error(), tc.want, "error")
			if got := r.exitCode(); got != exitGeneral {
				t.Fatalf("exit code = %d, want %d", got, exitGeneral)
			}
			if n := len(d.requests()); n != 0 {
				t.Fatalf("a bad request reached the daemon %d times", n)
			}
		})
	}
}

// The missing-body error has to teach, not just refuse.
func TestSendMissingBodyErrorMentionsBodyFile(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.SendResult{}))

	r := run(t, newSendCmd(), "", "backend")
	requireContains(t, r.err.Error(), "--body-file", "error")
	requireContains(t, r.err.Error(), "stdin", "error")
}

func TestSendEmptyStdinIsAnError(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.SendResult{}))

	r := run(t, newSendCmd(), "", "backend", "--body-file", "-")
	if r.err == nil {
		t.Fatal("send accepted an empty body from stdin")
	}
	requireContains(t, r.err.Error(), "stdin", "error")
}

func TestSendJSON(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K1QW8Z3M4T7V9XBCDEF2GH"}))

	r := mustRun(t, newSendCmd(), "", "backend", "--json", "hello")

	var got protocol.SendResult
	unmarshalJSON(t, r.stdout, &got)
	if got.MessageID != "01K1QW8Z3M4T7V9XBCDEF2GH" {
		t.Fatalf("message id = %q", got.MessageID)
	}
}

// TestSendImplicitlyRegistersFirst is M3: every send fires an implicit
// register call for the sender before the real "send" request, which is
// what refreshes last_seen and gives the daemon a live session to
// authenticate FromSession against.
func TestSendImplicitlyRegistersFirst(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)
	t.Setenv(envSessionOverride, "sess-1")

	d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

	mustRun(t, newSendCmd(), "", "backend", "hello")

	sendReq := d.registerThen(t, protocol.MethodSend)

	regParams := decodeParams[protocol.RegisterParams](t, d.requests()[0])
	if regParams.Name != "frontend" || regParams.Workspace != "storefront" {
		t.Fatalf("implicit register was for %s@%s, want frontend@storefront",
			regParams.Name, regParams.Workspace)
	}

	sendParams := decodeParams[protocol.SendParams](t, sendReq)
	if sendParams.FromSession == "" {
		t.Fatal("send did not carry a from_session")
	}
	if sendParams.FromSession != regParams.SessionID {
		t.Fatalf("from_session %q does not match the session just registered with %q",
			sendParams.FromSession, regParams.SessionID)
	}
}

// TestSendConflictExitsFive is the CLI-level half of the session-forgery
// fix: a 409 specifically from the "send" call itself (a session mismatch,
// see internal/daemon/server.go's authenticate) must exit 5, exactly like a
// register conflict does. The implicit register that precedes it succeeds
// normally, so this exercises send's own authentication failure rather than
// ensureRegistered's.
func TestSendConflictExitsFive(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, func(req protocol.Request) protocol.Response {
		if req.Method == protocol.MethodRegister {
			return protocol.OK(req.ID, protocol.RegisterResult{Created: true})
		}
		return protocol.Fail(req.ID, protocol.CodeConflict,
			"acting as frontend but a different session holds that name")
	})

	r := run(t, newSendCmd(), "", "backend", "hello")
	if r.err == nil {
		t.Fatal("send succeeded despite a session conflict")
	}
	if got := r.exitCode(); got != exitConflict {
		t.Fatalf("exit code = %d, want %d", got, exitConflict)
	}
}

// A send to an unheld name shares exitTimeout with `wait`'s timeout: both
// mean "nothing there for you," not "something is broken."
func TestSendToAnUnknownAgentReportsTheDaemonMessage(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, errHandler(protocol.CodeNotFound,
		"no agent named ghost in storefront — did you mean ghoul@storefront?"))

	r := run(t, newSendCmd(), "", "ghost", "hello")
	if r.err == nil {
		t.Fatal("send succeeded to an unknown agent")
	}
	requireContains(t, r.err.Error(), "no agent named ghost", "error")
	requireContains(t, r.err.Error(), "did you mean ghoul@storefront", "error")
	if got := r.exitCode(); got != exitTimeout {
		t.Fatalf("exit code = %d, want %d", got, exitTimeout)
	}
}

func TestSendWithoutADaemonExitsThree(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	r := run(t, newSendCmd(), "", "backend", "hello")
	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
}

func TestSendKindsAreAccepted(t *testing.T) {
	for _, kind := range validKinds {
		t.Run(kind, func(t *testing.T) {
			setIdentity(t, "frontend", "storefront")
			d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

			mustRun(t, newSendCmd(), "", "backend", "--kind", kind, "body")

			params := decodeParams[protocol.SendParams](t, d.registerThen(t, protocol.MethodSend))
			if params.Kind != kind {
				t.Fatalf("kind = %q, want %q", params.Kind, kind)
			}
		})
	}
}

// -- positional <to> (Phase 6) ------------------------------------------------

// TestSendPositionalTarget is the new primary form: `tether send <to> <body>`
// with no --to flag at all.
func TestSendPositionalTarget(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

	r := mustRun(t, newSendCmd(), "", "backend", "hi")

	params := decodeParams[protocol.SendParams](t, d.registerThen(t, protocol.MethodSend))
	if params.ToName != "backend" || params.ToWorkspace != "storefront" {
		t.Fatalf("to = %s@%s, want backend@storefront", params.ToName, params.ToWorkspace)
	}
	if params.Body != "hi" {
		t.Fatalf("body = %q, want %q", params.Body, "hi")
	}
	requireContains(t, r.stdout, "backend@storefront", "stdout")
}

// TestSendShowsRecipientStateWhenPresent proves the unicast output line
// grows a state suffix when the daemon reports one.
func TestSendShowsRecipientStateWhenPresent(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K", RecipientState: "blocked"}))

	r := mustRun(t, newSendCmd(), "", "backend", "hi")
	requireContains(t, r.stdout, "sent 01K to backend@storefront (blocked)", "stdout")
}

// TestSendOmitsRecipientStateWhenAbsent covers an older daemon (or a
// broadcast) that never sets RecipientState -- the line must not grow a
// trailing "()" or otherwise change shape.
func TestSendOmitsRecipientStateWhenAbsent(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

	r := mustRun(t, newSendCmd(), "", "backend", "hi")
	requireContains(t, r.stdout, "sent 01K to backend@storefront\n", "stdout")
	requireNotContains(t, r.stdout, "(", "stdout")
}

// TestSendPositionalWithBodyFile covers the positional target combined with
// --body-file, the shape a caller with a hostile body will actually use.
func TestSendPositionalWithBodyFile(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

	dir := t.TempDir()
	path := filepath.Join(dir, "body.md")
	if err := os.WriteFile(path, []byte(hostileBody), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mustRun(t, newSendCmd(), "", "backend", "--body-file", path)

	params := decodeParams[protocol.SendParams](t, d.registerThen(t, protocol.MethodSend))
	if params.ToName != "backend" {
		t.Fatalf("to = %q, want backend", params.ToName)
	}
	if params.Body != hostileBody {
		t.Fatalf("body was altered in transit.\n got: %q\nwant: %q", params.Body, hostileBody)
	}
}

// TestSendTooManyPositionalArgs confirms `send <to> <body> <extra>` is
// rejected rather than silently dropping the extra argument.
func TestSendTooManyPositionalArgs(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.SendResult{MessageID: "01K"}))

	r := run(t, newSendCmd(), "", "backend", "hi", "extra")
	if r.err == nil {
		t.Fatal("send accepted three positional arguments")
	}
}

// -- broadcast ('*' / 'all') (Phase 6) ----------------------------------------

// TestSendBroadcastStarAndAllProduceTheSameRequestShape confirms both spellings
// of the broadcast marker pass the target through to the daemon literally and
// identically -- the CLI does not normalise one to the other.
func TestSendBroadcastStarAndAllProduceTheSameRequestShape(t *testing.T) {
	for _, marker := range []string{"*", "all"} {
		t.Run(marker, func(t *testing.T) {
			setIdentity(t, "frontend", "storefront")
			d := newFakeDaemon(t, okHandler(protocol.SendResult{
				Recipients: []string{"backend@storefront", "reviewer@storefront"},
				Delivered:  2,
			}))

			r := mustRun(t, newSendCmd(), "", marker, "heads up")

			params := decodeParams[protocol.SendParams](t, d.registerThen(t, protocol.MethodSend))
			if params.ToName != marker {
				t.Fatalf("to_name = %q, want %q passed through literally", params.ToName, marker)
			}
			if params.ToWorkspace != "storefront" {
				t.Fatalf("to_workspace = %q, want storefront", params.ToWorkspace)
			}
			requireContains(t, r.stdout, "2 agents", "stdout")
		})
	}
}

// TestSendBroadcastWithNoOtherAgentsIsSuccess is the CLI-level half of "a
// lone agent broadcasting to an empty room is a valid, unremarkable case":
// Delivered: 0 must exit 0, not fail.
func TestSendBroadcastWithNoOtherAgentsIsSuccess(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.SendResult{Recipients: []string{}, Delivered: 0}))

	r := run(t, newSendCmd(), "", "*", "anyone?")
	if r.err != nil {
		t.Fatalf("broadcast to an empty room failed: %v", r.err)
	}
	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want %d", got, exitOK)
	}
	requireContains(t, r.stdout, "nobody else", "stdout")
}
