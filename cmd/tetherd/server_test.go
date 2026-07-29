package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/praneethravuri/tether/internal/store"
	"github.com/praneethravuri/tether/pkg/protocol"
)

// fakeClock is a mutex-guarded fake clock so tests can fast-forward past
// staleness thresholds without sleeping. It mirrors internal/store's own
// test clock (store_test.go's clock), which this package cannot import
// (it is unexported and lives in a _test.go file).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Now()} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// -- harness ----------------------------------------------------------------

// testServer is a real Server on a real unix socket, driven over the wire.
// Nothing here imports cmd/tether: the daemon is exercised exactly as an
// arbitrary client would exercise it.
type testServer struct {
	t      *testing.T
	srv    *Server
	store  *store.Store
	sock   string
	cancel context.CancelFunc
	done   chan error
}

// shortTempDir returns a short-lived directory with a short path. t.TempDir()
// embeds the test name, and a unix socket path is capped at ~104 bytes on
// darwin, so long test names would break the bind.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "td")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newTestServer(t *testing.T, tweak func(*Config)) *testServer {
	t.Helper()

	dir := shortTempDir(t)
	st, err := store.Open(context.Background(), filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Logger = log.New(io.Discard, "", 0)
	cfg.SweepInterval = 50 * time.Millisecond // exercise the sweeper in every test
	if tweak != nil {
		tweak(&cfg)
	}

	sock := filepath.Join(dir, "s")
	ln, err := protocol.Listen(sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ts := &testServer{
		t:      t,
		srv:    NewServer(st, cfg),
		store:  st,
		sock:   sock,
		cancel: cancel,
		done:   make(chan error, 1),
	}
	go func() { ts.done <- ts.srv.Serve(ctx, ln) }()

	t.Cleanup(func() { ts.stop() })
	return ts
}

// stop shuts the server down and fails the test if it does not come back.
func (ts *testServer) stop() {
	ts.t.Helper()
	ts.cancel()
	select {
	case err := <-ts.done:
		if err != nil {
			ts.t.Errorf("Serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		ts.t.Error("Serve did not return after cancellation")
	}
	if err := ts.store.Close(); err != nil && !strings.Contains(err.Error(), "closed") {
		ts.t.Errorf("close store: %v", err)
	}
}

// client is a raw newline-delimited JSON client.
type client struct {
	t    *testing.T
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
	seq  int
}

func (ts *testServer) dial() *client {
	ts.t.Helper()
	conn, err := net.Dial("unix", ts.sock)
	if err != nil {
		ts.t.Fatalf("dial: %v", err)
	}
	c := &client{t: ts.t, conn: conn, enc: json.NewEncoder(conn), dec: json.NewDecoder(conn)}
	ts.t.Cleanup(c.close)
	return c
}

func (c *client) close() { _ = c.conn.Close() }

// call sends one request and returns the response.
func (c *client) call(method string, params any) protocol.Response {
	c.t.Helper()
	c.seq++
	id := fmt.Sprintf("r%d", c.seq)

	req := protocol.Request{ID: id, Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			c.t.Fatalf("marshal params: %v", err)
		}
		req.Params = raw
	}
	if err := c.enc.Encode(req); err != nil {
		c.t.Fatalf("write %s: %v", method, err)
	}

	var resp protocol.Response
	if err := c.dec.Decode(&resp); err != nil {
		c.t.Fatalf("read %s: %v", method, err)
	}
	if resp.ID != id {
		c.t.Fatalf("response id = %q, want %q", resp.ID, id)
	}
	return resp
}

// mustCall fails unless the call succeeded, and decodes the result.
func (c *client) mustCall(method string, params, out any) {
	c.t.Helper()
	resp := c.call(method, params)
	if resp.Error != nil {
		c.t.Fatalf("%s failed: %v", method, resp.Error)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			c.t.Fatalf("decode %s result: %v", method, err)
		}
	}
}

// mustFail fails unless the call returned the given code.
func (c *client) mustFail(method string, params any, code int) *protocol.Error {
	c.t.Helper()
	resp := c.call(method, params)
	if resp.Error == nil {
		c.t.Fatalf("%s: expected error %d, got result %s", method, code, resp.Result)
	}
	if resp.Error.Code != code {
		c.t.Fatalf("%s: error code = %d (%s), want %d", method, resp.Error.Code, resp.Error.Message, code)
	}
	return resp.Error
}

func (c *client) register(name, ws, session string) protocol.RegisterResult {
	c.t.Helper()
	var out protocol.RegisterResult
	c.mustCall(protocol.MethodRegister, protocol.RegisterParams{
		Name: name, Workspace: ws, Harness: "test", SessionID: session, Cwd: "/tmp", PID: os.Getpid(),
	}, &out)
	return out
}

func (c *client) send(body string) string {
	c.t.Helper()
	var out protocol.SendResult
	c.mustCall(protocol.MethodSend, protocol.SendParams{
		FromName: "alice", FromWorkspace: "proj", ToName: "bob", ToWorkspace: "proj", Body: body,
	}, &out)
	return out.MessageID
}

// inbox drains by default now (replay=false means "drain", not "peek"). See
// inboxPeek for a non-destructive read.
func (c *client) inbox(name string, replay bool) []protocol.MessageView {
	c.t.Helper()
	var out protocol.InboxResult
	c.mustCall(protocol.MethodInbox, protocol.InboxParams{
		Name: name, Workspace: "proj", Limit: 500, Replay: replay,
	}, &out)
	return out.Messages
}

func (c *client) inboxPeek(name string) []protocol.MessageView {
	c.t.Helper()
	var out protocol.InboxResult
	c.mustCall(protocol.MethodInbox, protocol.InboxParams{
		Name: name, Workspace: "proj", Limit: 500, Peek: true,
	}, &out)
	return out.Messages
}

// -- registration -----------------------------------------------------------

func TestRegister(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()

	got := c.register("alice", "proj", "sess-1")
	want := protocol.RegisterResult{Address: "alice@proj", Harness: "test", Created: true}
	if got != want {
		t.Fatalf("register result = %+v, want %+v", got, want)
	}

	// The same session re-registering is a supported no-op, not a conflict,
	// and is a refresh of the existing row rather than a fresh creation.
	got = c.register("alice", "proj", "sess-1")
	if got.Created {
		t.Fatalf("re-register reported Created=true, want false (the row already existed)")
	}
}

func TestRegister_DuplicateNameFromAnotherSessionConflicts(t *testing.T) {
	ts := newTestServer(t, nil)

	a := ts.dial()
	a.register("alice", "proj", "sess-1")

	b := ts.dial()
	_ = b.mustFail(protocol.MethodRegister, protocol.RegisterParams{
		Name: "alice", Workspace: "proj", Harness: "test", SessionID: "sess-2",
	}, protocol.CodeConflict)
}

func TestRegister_MissingFields(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()

	_ = c.mustFail(protocol.MethodRegister, protocol.RegisterParams{Workspace: "proj"}, protocol.CodeBadRequest)
	_ = c.mustFail(protocol.MethodRegister, protocol.RegisterParams{Name: "alice"}, protocol.CodeBadRequest)
	_ = c.mustFail(protocol.MethodRegister, protocol.RegisterParams{Name: "a@b", Workspace: "proj"}, protocol.CodeBadRequest)
	// Absent params block at all.
	_ = c.mustFail(protocol.MethodRegister, nil, protocol.CodeBadRequest)
}

// implausiblePID is large enough that no supported platform will ever hand
// it to a real process (see internal/proc/proc_test.go's identical
// reasoning), so registering with it is a safe, non-flaky way to exercise
// the "session pid is not alive" rejection.
const implausiblePID = 1 << 30

// TestRegister_DeadPidRejected is P1: handleRegister must reject a session
// pid that is not alive, rather than accepting whatever a client claims.
func TestRegister_DeadPidRejected(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()

	e := c.mustFail(protocol.MethodRegister, protocol.RegisterParams{
		Name: "alice", Workspace: "proj", Harness: "test", SessionID: "s1", PID: implausiblePID,
	}, protocol.CodeBadRequest)
	if !strings.Contains(e.Message, "1073741824") {
		t.Fatalf("error message %q does not name the dead pid", e.Message)
	}
}

// TestRegister_LivePidRecordsPidStart proves the daemon computes pid_start
// itself (never trusting a client-supplied value) for a genuinely live pid,
// by round-tripping through status and confirming the agent is reachable --
// the store-level round trip of PIDStart itself is covered directly in
// internal/store/store_test.go.
func TestRegister_LivePidRecordsPidStart(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()

	c.register("alice", "proj", "s1") // client.register uses os.Getpid(), which is alive

	var st protocol.StatusResult
	c.mustCall(protocol.MethodStatus, protocol.StatusParams{Name: "alice", Workspace: "proj"}, &st)
	if st.Agent.PID != os.Getpid() {
		t.Fatalf("stored pid = %d, want %d", st.Agent.PID, os.Getpid())
	}
}

// TestRegister_DeadIncumbentReclaimedImmediately is the headline fix: a name
// held by a session whose pid is now provably dead must be reclaimable right
// away, not after StaleAfter elapses.
//
// The incumbent cannot be seeded through the register RPC itself: handleRegister
// rejects a dead pid on every registration, first-time or not (see
// TestRegister_DeadPidRejected), so a session's pid can only go from alive to
// dead the way it would in production -- the process that registered it
// exits sometime later while the row stays in the store. This test fast-forwards
// past that by writing the incumbent row directly through the store, exactly
// as if it had registered validly with a pid that has since exited.
func TestRegister_DeadIncumbentReclaimedImmediately(t *testing.T) {
	// A long StaleAfter proves the reclaim is NOT happening via the
	// time-based path.
	ts := newTestServer(t, func(c *Config) { c.StaleAfter = time.Hour })
	c := ts.dial()

	if err := ts.store.Register(context.Background(), store.Agent{
		Workspace: "proj", Name: "alice", Harness: "test", SessionID: "sess-dead",
		PID: implausiblePID, Cwd: "/tmp",
	}, time.Now()); err != nil {
		t.Fatalf("seed incumbent: %v", err)
	}

	got := c.register("alice", "proj", "sess-new")
	if got.Created {
		t.Fatalf("reclaim reported Created=true, want false (the row already existed)")
	}

	var st protocol.StatusResult
	c.mustCall(protocol.MethodStatus, protocol.StatusParams{Name: "alice", Workspace: "proj"}, &st)
	if st.Agent.PID != os.Getpid() {
		t.Fatalf("reclaim did not update the stored pid: %+v", st.Agent)
	}
}

// TestRegister_LiveIncumbentStillConflicts is the mirror of the reclaim
// test: a name held by a session whose pid IS alive must still 409 for a
// different session, exactly like today.
func TestRegister_LiveIncumbentStillConflicts(t *testing.T) {
	ts := newTestServer(t, func(c *Config) { c.StaleAfter = time.Hour })

	a := ts.dial()
	a.register("alice", "proj", "sess-1") // peer pid is this live test process

	b := ts.dial()
	_ = b.mustFail(protocol.MethodRegister, protocol.RegisterParams{
		Name: "alice", Workspace: "proj", Harness: "test", SessionID: "sess-2", PID: os.Getpid(),
	}, protocol.CodeConflict)
}

// -- authentication -----------------------------------------------------------

// TestSend_SessionMismatchIsConflict is the other P1 fix: a sender claiming
// a session that does not match the live session actually holding
// from_name must be rejected, closing the forgery this phase exists to fix.
func TestSend_SessionMismatchIsConflict(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "sess-1")
	c.register("bob", "proj", "sess-2")

	_ = c.mustFail(protocol.MethodSend, protocol.SendParams{
		FromName: "alice", FromWorkspace: "proj", FromSession: "sess-intruder",
		ToName: "bob", ToWorkspace: "proj", Body: "forged",
	}, protocol.CodeConflict)

	// The genuine session still works.
	c.mustCall(protocol.MethodSend, protocol.SendParams{
		FromName: "alice", FromWorkspace: "proj", FromSession: "sess-1",
		ToName: "bob", ToWorkspace: "proj", Body: "genuine",
	}, &protocol.SendResult{})
}

