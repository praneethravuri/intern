package main

import (
	"strings"
	"testing"
	"time"

	"github.com/praneethravuri/intern/internal/protocol"
)

// ago renders a timestamp d in the past, the way the daemon would.
func ago(d time.Duration) string {
	return time.Now().Add(-d).UTC().Format(time.RFC3339)
}

func twoMessages() protocol.InboxResult {
	return protocol.InboxResult{
		Messages: []protocol.MessageView{
			{
				ID:        "01K1QW8Z3M4T7V9XBCDEF2GH",
				From:      "agent@storefront",
				To:        "frontend@storefront",
				Kind:      kindHandoff,
				Body:      "Logo is at assets/logo.png — 512x512, transparent.",
				CreatedAt: ago(2 * time.Minute),
			},
			{
				ID:        "01K1QX9A0B1C2D3E4F5G6H7J",
				From:      "backend@storefront",
				To:        "frontend@storefront",
				Kind:      kindQuestion,
				ReplyTo:   "01KPARENT",
				Body:      "Which endpoint should return the total?\nThe cart one or the order one?",
				CreatedAt: ago(3 * time.Minute),
			},
		},
		Cleared: 2,
		Pending: 0,
	}
}

func TestInboxHappyPath(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(twoMessages()))

	r := mustRun(t, newInboxCmd(), "", "--limit", "10")

	params := decodeParams[protocol.InboxParams](t, d.registerThen(t, protocol.MethodInbox))
	if params.Name != "frontend" || params.Workspace != "storefront" {
		t.Fatalf("inbox for %s@%s, want frontend@storefront", params.Name, params.Workspace)
	}
	if params.Limit != 10 || params.Peek || params.Replay {
		t.Fatalf("limit = %d peek = %v replay = %v, want 10 / false / false", params.Limit, params.Peek, params.Replay)
	}

	out := r.stdout
	requireContains(t, out, "[01K1QW8Z3M4T7V9XBCDEF2GH] agent@storefront · handoff · 2m ago", "stdout")
	requireContains(t, out, "  Logo is at assets/logo.png — 512x512, transparent.", "stdout")
	requireContains(t, out, "3m ago", "stdout")
	requireContains(t, out, "reply to 01KPARENT", "stdout")

	// The default drain says so, plainly, and does not mention ack (the
	// command no longer exists).
	requireContains(t, out, "2 messages, inbox cleared.", "stdout")
	requireNotContains(t, out, "ack with", "stdout")

	// A multi-line body stays readable, indented under its header.
	requireContains(t, out, "  Which endpoint should return the total?\n  The cart one or the order one?",
		"stdout")
}

func TestInboxSingularWording(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID: "01K", From: "agent@storefront", Kind: kindNote, Body: "hi", CreatedAt: ago(time.Second),
	}}, Cleared: 1}))

	r := mustRun(t, newInboxCmd(), "")
	requireContains(t, r.stdout, "1 message, inbox cleared.", "stdout")
	if strings.Contains(r.stdout, "1 messages") {
		t.Fatalf("stdout says \"1 messages\":\n%s", r.stdout)
	}
}

func TestInboxEmpty(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{}))

	r := mustRun(t, newInboxCmd(), "")

	requireContains(t, r.stdout, "0 messages.", "stdout")
	requireContains(t, r.stdout, "Next: intern wait", "stdout")
	requireNotContains(t, r.stdout, "ack with", "stdout")
	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0 for an empty inbox", got)
	}
}

// TestInboxPeekDoesNotClear checks both the request (Peek: true, not a
// drain) and the human-visible "not cleared" wording that tells a caller
// this read left the mail in place.
func TestInboxPeekDoesNotClear(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID: "01K", From: "agent@storefront", Kind: kindNote, Body: "hi", CreatedAt: ago(time.Second),
	}}, Pending: 1}))

	r := mustRun(t, newInboxCmd(), "", "--peek")

	params := decodeParams[protocol.InboxParams](t, d.registerThen(t, protocol.MethodInbox))
	if !params.Peek || params.Replay {
		t.Fatalf("peek = %v replay = %v, want true / false", params.Peek, params.Replay)
	}

	requireContains(t, r.stdout, "1 message (peek — not cleared).", "stdout")
	requireNotContains(t, r.stdout, "inbox cleared", "stdout")
}

// TestInboxPeekReportsTheRealTotalWhenLimitCutTheListShort is AXI principle
// 1: a peek never changes anything, so Pending is the true total that
// exists -- when --limit shows fewer than that, the output must say so
// rather than let "1 message" pass for the whole inbox.
func TestInboxPeekReportsTheRealTotalWhenLimitCutTheListShort(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID: "01K", From: "agent@storefront", Kind: kindNote, Body: "hi", CreatedAt: ago(time.Second),
	}}, Pending: 5}))

	r := mustRun(t, newInboxCmd(), "", "--peek", "--limit", "1")
	requireContains(t, r.stdout, "1 of 5 messages pending (peek — not cleared).", "stdout")
}

// TestInboxPeekOmitsTheTotalWhenNothingWasCutShort keeps the ordinary case
// unchanged: when everything pending fit in the list, naming a redundant
// total would just be noise.
func TestInboxPeekOmitsTheTotalWhenNothingWasCutShort(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID: "01K", From: "agent@storefront", Kind: kindNote, Body: "hi", CreatedAt: ago(time.Second),
	}}, Pending: 1}))

	r := mustRun(t, newInboxCmd(), "", "--peek")
	requireContains(t, r.stdout, "1 message (peek — not cleared).", "stdout")
	requireNotContains(t, r.stdout, " of ", "stdout")
}

// TestInboxDrainNotesMailLeftBehindByLimit is the same principle for a real
// drain: a nonzero Pending after --limit cut the drain short means the
// inbox is not actually empty, so "cleared" alone would be a misleadingly
// partial claim -- the daemon-honest total has to say so.
func TestInboxDrainNotesMailLeftBehindByLimit(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID: "01K", From: "agent@storefront", Kind: kindNote, Body: "hi", CreatedAt: ago(time.Second),
	}}, Cleared: 1, Pending: 4}))

	r := mustRun(t, newInboxCmd(), "", "--limit", "1")
	requireContains(t, r.stdout, "1 message, inbox cleared — 4 messages still pending, run `intern inbox` again.", "stdout")
}

func TestInboxReplay(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.InboxResult{}))

	mustRun(t, newInboxCmd(), "", "--replay")

	params := decodeParams[protocol.InboxParams](t, d.registerThen(t, protocol.MethodInbox))
	if !params.Replay || params.Peek {
		t.Fatalf("replay = %v peek = %v, want true / false", params.Replay, params.Peek)
	}
}

// TestInboxPeekAndReplayTogetherFailsClientSide is P1: the two flags are
// mutually exclusive, and the CLI must catch that itself before the request
// ever reaches the daemon.
func TestInboxPeekAndReplayTogetherFailsClientSide(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.InboxResult{}))

	r := run(t, newInboxCmd(), "", "--peek", "--replay")
	if r.err == nil {
		t.Fatal("--peek --replay together succeeded")
	}
	if got := r.exitCode(); got != exitGeneral {
		t.Fatalf("exit code = %d, want %d", got, exitGeneral)
	}
	if n := len(d.requests()); n != 0 {
		t.Fatalf("a request reached the daemon %d times, want 0", n)
	}
}

func TestInboxJSON(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	want := twoMessages()
	newFakeDaemon(t, okHandler(want))

	r := mustRun(t, newInboxCmd(), "", "--json")

	var got protocol.InboxResult
	unmarshalJSON(t, r.stdout, &got)
	if len(got.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(got.Messages))
	}
	if got.Messages[0].Body != want.Messages[0].Body {
		t.Fatalf("body = %q, want %q", got.Messages[0].Body, want.Messages[0].Body)
	}
	if got.Messages[0].ID != want.Messages[0].ID {
		t.Fatalf("id = %q, want %q", got.Messages[0].ID, want.Messages[0].ID)
	}
	if got.Cleared != 2 {
		t.Fatalf("cleared = %d, want 2", got.Cleared)
	}
}

// An empty inbox in JSON mode is an empty array, never null: callers index it.
func TestInboxJSONEmptyIsAnArray(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{}))

	r := mustRun(t, newInboxCmd(), "", "--json")
	requireContains(t, r.stdout, `"messages": []`, "stdout")

	var got protocol.InboxResult
	unmarshalJSON(t, r.stdout, &got)
}