// TestSend_EmptyStoredSessionStillAllowed covers the legacy/no-session row:
// a sender whose stored session_id is empty (registered before session
// authentication existed, or by a caller that never supplied one) must not
// be locked out by this phase's change.
func TestSend_EmptyStoredSessionStillAllowed(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.mustCall(protocol.MethodRegister, protocol.RegisterParams{
		Name: "alice", Workspace: "proj", Harness: "test", // no SessionID
	}, &protocol.RegisterResult{})
	c.register("bob", "proj", "sess-2")

	c.mustCall(protocol.MethodSend, protocol.SendParams{
		FromName: "alice", FromWorkspace: "proj", FromSession: "whatever-i-like",
		ToName: "bob", ToWorkspace: "proj", Body: "hi",
	}, &protocol.SendResult{})
}

// -- activity keeps an agent alive --------------------------------------------

// TestActivityKeepsAgentAliveAndNameUnstealable is the core regression test
// for this phase's P1 bug: before implicit registration, nothing ever
// refreshed last_seen after the initial register, so every agent silently
// vanished from `who` after StaleAfter and its name became stealable even
// though the agent was still alive and working. With a very short
// StaleAfter and a fake clock advanced well past it, an agent that has made
// ANY authenticated call since registering (here, a second inbox call) must
// still appear in `who` AND must not be stealable by a different session.
func TestActivityKeepsAgentAliveAndNameUnstealable(t *testing.T) {
	const staleAfter = 50 * time.Millisecond
	clk := newFakeClock()

	ts := newTestServer(t, func(c *Config) { c.StaleAfter = staleAfter })
	ts.store.Now = clk.Now

	c := ts.dial()
	c.register("alice", "proj", "sess-1")

	clk.advance(staleAfter * 10)

	// Any activity since register -- an authenticated inbox call -- must
	// refresh last_seen.
	c.mustCall(protocol.MethodInbox, protocol.InboxParams{
		Name: "alice", Workspace: "proj", Session: "sess-1",
	}, &protocol.InboxResult{})

	var who protocol.WhoResult
	c.mustCall(protocol.MethodWho, protocol.WhoParams{Workspace: "proj"}, &who)
	found := false
	for _, a := range who.Agents {
		if a.Name == "alice" {
			found = true
		}
	}
	if !found {
		t.Fatalf("alice missing from who after activity within StaleAfter of the read: %+v", who.Agents)
	}

	// And the name must not be stealable by a different session: alice's
	// pid (this live test process) is also still alive, so this is doubly
	// protected, but the point under test is specifically that activity
	// alone -- not just a live pid -- keeps last_seen fresh enough that the
	// time-based staleness guard in store.Register would refuse a steal too.
	b := ts.dial()
	_ = b.mustFail(protocol.MethodRegister, protocol.RegisterParams{
		Name: "alice", Workspace: "proj", Harness: "test", SessionID: "sess-intruder",
	}, protocol.CodeConflict)
}