// A body full of shell metacharacters must render, and reach --json, unchanged.
func TestInboxRendersHostileBodies(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID:        "01K",
		From:      "agent@storefront",
		Kind:      kindNote,
		Body:      hostileBody,
		CreatedAt: ago(time.Second),
	}}}))

	r := mustRun(t, newInboxCmd(), "", "--json")

	var got protocol.InboxResult
	unmarshalJSON(t, r.stdout, &got)
	if got.Messages[0].Body != hostileBody {
		t.Fatalf("body = %q, want %q", got.Messages[0].Body, hostileBody)
	}
}

// escapeHostileBody is what H1 is actually about: bytes a terminal would act
// on rather than print, most importantly the ESC that begins an ANSI escape
// sequence. This is a different threat than hostileBody (cmd_send_test.go),
// which is shell-hostile, not terminal-hostile.
const escapeHostileBody = "before\x1b[2J\x1b[Hafter: screen cleared\r\x07bell and cr"

// TestInboxHumanOutputSanitisesControlBytesButJSONStaysByteExact is H1's
// central guarantee: sanitising is a property of the human-text renderer
// only. --json is the byte-exact contract every programmatic consumer
// relies on, so it must reproduce the hostile body completely unchanged
// even though the human view neutralises it.
func TestInboxHumanOutputSanitisesControlBytesButJSONStaysByteExact(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	msg := protocol.MessageView{
		ID:        "01K",
		From:      "agent@storefront",
		Kind:      kindNote,
		Body:      escapeHostileBody,
		CreatedAt: ago(time.Second),
	}

	t.Run("human output", func(t *testing.T) {
		newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{msg}}))
		r := mustRun(t, newInboxCmd(), "")
		if strings.ContainsAny(r.stdout, "\x1b\r\x07") {
			t.Fatalf("human inbox output still contains a raw control byte:\n%q", r.stdout)
		}
		// The rest of the body -- the part that is not a control byte -- must
		// still be legible, i.e. sanitising is not just blanking the message.
		requireContains(t, r.stdout, "screen cleared", "stdout")
		requireContains(t, r.stdout, "bell and cr", "stdout")
	})

	t.Run("json output is byte exact", func(t *testing.T) {
		newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{msg}}))
		r := mustRun(t, newInboxCmd(), "", "--json")

		var got protocol.InboxResult
		unmarshalJSON(t, r.stdout, &got)
		if got.Messages[0].Body != escapeHostileBody {
			t.Fatalf("--json body was altered:\n got:  %q\nwant: %q", got.Messages[0].Body, escapeHostileBody)
		}
	})
}

// TestInboxHumanOutputCarriesAnUntrustedContentNotice is H1: the human
// inbox render must tell an LLM reading it that message bodies are data
// from other agents, not instructions, once per batch rather than once per
// message (so it does not bloat the output).
func TestInboxHumanOutputCarriesAnUntrustedContentNotice(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(twoMessages()))

	r := mustRun(t, newInboxCmd(), "")

	if n := strings.Count(r.stdout, untrustedContentNotice); n != 1 {
		t.Fatalf("untrusted-content notice appeared %d times, want exactly 1:\n%s", n, r.stdout)
	}
}

// An empty inbox has no bodies to warn about, so the notice would be noise.
func TestInboxEmptyHasNoUntrustedContentNotice(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{}))

	r := mustRun(t, newInboxCmd(), "")
	requireNotContains(t, r.stdout, untrustedContentNotice, "stdout")
}

// --json must never carry the human-only notice: it is not part of the
// programmatic contract.
func TestInboxJSONHasNoUntrustedContentNotice(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(twoMessages()))

	r := mustRun(t, newInboxCmd(), "", "--json")
	requireNotContains(t, r.stdout, untrustedContentNotice, "stdout")
}

// TestInboxDroppedWarningGoesToStderr checks the P1-adjacent contract: a
// nonzero Dropped is a warning about the depth cap, not part of the
// machine-parseable channel, so it must land on stderr, never stdout.
func TestInboxDroppedWarningGoesToStderr(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{
		Messages: []protocol.MessageView{{
			ID: "01K", From: "agent@storefront", Kind: kindNote, Body: "hi", CreatedAt: ago(time.Second),
		}},
		Cleared: 1,
		Dropped: 3,
	}))

	r := mustRun(t, newInboxCmd(), "")
	requireContains(t, r.stderr, "3 messages were dropped", "stderr")
	requireNotContains(t, r.stdout, "dropped", "stdout")
}