// TestUnregisterAndHeartbeatAreGone confirms the retired RPCs are not merely
// absent from the CLI surface but actively rejected by the wire: a raw
// "unregister" or "heartbeat" request now falls through dispatch's default
// case like any other unknown method.
func TestUnregisterAndHeartbeatAreGone(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")

	for _, method := range []string{"unregister", "heartbeat"} {
		e := c.mustFail(method, nil, protocol.CodeBadRequest)
		if !strings.Contains(e.Message, method) {
			t.Fatalf("%s error message %q does not name the method", method, e.Message)
		}
	}

	// The connection survives both.
	c.register("bob", "proj", "s2")
}

// -- mail -------------------------------------------------------------------

// TestInboxDrainsPeekReplay is Phase 5's headline server-side contract:
// inbox drains by default (read + ack in one call), --peek leaves mail in
// place, and a message only shows up in --replay's history once a real
// drain has acked it.
func TestInboxDrainsPeekReplay(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")
	c.register("bob", "proj", "s2")

	id := c.send("hello bob")

	// A peek does not clear: two peeks return the same message, unacked.
	first := c.inboxPeek("bob")
	if len(first) != 1 {
		t.Fatalf("peek #1 = %d messages, want 1", len(first))
	}
	m := first[0]
	if m.ID != id || m.From != "alice@proj" || m.To != "bob@proj" || m.Body != "hello bob" {
		t.Fatalf("unexpected message %+v", m)
	}
	if m.Kind != store.KindNote {
		t.Fatalf("kind = %q, want %q", m.Kind, store.KindNote)
	}
	if m.ThreadID == "" {
		t.Fatal("thread_id is empty")
	}
	if _, err := time.Parse(time.RFC3339, m.CreatedAt); err != nil {
		t.Fatalf("created_at %q is not RFC3339: %v", m.CreatedAt, err)
	}
	if m.DeliveredAt == nil {
		t.Fatal("delivered_at not stamped on first read")
	}
	if m.AckedAt != nil {
		t.Fatalf("acked_at set after a peek: %v", *m.AckedAt)
	}

	second := c.inboxPeek("bob")
	if len(second) != 1 || second[0].ID != id {
		t.Fatalf("peek #2 = %+v, want the same one message again", second)
	}

	// The default drains: the message comes back once, marked acked, and is
	// gone on every subsequent read (peek or drain).
	drained := c.inbox("bob", false)
	if len(drained) != 1 || drained[0].ID != id {
		t.Fatalf("drain = %+v, want the one message", drained)
	}
	if drained[0].AckedAt == nil {
		t.Fatal("drained message has no acked_at")
	}

	if again := c.inbox("bob", false); len(again) != 0 {
		t.Fatalf("second drain = %+v, want empty", again)
	}
	if again := c.inboxPeek("bob"); len(again) != 0 {
		t.Fatalf("peek after drain = %+v, want empty", again)
	}

	replay := c.inbox("bob", true)
	if len(replay) != 1 || replay[0].ID != id {
		t.Fatalf("replay = %+v, want the drained message", replay)
	}
	if replay[0].AckedAt == nil {
		t.Fatal("replayed message has no acked_at")
	}
}

func TestSend_UnknownRecipient(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")

	_ = c.mustFail(protocol.MethodSend, protocol.SendParams{
		FromName: "alice", FromWorkspace: "proj", ToName: "ghost", ToWorkspace: "proj", Body: "anyone there",
	}, protocol.CodeNotFound)
}

// TestSend_UnknownRecipientTypoSuggestsTheCloseMatch is the did-you-mean
// half of the 404 path: a recipient name close to a real, registered one
// gets a suggestion appended to the error message. The wire code stays 404
// -- this only enriches the message, it does not invent a new error kind.
func TestSend_UnknownRecipientTypoSuggestsTheCloseMatch(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")
	c.register("backend", "proj", "s2")

	e := c.mustFail(protocol.MethodSend, protocol.SendParams{
		FromName: "alice", FromWorkspace: "proj", ToName: "backand", ToWorkspace: "proj", Body: "hi",
	}, protocol.CodeNotFound)
	if !strings.Contains(e.Message, "did you mean") || !strings.Contains(e.Message, "backend@proj") {
		t.Fatalf("error message = %q, want a did-you-mean pointing at backend@proj", e.Message)
	}
}

// TestSend_UnknownRecipientWithNoCloseMatchSuggestsNothing is the other
// half: when nothing registered is close enough, the message stays plain.
func TestSend_UnknownRecipientWithNoCloseMatchSuggestsNothing(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")
	c.register("backend", "proj", "s2")

	e := c.mustFail(protocol.MethodSend, protocol.SendParams{
		FromName: "alice", FromWorkspace: "proj", ToName: "zzzzzzzzzz", ToWorkspace: "proj", Body: "hi",
	}, protocol.CodeNotFound)
	if strings.Contains(e.Message, "did you mean") {
		t.Fatalf("error message = %q, want no did-you-mean suggestion", e.Message)
	}
}

// -- broadcast ('*' / 'all') --------------------------------------------------

// TestSend_BroadcastReachesEveryoneButTheSender is the headline broadcast
// contract: with a sender plus two other agents registered, a send to '*'
// lands in exactly the two others' inboxes and never the sender's own --
// the entire loop-prevention mechanism for this phase is that exclusion.
func TestSend_BroadcastReachesEveryoneButTheSender(t *testing.T) {
	for _, marker := range []string{"*", "all"} {
		t.Run(marker, func(t *testing.T) {
			ts := newTestServer(t, nil)
			c := ts.dial()
			c.register("alice", "proj", "s1")
			c.register("bob", "proj", "s2")
			c.register("carol", "proj", "s3")

			var out protocol.SendResult
			c.mustCall(protocol.MethodSend, protocol.SendParams{
				FromName: "alice", FromWorkspace: "proj",
				ToName: marker, ToWorkspace: "proj", Body: "heads up",
			}, &out)

			if out.Delivered != 2 {
				t.Fatalf("delivered = %d, want 2", out.Delivered)
			}
			got := map[string]bool{}
			for _, a := range out.Recipients {
				got[a] = true
			}
			if len(got) != 2 || !got["bob@proj"] || !got["carol@proj"] {
				t.Fatalf("recipients = %v, want exactly bob@proj and carol@proj", out.Recipients)
			}

			if msgs := c.inboxPeek("bob"); len(msgs) != 1 || msgs[0].Body != "heads up" {
				t.Fatalf("bob's inbox = %+v, want the broadcast message", msgs)
			}
			if msgs := c.inboxPeek("carol"); len(msgs) != 1 || msgs[0].Body != "heads up" {
				t.Fatalf("carol's inbox = %+v, want the broadcast message", msgs)
			}
			if msgs := c.inboxPeek("alice"); len(msgs) != 0 {
				t.Fatalf("alice's (the sender's) inbox = %+v, want empty", msgs)
			}
		})
	}
}

// TestSend_BroadcastWithOnlyTheSenderIsSuccessWithZeroDelivered covers "a
// lone agent broadcasting to an empty room": that must succeed, not fail,
// and report Delivered: 0.
func TestSend_BroadcastWithOnlyTheSenderIsSuccessWithZeroDelivered(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")

	var out protocol.SendResult
	c.mustCall(protocol.MethodSend, protocol.SendParams{
		FromName: "alice", FromWorkspace: "proj",
		ToName: "*", ToWorkspace: "proj", Body: "anyone?",
	}, &out)

	if out.Delivered != 0 {
		t.Fatalf("delivered = %d, want 0", out.Delivered)
	}
	if len(out.Recipients) != 0 {
		t.Fatalf("recipients = %v, want none", out.Recipients)
	}
}

// TestRegister_ReservedBroadcastNamesAreRejected is the trust-boundary half
// of broadcast addressing: no agent may ever register as "*" or "all",
// since those are reserved recipient markers, not real names.
func TestRegister_ReservedBroadcastNamesAreRejected(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()

	for _, name := range []string{"*", "all"} {
		_ = c.mustFail(protocol.MethodRegister, protocol.RegisterParams{
			Name: name, Workspace: "proj", Harness: "test", PID: os.Getpid(),
		}, protocol.CodeBadRequest)
	}
}

func TestSend_Validation(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")
	c.register("bob", "proj", "s2")

	base := func() protocol.SendParams {
		return protocol.SendParams{FromName: "alice", FromWorkspace: "proj", ToName: "bob", ToWorkspace: "proj", Body: "hi"}
	}

	empty := base()
	empty.Body = "   "
	_ = c.mustFail(protocol.MethodSend, empty, protocol.CodeBadRequest)

	badKind := base()
	badKind.Kind = "shout"
	_ = c.mustFail(protocol.MethodSend, badKind, protocol.CodeBadRequest)

	noFrom := base()
	noFrom.FromName = ""
	_ = c.mustFail(protocol.MethodSend, noFrom, protocol.CodeBadRequest)

	huge := base()
	huge.Body = strings.Repeat("x", (64<<10)+1)
	_ = c.mustFail(protocol.MethodSend, huge, protocol.CodeTooLarge)

	badReply := base()
	badReply.ReplyTo = "nosuchmessage"
	_ = c.mustFail(protocol.MethodSend, badReply, protocol.CodeNotFound)

	// The daemon is still healthy after all of that.
	c.send("still working")
}

// TestInbox_LimitIsClampedOrRejected covers both halves of the P1 fix:
// a non-positive limit quietly becomes the default of 50 (never "no
// limit"), and anything past maxInboxLimit is now a hard 400 naming both
// numbers instead of a silent clamp down to the max.
func TestInbox_LimitIsClampedOrRejected(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")
	c.register("bob", "proj", "s2")

	// More than defaultInboxLimit so the cap is actually provable, not just
	// "returned everything there was".
	const sent = defaultInboxLimit + 10
	for i := 0; i < sent; i++ {
		c.send(fmt.Sprintf("msg %d", i))
	}

	var out protocol.InboxResult
	peek := func(limit int) int {
		t.Helper()
		c.mustCall(protocol.MethodInbox,
			protocol.InboxParams{Name: "bob", Workspace: "proj", Limit: limit, Peek: true}, &out)
		return len(out.Messages)
	}

	// A negative limit falls back to the default rather than reaching SQL.
	if got := peek(-5); got != defaultInboxLimit {
		t.Fatalf("inbox with negative limit = %d messages, want %d", got, defaultInboxLimit)
	}
	// limit 0 means the same thing: the honest default, not "no limit".
	if got := peek(0); got != defaultInboxLimit {
		t.Fatalf("inbox with limit 0 = %d messages, want %d", got, defaultInboxLimit)
	}

	// Anything past maxInboxLimit is rejected outright, not silently
	// truncated down to it.
	e := c.mustFail(protocol.MethodInbox,
		protocol.InboxParams{Name: "bob", Workspace: "proj", Limit: maxInboxLimit + 1, Peek: true},
		protocol.CodeBadRequest)
	if !strings.Contains(e.Message, fmt.Sprint(maxInboxLimit+1)) || !strings.Contains(e.Message, fmt.Sprint(maxInboxLimit)) {
		t.Fatalf("error message %q does not name both the limit and the max", e.Message)
	}

	if got, err := clampLimit(0); err != nil || got != defaultInboxLimit {
		t.Fatalf("clampLimit(0) = (%d, %v), want (%d, nil)", got, err, defaultInboxLimit)
	}
	if _, err := clampLimit(maxInboxLimit + 1); err == nil {
		t.Fatal("clampLimit(maxInboxLimit+1) succeeded, want an error")
	}
}

// TestAckMethodIsGone confirms the retired "ack" RPC is rejected exactly
// like any other unknown method now that draining is the only way to
// acknowledge mail (see TestUnregisterAndHeartbeatAreGone for the same
// pattern applied to the other retired RPCs).
func TestAckMethodIsGone(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("bob", "proj", "s1")

	e := c.mustFail("ack", nil, protocol.CodeBadRequest)
	if !strings.Contains(e.Message, "ack") {
		t.Fatalf("ack error message %q does not name the method", e.Message)
	}
}

// -- presence ---------------------------------------------------------------

func TestWhoStatus(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")
	c.register("bob", "other", "s2")

	var who protocol.WhoResult
	c.mustCall(protocol.MethodWho, protocol.WhoParams{Workspace: "proj"}, &who)
	if len(who.Agents) != 1 || who.Agents[0].Address != "alice@proj" {
		t.Fatalf("who(proj) = %+v, want just alice@proj", who.Agents)
	}

	c.mustCall(protocol.MethodWho, protocol.WhoParams{All: true}, &who)
	if len(who.Agents) != 2 {
		t.Fatalf("who(all) = %d agents, want 2", len(who.Agents))
	}

	var st protocol.StatusResult
	c.mustCall(protocol.MethodStatus, protocol.StatusParams{Name: "alice", Workspace: "proj"}, &st)
	if st.Agent.Address != "alice@proj" {
		t.Fatalf("status agent = %+v", st.Agent)
	}
	if _, err := time.Parse(time.RFC3339, st.Agent.LastSeen); err != nil {
		t.Fatalf("last_seen %q is not RFC3339: %v", st.Agent.LastSeen, err)
	}
	_ = c.mustFail(protocol.MethodStatus, protocol.StatusParams{Name: "ghost", Workspace: "proj"}, protocol.CodeNotFound)
}