func TestInboxWithoutADaemonExitsThree(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	r := run(t, newInboxCmd(), "")
	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
}

func TestInboxRejectsANegativeLimit(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.InboxResult{}))

	r := run(t, newInboxCmd(), "", "--limit", "-1")
	if r.err == nil {
		t.Fatal("inbox accepted a negative limit")
	}
	if n := len(d.requests()); n != 0 {
		t.Fatalf("a bad request reached the daemon %d times", n)
	}
}

// A daemon speaking garbage must not panic the renderer.
func TestInboxSurvivesAMalformedResponse(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newRawDaemon(t, []byte("not json at all\n"))

	r := run(t, newInboxCmd(), "")
	if r.err == nil {
		t.Fatal("inbox succeeded against a daemon speaking garbage")
	}
	requireContains(t, r.err.Error(), "malformed response", "error")
}

// Timestamps the CLI cannot parse are shown, not hidden or crashed on.
func TestInboxToleratesOddTimestamps(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID: "01K", From: "agent@storefront", Kind: kindNote, Body: "hi", CreatedAt: "yesterday",
	}}}))

	r := mustRun(t, newInboxCmd(), "")
	requireContains(t, r.stdout, "yesterday", "stdout")
}

// TestInboxTruncatesLongBodiesByDefault checks the human view caps a message
// body at defaultBodyDisplayMax and says so, while leaving --json (checked
// separately below) completely untouched.
func TestInboxTruncatesLongBodiesByDefault(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	long := strings.Repeat("x", defaultBodyDisplayMax+500)
	newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID: "01K", From: "backend@storefront", Kind: kindNote, Body: long, CreatedAt: ago(time.Second),
	}}}))

	r := mustRun(t, newInboxCmd(), "")
	requireContains(t, r.stdout, strings.Repeat("x", defaultBodyDisplayMax), "stdout")
	requireContains(t, r.stdout, "--full for all", "stdout")
	if longestRunOf(r.stdout, 'x') > defaultBodyDisplayMax {
		t.Fatalf("stdout contains a run of more than %d body characters, want it truncated at %d",
			defaultBodyDisplayMax, defaultBodyDisplayMax)
	}
}

// longestRunOf returns the length of the longest unbroken run of r in s.
// Used instead of strings.Count so ordinary English words containing the
// probe rune (e.g. "inbox", "Next") do not throw off a truncation-boundary
// check aimed only at the message body itself.
func longestRunOf(s string, r rune) int {
	longest, cur := 0, 0
	for _, c := range s {
		if c == r {
			cur++
			if cur > longest {
				longest = cur
			}
		} else {
			cur = 0
		}
	}
	return longest
}

// TestInboxFullFlagShowsTheWholeBody checks --full bypasses the truncation
// entirely.
func TestInboxFullFlagShowsTheWholeBody(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	long := strings.Repeat("x", defaultBodyDisplayMax+500)
	newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID: "01K", From: "agent@storefront", Kind: kindNote, Body: long, CreatedAt: ago(time.Second),
	}}}))

	r := mustRun(t, newInboxCmd(), "", "--full")
	requireContains(t, r.stdout, long, "stdout")
	requireNotContains(t, r.stdout, "--full for all", "stdout")
}

// A short body is never truncated, and never grows a truncation hint it does
// not need.
func TestInboxShortBodyIsNeverTruncated(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.InboxResult{Messages: []protocol.MessageView{{
		ID: "01K", From: "agent@storefront", Kind: kindNote, Body: "short and sweet", CreatedAt: ago(time.Second),
	}}}))

	r := mustRun(t, newInboxCmd(), "")
	requireContains(t, r.stdout, "short and sweet", "stdout")
	requireNotContains(t, r.stdout, "--full for all", "stdout")
}

// TestAckCommandIsGone confirms `intern ack` is no longer a recognised
// subcommand at all -- draining is the only way to acknowledge mail now.
func TestAckCommandIsGone(t *testing.T) {
	r := run(t, newRootCmd(), "", "ack", "01K")
	if r.err == nil {
		t.Fatal("`intern ack` was accepted; it should no longer exist")
	}
	requireContains(t, r.err.Error(), "unknown command", "error")
}