// TestWho_AllWithExplicitWorkspaceIsNotDiscarded is the P1 fix: handleWho
// used to blank the workspace unconditionally whenever --all was set, even
// when the caller ALSO named an explicit --workspace, silently widening the
// query to every workspace. --all and --workspace must compose: an explicit
// workspace always wins.
func TestWho_AllWithExplicitWorkspaceIsNotDiscarded(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")
	c.register("carol", "other", "s2")

	var who protocol.WhoResult
	c.mustCall(protocol.MethodWho, protocol.WhoParams{All: true, Workspace: "proj"}, &who)
	if len(who.Agents) != 1 || who.Agents[0].Address != "alice@proj" {
		t.Fatalf("who(all=true, workspace=proj) = %+v, want just alice@proj", who.Agents)
	}

	// Sanity: --all with no explicit workspace still means every workspace.
	c.mustCall(protocol.MethodWho, protocol.WhoParams{All: true}, &who)
	if len(who.Agents) != 2 {
		t.Fatalf("who(all=true) = %d agents, want 2", len(who.Agents))
	}
}

// TestWho_BlockedStateClearsOnRelease proves blocked is read from the live
// wait registry, not a durable log: an agent shows blocked only while it is
// actually parked in `wait`, and stops the instant that call returns
// (Waiters.Release runs).
func TestWho_BlockedStateClearsOnRelease(t *testing.T) {
	ts := newTestServer(t, nil)
	setup := ts.dial()
	setup.register("alice", "proj", "s1")
	setup.register("bob", "proj", "s2")

	waiter := ts.dial()
	done := make(chan protocol.WaitResult, 1)
	go func() {
		var out protocol.WaitResult
		waiter.mustCall(protocol.MethodWait, protocol.WaitParams{
			Name: "bob", Workspace: "proj", TimeoutMS: 30000,
		}, &out)
		done <- out
	}()

	// Give the waiter time to actually park before checking `who`.
	deadline := time.Now().Add(5 * time.Second)
	for ts.srv.waiters.Count("bob@proj") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("waiter never parked")
		}
		time.Sleep(10 * time.Millisecond)
	}

	var who protocol.WhoResult
	setup.mustCall(protocol.MethodWho, protocol.WhoParams{Workspace: "proj"}, &who)
	bobState := ""
	for _, a := range who.Agents {
		if a.Name == "bob" {
			bobState = a.State
		}
	}
	if bobState != "blocked" {
		t.Fatalf("bob's state while parked = %q, want blocked (%+v)", bobState, who.Agents)
	}

	setup.send("wake up") // alice -> bob, releases the parked wait

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not wake after send")
	}

	setup.mustCall(protocol.MethodWho, protocol.WhoParams{Workspace: "proj"}, &who)
	for _, a := range who.Agents {
		if a.Name == "bob" && a.State == "blocked" {
			t.Fatalf("bob still reads as blocked after the wait returned: %+v", who.Agents)
		}
	}
}

// -- protocol robustness ----------------------------------------------------

func TestUnknownMethod(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()

	e := c.mustFail("teleport", nil, protocol.CodeBadRequest)
	if !strings.Contains(e.Message, "teleport") {
		t.Fatalf("error message %q does not name the method", e.Message)
	}
	// The connection survives an unknown method.
	c.register("alice", "proj", "s1")
}

func TestMalformedJSONClosesConnectionButNotDaemon(t *testing.T) {
	ts := newTestServer(t, nil)

	bad := ts.dial()
	if _, err := bad.conn.Write([]byte("{\"id\": \"1\", this is not json}\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var resp protocol.Response
	if err := bad.dec.Decode(&resp); err != nil {
		t.Fatalf("expected an error response before close, got %v", err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.CodeBadRequest {
		t.Fatalf("response = %+v, want a 400", resp)
	}

	// The connection is then closed cleanly: EOF, not a reset mid-stream.
	if err := bad.dec.Decode(&resp); err != io.EOF && !strings.Contains(fmt.Sprint(err), "closed") {
		t.Fatalf("second decode = %v, want EOF", err)
	}

	// A brand new connection still works.
	good := ts.dial()
	good.register("alice", "proj", "s1")
}

func TestOversizedRequestIsRejected(t *testing.T) {
	ts := newTestServer(t, func(c *Config) { c.MaxRequestBytes = 4 << 10 })

	c := ts.dial()
	// Comfortably over the cap but well under the socket buffer, so the write
	// completes even though the daemon stops reading.
	body := strings.Repeat("x", 16<<10)
	req := protocol.Request{ID: "big", Method: protocol.MethodSend}
	req.Params, _ = json.Marshal(protocol.SendParams{
		FromName: "a", FromWorkspace: "proj", ToName: "b", ToWorkspace: "proj", Body: body,
	})
	if err := c.enc.Encode(req); err != nil {
		t.Fatalf("write: %v", err)
	}

	var resp protocol.Response
	if err := c.dec.Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.CodeTooLarge {
		t.Fatalf("response = %+v, want a 413", resp)
	}

	// The daemon survived and serves new clients.
	good := ts.dial()
	good.register("alice", "proj", "s1")
}

func TestConnectionIsReusable(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()

	c.register("alice", "proj", "s1")
	c.register("bob", "proj", "s2")
	id := c.send("three requests, one dial")

	msgs := c.inbox("bob", false)
	if len(msgs) != 1 || msgs[0].ID != id {
		t.Fatalf("inbox = %+v, want the one message", msgs)
	}
	if c.seq < 3 {
		t.Fatalf("only %d requests were made on the connection", c.seq)
	}
}

func TestConcurrentClients(t *testing.T) {
	ts := newTestServer(t, nil)

	const (
		clients      = 20
		perClient    = 5
		totalWanted  = clients * perClient
		workspace    = "proj"
		sinkName     = "sink"
		sinkSessions = "sink-session"
	)

	setup := ts.dial()
	setup.register(sinkName, workspace, sinkSessions)

	var wg sync.WaitGroup
	errs := make(chan error, clients)

	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			conn, err := net.Dial("unix", ts.sock)
			if err != nil {
				errs <- fmt.Errorf("client %d dial: %w", i, err)
				return
			}
			defer func() { _ = conn.Close() }()

			enc := json.NewEncoder(conn)
			dec := json.NewDecoder(conn)
			call := func(method string, params any) error {
				raw, err := json.Marshal(params)
				if err != nil {
					return err
				}
				if err := enc.Encode(protocol.Request{ID: "x", Method: method, Params: raw}); err != nil {
					return err
				}
				var resp protocol.Response
				if err := dec.Decode(&resp); err != nil {
					return err
				}
				if resp.Error != nil {
					return fmt.Errorf("%s: %w", method, resp.Error)
				}
				return nil
			}

			name := fmt.Sprintf("agent-%02d", i)
			if err := call(protocol.MethodRegister, protocol.RegisterParams{
				Name: name, Workspace: workspace, Harness: "test", SessionID: name,
			}); err != nil {
				errs <- fmt.Errorf("client %d: %w", i, err)
				return
			}

			for j := 0; j < perClient; j++ {
				if err := call(protocol.MethodSend, protocol.SendParams{
					FromName: name, FromWorkspace: workspace,
					ToName: sinkName, ToWorkspace: workspace,
					Body: fmt.Sprintf("%s/%d", name, j),
				}); err != nil {
					errs <- fmt.Errorf("client %d msg %d: %w", i, j, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	msgs := setup.inbox(sinkName, false)
	if len(msgs) != totalWanted {
		t.Fatalf("sink received %d messages, want %d", len(msgs), totalWanted)
	}
	seen := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		if seen[m.Body] {
			t.Errorf("duplicate message body %q", m.Body)
		}
		seen[m.Body] = true
	}
	for i := 0; i < clients; i++ {
		for j := 0; j < perClient; j++ {
			want := fmt.Sprintf("agent-%02d/%d", i, j)
			if !seen[want] {
				t.Errorf("lost message %q", want)
			}
		}
	}

	var who protocol.WhoResult
	setup.mustCall(protocol.MethodWho, protocol.WhoParams{Workspace: workspace}, &who)
	if len(who.Agents) != clients+1 {
		t.Fatalf("who = %d agents, want %d", len(who.Agents), clients+1)
	}
}

// -- wait -------------------------------------------------------------------

func TestWait_ReturnsImmediatelyWhenMailExists(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("alice", "proj", "s1")
	c.register("bob", "proj", "s2")
	c.send("already here")

	start := time.Now()
	var out protocol.WaitResult
	c.mustCall(protocol.MethodWait, protocol.WaitParams{Name: "bob", Workspace: "proj", TimeoutMS: 30000}, &out)
	elapsed := time.Since(start)

	if out.TimedOut {
		t.Fatal("wait timed out even though mail was already pending")
	}
	if out.Pending != 1 {
		t.Fatalf("pending = %d, want 1", out.Pending)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("wait took %v with mail already pending", elapsed)
	}
}

func TestWait_WakesOnSendFromAnotherConnection(t *testing.T) {
	ts := newTestServer(t, nil)

	setup := ts.dial()
	setup.register("alice", "proj", "s1")
	setup.register("bob", "proj", "s2")

	waiter := ts.dial()
	type result struct {
		res     protocol.WaitResult
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		var out protocol.WaitResult
		waiter.mustCall(protocol.MethodWait, protocol.WaitParams{
			Name: "bob", Workspace: "proj", TimeoutMS: 30000,
		}, &out)
		done <- result{out, time.Since(start)}
	}()

	// Give the waiter time to actually park in the select rather than take the
	// pending-count shortcut, so the wake path is what is under test.
	time.Sleep(150 * time.Millisecond)
	setup.send("wake up")

	select {
	case got := <-done:
		if got.res.TimedOut {
			t.Fatal("wait reported a timeout after a send")
		}
		if got.res.Pending != 1 {
			t.Fatalf("pending = %d, want 1", got.res.Pending)
		}
		if got.elapsed > 5*time.Second {
			t.Fatalf("wait took %v, expected to wake promptly", got.elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("wait did not wake within 10s of the send")
	}
}

func TestWait_TimesOutCleanly(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()
	c.register("bob", "proj", "s1")

	start := time.Now()
	var out protocol.WaitResult
	c.mustCall(protocol.MethodWait, protocol.WaitParams{Name: "bob", Workspace: "proj", TimeoutMS: 150}, &out)
	elapsed := time.Since(start)

	if !out.TimedOut {
		t.Fatalf("wait result = %+v, want timed_out", out)
	}
	if out.Pending != 0 {
		t.Fatalf("pending = %d, want 0", out.Pending)
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("wait returned after %v, want at least the requested 150ms", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("wait overshot its timeout: %v", elapsed)
	}

	// The waiter released its slot, so nothing is left behind.
	if n := ts.srv.waiters.Len(); n != 0 {
		t.Fatalf("waiters map holds %d entries after a timeout, want 0", n)
	}

	// The connection is reusable straight after a timeout.
	c.mustCall(protocol.MethodWait, protocol.WaitParams{Name: "bob", Workspace: "proj", TimeoutMS: 50}, &out)
	if !out.TimedOut {
		t.Fatal("second wait did not time out")
	}
}

func TestWait_TimeoutIsCapped(t *testing.T) {
	ts := newTestServer(t, func(c *Config) { c.MaxWait = 120 * time.Millisecond })
	c := ts.dial()
	c.register("bob", "proj", "s1")

	start := time.Now()
	var out protocol.WaitResult
	// Ask for an hour; the daemon must impose its own ceiling.
	c.mustCall(protocol.MethodWait, protocol.WaitParams{Name: "bob", Workspace: "proj", TimeoutMS: 3600000}, &out)

	if !out.TimedOut {
		t.Fatalf("wait result = %+v, want timed_out", out)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("wait honoured the client's timeout (%v) instead of the cap", elapsed)
	}
	// H2: a timeout produced by the server's own ceiling, not the caller's
	// requested duration, must be flagged as such so the CLI can tell it
	// apart from a genuine "nothing arrived within what you asked for".
	if !out.Capped {
		t.Fatalf("wait result = %+v, want capped=true since MaxWait was the binding limit", out)
	}
}

// TestWait_NotCappedWhenTheRequestFitsUnderTheCeiling is the mirror of
// TestWait_TimeoutIsCapped: when the caller's own requested timeout is what
// actually elapses (it is under the server's ceiling), Capped must be false,
// or the CLI's re-issue loop (cmd_wait.go's waitUpTo) would spin forever
// re-asking for a timeout that will never arrive any sooner.
func TestWait_NotCappedWhenTheRequestFitsUnderTheCeiling(t *testing.T) {
	ts := newTestServer(t, func(c *Config) { c.MaxWait = time.Minute })
	c := ts.dial()
	c.register("bob", "proj", "s1")

	var out protocol.WaitResult
	c.mustCall(protocol.MethodWait, protocol.WaitParams{Name: "bob", Workspace: "proj", TimeoutMS: 100}, &out)

	if !out.TimedOut {
		t.Fatalf("wait result = %+v, want timed_out", out)
	}
	if out.Capped {
		t.Fatalf("wait result = %+v, want capped=false: the request's own timeout is what elapsed", out)
	}
}

func TestWait_ConcurrentWaitersAllWake(t *testing.T) {
	ts := newTestServer(t, nil)

	setup := ts.dial()
	setup.register("alice", "proj", "s1")
	setup.register("bob", "proj", "s2")

	const waiters = 10
	done := make(chan protocol.WaitResult, waiters)
	for i := 0; i < waiters; i++ {
		c := ts.dial()
		go func() {
			var out protocol.WaitResult
			c.mustCall(protocol.MethodWait, protocol.WaitParams{
				Name: "bob", Workspace: "proj", TimeoutMS: 30000,
			}, &out)
			done <- out
		}()
	}

	time.Sleep(200 * time.Millisecond)
	setup.send("broadcast")

	for i := 0; i < waiters; i++ {
		select {
		case out := <-done:
			if out.TimedOut {
				t.Fatalf("waiter %d timed out", i)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d waiters woke", i, waiters)
		}
	}
	if n := ts.srv.waiters.Len(); n != 0 {
		t.Fatalf("waiters map holds %d entries, want 0", n)
	}
}

// TestHandleWait_PanicStillReleasesTheWaiter proves the defer ordering
// handleWait relies on: `ch := s.waiters.Wait(addr)` is followed immediately
// by `defer s.waiters.Release(addr)`, specifically so that anything panicking
// later in the same call -- including a downstream store failure nobody
// anticipated -- still gives the slot back rather than leaking it forever, a
// leak that would matter because dispatch's own recover (a level up) turns
// the panic into an ordinary 500 and the daemon keeps running.
//
// A nil store is what actually panics here: NewServer accepts a nil *Store
// (see TestServe_NilListener), and store.PendingCount dereferences a nil
// receiver's connection pool field, which is a real, unremarkable way for a
// handler to panic in production -- not a contrived one.
func TestHandleWait_PanicStillReleasesTheWaiter(t *testing.T) {
	srv := NewServer(nil, Config{Logger: log.New(io.Discard, "", 0)})

	req := protocol.Request{ID: "1", Method: protocol.MethodWait}
	req.Params, _ = json.Marshal(protocol.WaitParams{Name: "bob", Workspace: "proj", TimeoutMS: 1000})

	resp := srv.dispatch(context.Background(), req, 0)
	if resp.Error == nil || resp.Error.Code != protocol.CodeInternal {
		t.Fatalf("dispatch response = %+v, want a 500 (the panic recovered into an internal error)", resp)
	}
	if n := srv.waiters.Len(); n != 0 {
		t.Fatalf("waiters map holds %d entries after a panic in handleWait, want 0", n)
	}

	// The server is still usable: a fresh wait on the same address blocks
	// normally rather than tripping over a leftover entry.
	req2 := protocol.Request{ID: "2", Method: protocol.MethodWho}
	req2.Params, _ = json.Marshal(protocol.WhoParams{Workspace: "proj"})
	resp2 := srv.dispatch(context.Background(), req2, 0)
	if resp2.Error == nil || resp2.Error.Code != protocol.CodeInternal {
		t.Fatalf("who against a nil store = %+v, want a 500 too (sanity: the server is still dispatching)", resp2)
	}
}

// -- shutdown ---------------------------------------------------------------

func TestGracefulShutdownUnblocksEverything(t *testing.T) {
	dir := shortTempDir(t)
	st, err := store.Open(context.Background(), filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	cfg := DefaultConfig()
	cfg.Logger = log.New(io.Discard, "", 0)
	cfg.SweepInterval = 20 * time.Millisecond
	cfg.ShutdownTimeout = 2 * time.Second

	sock := filepath.Join(dir, "s")
	ln, err := protocol.Listen(sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := NewServer(st, cfg)
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	var serveErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		serveErr = srv.Serve(ctx, ln)
	}()

	// One idle connection and one parked in a long wait: both must be released
	// by shutdown, not by their own timeouts.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	waitConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = waitConn.Close() }()

	enc := json.NewEncoder(waitConn)
	dec := json.NewDecoder(waitConn)
	reg, _ := json.Marshal(protocol.RegisterParams{Name: "bob", Workspace: "proj", SessionID: "s1"})
	if err := enc.Encode(protocol.Request{ID: "1", Method: protocol.MethodRegister, Params: reg}); err != nil {
		t.Fatalf("register: %v", err)
	}
	var resp protocol.Response
	if err := dec.Decode(&resp); err != nil || resp.Error != nil {
		t.Fatalf("register: %v %+v", err, resp.Error)
	}

	waitParams, _ := json.Marshal(protocol.WaitParams{Name: "bob", Workspace: "proj", TimeoutMS: 300000})
	if err := enc.Encode(protocol.Request{ID: "2", Method: protocol.MethodWait, Params: waitParams}); err != nil {
		t.Fatalf("wait: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // let it park

	start := time.Now()
	cancel()

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after cancellation")
	}
	if serveErr != nil {
		t.Fatalf("Serve returned %v", serveErr)
	}
	if elapsed := time.Since(start); elapsed > cfg.ShutdownTimeout+2*time.Second {
		t.Fatalf("shutdown took %v", elapsed)
	}

	// The parked wait was released rather than left hanging: either it answered
	// on the way out or the connection was closed under it. Both are clean.
	_ = waitConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := dec.Decode(&resp); err == nil && resp.Error != nil {
		t.Fatalf("parked wait failed: %v", resp.Error)
	}

	// The socket no longer accepts.
	if c, err := net.DialTimeout("unix", sock, 500*time.Millisecond); err == nil {
		_ = c.Close()
		t.Fatal("listener still accepting after shutdown")
	}
}

func TestServe_NilListener(t *testing.T) {
	srv := NewServer(nil, Config{Logger: log.New(io.Discard, "", 0)})
	if err := srv.Serve(context.Background(), nil); err == nil {
		t.Fatal("Serve(nil listener) = nil, want an error")
	}
}

// -- unit-level helpers -----------------------------------------------------

func TestConfigDefaults(t *testing.T) {
	c := Config{}.withDefaults()
	if c.StaleAfter <= 0 || c.DeadAfter <= 0 || c.SweepInterval <= 0 ||
		c.ShutdownTimeout <= 0 || c.RequestTimeout <= 0 || c.MaxRequestBytes <= 0 ||
		c.DefaultWait <= 0 || c.MaxWait <= 0 || c.Logger == nil {
		t.Fatalf("zero Config did not fill in: %+v", c)
	}
	// A caller-supplied default longer than the cap is pulled back to the cap.
	c = Config{DefaultWait: time.Hour, MaxWait: time.Minute}.withDefaults()
	if c.DefaultWait != time.Minute {
		t.Fatalf("DefaultWait = %v, want %v", c.DefaultWait, time.Minute)
	}
}

func TestClipBoundsAndSanitises(t *testing.T) {
	long := strings.Repeat("a", maxClientMsgLen*2)
	if got := clip(long); len(got) != maxClientMsgLen+3 {
		t.Fatalf("clip length = %d, want %d", len(got), maxClientMsgLen+3)
	}
	if got := clip("bad\x1b[31mescape\n"); strings.ContainsAny(got, "\x1b\n") {
		t.Fatalf("clip left control characters in %q", got)
	}
}

func TestPublicMessageHidesStorePrefix(t *testing.T) {
	if got := publicMessage(store.ErrNoSuchAgent); strings.HasPrefix(got, "store:") {
		t.Fatalf("publicMessage = %q, still carries the package prefix", got)
	}
}

func TestFailHidesInternalDetail(t *testing.T) {
	srv := NewServer(nil, Config{Logger: log.New(io.Discard, "", 0)})
	resp := srv.fail("1", fmt.Errorf("sqlite: disk image is malformed at /home/user/secret.db"), "inbox")
	if resp.Error == nil || resp.Error.Code != protocol.CodeInternal {
		t.Fatalf("resp = %+v, want a 500", resp)
	}
	if strings.Contains(resp.Error.Message, "secret.db") || strings.Contains(resp.Error.Message, "sqlite") {
		t.Fatalf("500 leaked internal detail: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "ref ") {
		t.Fatalf("500 has no log reference: %q", resp.Error.Message)
	}
}

func TestLimitedReaderIsPerRequest(t *testing.T) {
	lr := &limitedReader{r: strings.NewReader(strings.Repeat("x", 100))}
	lr.reset(10)

	buf := make([]byte, 64)
	n, err := lr.Read(buf)
	if err != nil || n != 10 {
		t.Fatalf("first read = (%d, %v), want (10, nil)", n, err)
	}
	if _, err := lr.Read(buf); err != errRequestTooLarge {
		t.Fatalf("read past budget = %v, want errRequestTooLarge", err)
	}

	// Resetting restores the budget: one connection, many requests.
	lr.reset(10)
	if n, err := lr.Read(buf); err != nil || n != 10 {
		t.Fatalf("read after reset = (%d, %v), want (10, nil)", n, err)
	}
}

func TestMessageViewConversion(t *testing.T) {
	delivered := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	m := store.Message{
		ID: "01", ThreadID: "01", FromName: "alice", FromWS: "proj",
		ToName: "bob", ToWS: "proj", Kind: store.KindNote, Body: "hi",
		CreatedAt: delivered, DeliveredAt: &delivered,
	}
	v := messageView(m)
	if v.From != "alice@proj" || v.To != "bob@proj" {
		t.Fatalf("addresses = %s -> %s", v.From, v.To)
	}
	if v.CreatedAt != "2026-07-28T12:00:00Z" {
		t.Fatalf("created_at = %q", v.CreatedAt)
	}
	if v.DeliveredAt == nil || *v.DeliveredAt != "2026-07-28T12:00:00Z" {
		t.Fatalf("delivered_at = %v", v.DeliveredAt)
	}
	if v.AckedAt != nil {
		t.Fatalf("acked_at = %v, want nil", v.AckedAt)
	}

	// A zero time renders as empty, never as year 1.
	if got := formatTime(time.Time{}); got != "" {
		t.Fatalf("formatTime(zero) = %q, want empty", got)
	}
}

func TestRequireAddress(t *testing.T) {
	if _, _, err := requireAddress("  alice  ", " proj "); err != nil {
		t.Fatalf("trimmed address rejected: %v", err)
	}
	for _, tc := range [][2]string{{"", "proj"}, {"alice", ""}, {"a@b", "proj"}, {"alice", "p@j"}} {
		if _, _, err := requireAddress(tc[0], tc[1]); err == nil {
			t.Fatalf("requireAddress(%q, %q) = nil, want an error", tc[0], tc[1])
		}
	}
}

// TestRequireAddressRejectsControlBytes is H1: name and workspace are
// rendered verbatim in every other agent's terminal, so a control byte --
// not just "@" -- must be refused at the door rather than merely stripped
// downstream.
func TestRequireAddressRejectsControlBytes(t *testing.T) {
	// TrimSpace runs first and legitimately eats leading/trailing whitespace
	// (which includes \r, \n and \t), so every control byte here sits in the
	// middle of the string where trimming cannot remove it.
	hostile := [][2]string{
		{"alice\x1b[31m-x", "proj"},         // ESC, the start of every ANSI escape
		{"ali\rce", "proj"},                 // embedded carriage return
		{"ali\nce", "proj"},                 // embedded newline
		{"ali\x07ce", "proj"},               // BEL
		{"ali\x7fce", "proj"},               // DEL
		{"alice", "proj\x1b]0;pwned\x07-x"}, // control bytes in the workspace half too
	}
	for _, tc := range hostile {
		if _, _, err := requireAddress(tc[0], tc[1]); err == nil {
			t.Fatalf("requireAddress(%q, %q) = nil, want a control-byte rejection", tc[0], tc[1])
		}
	}
}

func TestHasControlBytes(t *testing.T) {
	for _, s := range []string{"", "plain", "with space", "unicode: café"} {
		if hasControlBytes(s) {
			t.Errorf("hasControlBytes(%q) = true, want false", s)
		}
	}
	for _, s := range []string{"\x1b", "\r", "\n", "\x00", "\x7f", "prefix\x1bsuffix"} {
		if !hasControlBytes(s) {
			t.Errorf("hasControlBytes(%q) = false, want true", s)
		}
	}
}

func TestStripControlLeavesLengthAndOrdinaryTextAlone(t *testing.T) {
	if got := stripControl("plain text, nothing to strip"); got != "plain text, nothing to strip" {
		t.Fatalf("stripControl altered ordinary text: %q", got)
	}
	got := stripControl("a\x1bb\rc\nd\x7fe")
	if strings.ContainsAny(got, "\x1b\r\n\x7f") {
		t.Fatalf("stripControl left control bytes in %q", got)
	}
	if len(got) != len("a\x1bb\rc\nd\x7fe") {
		t.Fatalf("stripControl changed the length: %d vs %d", len(got), len("a\x1bb\rc\nd\x7fe"))
	}
}

// TestRegisterSanitisesMetadata is H1: harness, cwd and session_id are
// client-controlled strings that reach every `tether who`/`status`/`doctor`
// call for that agent, so a control byte in any of them must be neutralised
// before it is stored, not merely at render time.
func TestRegisterSanitisesMetadata(t *testing.T) {
	ts := newTestServer(t, nil)
	c := ts.dial()

	c.mustCall(protocol.MethodRegister, protocol.RegisterParams{
		Name: "alice", Workspace: "proj",
		Harness:   "evil\x1b[31mharness",
		SessionID: "sess\x07-1",
		Cwd:       "/tmp/\x1b]0;pwned\x07repo",
		PID:       1,
	}, &protocol.RegisterResult{})

	var who protocol.WhoResult
	c.mustCall(protocol.MethodWho, protocol.WhoParams{Workspace: "proj"}, &who)
	if len(who.Agents) != 1 {
		t.Fatalf("who = %d agents, want 1", len(who.Agents))
	}
	a := who.Agents[0]
	for _, field := range []string{a.Harness, a.Cwd} {
		if hasControlBytes(field) {
			t.Fatalf("stored agent field %q still has a control byte", field)
		}
	}

	var st protocol.StatusResult
	c.mustCall(protocol.MethodStatus, protocol.StatusParams{Name: "alice", Workspace: "proj"}, &st)

	// A same-session re-register must still be recognised as idempotent even
	// though the session id went through stripControl -- sanitising must not
	// itself corrupt the value used for the upsert guard, only the bytes a
	// terminal could act on.
	c.mustCall(protocol.MethodRegister, protocol.RegisterParams{
		Name: "alice", Workspace: "proj",
		Harness: "evil\x1b[31mharness", SessionID: "sess\x07-1", Cwd: "/tmp/repo", PID: 1,
	}, &protocol.RegisterResult{})
}
