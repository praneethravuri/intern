package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// pair registers alice and bob in workspace "ws".
func pair(t *testing.T, s *Store) {
	t.Helper()
	mustRegister(t, s, Agent{Workspace: "ws", Name: "alice", Cwd: "/a", SessionID: "sa"})
	mustRegister(t, s, Agent{Workspace: "ws", Name: "bob", Cwd: "/b", SessionID: "sb"})
}

func ids(ms []Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// -------------------------------------------------------------- send ------

func TestSendToUnregisteredRecipient(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	mustRegister(t, s, Agent{Workspace: "ws", Name: "alice", Cwd: "/a"})

	_, err := s.Send(ctx, note("hello?"))
	if !errors.Is(err, ErrNoSuchAgent) {
		t.Fatalf("Send to unregistered = %v, want ErrNoSuchAgent", err)
	}
}

func TestSendBodyValidation(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	if _, err := s.Send(ctx, note("")); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("empty body = %v, want ErrEmptyBody", err)
	}
	if _, err := s.Send(ctx, note("   \n\t ")); !errors.Is(err, ErrEmptyBody) {
		t.Errorf("whitespace body = %v, want ErrEmptyBody", err)
	}

	big := strings.Repeat("x", s.MaxBodyBytes+1)
	if _, err := s.Send(ctx, note(big)); !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("oversized body = %v, want ErrBodyTooLarge", err)
	}

	// Exactly at the limit is fine.
	if _, err := s.Send(ctx, note(strings.Repeat("x", s.MaxBodyBytes))); err != nil {
		t.Errorf("body at limit: %v", err)
	}

	// A lowered cap is honoured too.
	s.MaxBodyBytes = 8
	if _, err := s.Send(ctx, note("123456789")); !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("custom cap = %v, want ErrBodyTooLarge", err)
	}
}

func TestSendBadAddress(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	m := note("hi")
	m.ToName = ""
	if _, err := s.Send(ctx, m); !errors.Is(err, ErrBadAddress) {
		t.Errorf("empty recipient = %v, want ErrBadAddress", err)
	}

	m = note("hi")
	m.FromWS = ""
	if _, err := s.Send(ctx, m); !errors.Is(err, ErrBadAddress) {
		t.Errorf("empty sender = %v, want ErrBadAddress", err)
	}
}

func TestSendToStaleAgentStillQueues(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now
	pair(t, s)

	// Bob has not been seen in hours -- mail must still queue for him.
	clk.advance(6 * time.Hour)
	if _, err := s.Send(ctx, note("still there?")); err != nil {
		t.Fatalf("Send to stale agent: %v", err)
	}
	n, err := s.PendingCount(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("PendingCount = %d, want 1", n)
	}
}

// ------------------------------------------------------------- the invariant

func TestInboxTwiceReturnsSameMessage(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	id1 := mustSend(t, s, note("first"))
	id2 := mustSend(t, s, note("second"))

	first, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox #1: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("Inbox #1 = %d messages, want 2", len(first))
	}

	second, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox #2: %v", err)
	}
	if len(second) != 2 {
		t.Fatalf("Inbox #2 = %d messages, want 2 (read must not delete)", len(second))
	}

	if got, want := ids(second), ids(first); !equalStrings(got, want) {
		t.Fatalf("Inbox #2 ids = %v, want %v", got, want)
	}
	if first[0].ID != id1 || first[1].ID != id2 {
		t.Fatalf("ids = %v, want [%s %s]", ids(first), id1, id2)
	}

	// A third read for good measure, and the pending count is unchanged.
	third, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox #3: %v", err)
	}
	if len(third) != 2 {
		t.Fatalf("Inbox #3 = %d messages, want 2", len(third))
	}
	n, err := s.PendingCount(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("PendingCount after 3 reads = %d, want 2", n)
	}
}

func TestInboxStampsDeliveredButNotAcked(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now
	pair(t, s)

	mustSend(t, s, note("hello"))

	// Before any read, delivered_at is NULL.
	pre, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(pre) != 1 {
		t.Fatalf("Inbox = %d, want 1", len(pre))
	}
	if pre[0].DeliveredAt == nil {
		t.Fatal("DeliveredAt is nil after first read, want stamped")
	}
	if pre[0].AckedAt != nil {
		t.Fatalf("AckedAt = %v, want nil (reading is not acking)", pre[0].AckedAt)
	}
	firstDelivery := pre[0].DeliveredAt.UnixMilli()

	// Re-reading later must not move the original delivery timestamp.
	clk.advance(time.Hour)
	post, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox #2: %v", err)
	}
	if post[0].DeliveredAt == nil {
		t.Fatal("DeliveredAt lost on redelivery")
	}
	if got := post[0].DeliveredAt.UnixMilli(); got != firstDelivery {
		t.Errorf("DeliveredAt moved: %d -> %d", firstDelivery, got)
	}
	if post[0].AckedAt != nil {
		t.Errorf("AckedAt = %v, want nil", post[0].AckedAt)
	}
	if post[0].CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

// -------------------------------------------------------------- drain ------

func TestDrainReturnsOldestFirstAndAcks(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	id1 := mustSend(t, s, note("first"))
	id2 := mustSend(t, s, note("second"))

	msgs, dropped, err := s.Drain(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(msgs) != 2 || msgs[0].ID != id1 || msgs[1].ID != id2 {
		t.Fatalf("Drain = %v, want [%s %s]", ids(msgs), id1, id2)
	}
	for _, m := range msgs {
		if m.AckedAt == nil {
			t.Errorf("message %s AckedAt is nil after Drain", m.ID)
		}
	}

	// A second Drain sees nothing: the first call already acked everything.
	again, _, err := s.Drain(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("second Drain: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second Drain = %v, want empty", ids(again))
	}

	// A non-destructive peek agrees: nothing pending.
	pending, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("Inbox after Drain = %v, want empty", ids(pending))
	}
}

func TestDrainOnCancelledContextLeavesMailUnacked(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	id1 := mustSend(t, s, note("one"))
	id2 := mustSend(t, s, note("two"))

	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	if _, _, err := s.Drain(cancelled, "ws", "bob", 10); err == nil {
		t.Fatal("Drain succeeded against an already-cancelled context")
	}

	// The failed attempt must have acked nothing: a fresh Drain still finds
	// both messages, in order.
	msgs, _, err := s.Drain(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Drain after cancellation: %v", err)
	}
	if len(msgs) != 2 || msgs[0].ID != id1 || msgs[1].ID != id2 {
		t.Fatalf("Drain after a cancelled attempt = %v, want [%s %s] (nothing should have been acked)",
			ids(msgs), id1, id2)
	}
}

func TestDrainReportsAndResetsDropped(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)
	mustSend(t, s, note("hi"))

	if _, err := s.w.ExecContext(ctx,
		`UPDATE agents SET dropped = 5 WHERE workspace = ? AND name = ?`, "ws", "bob"); err != nil {
		t.Fatalf("seed dropped: %v", err)
	}

	_, dropped, err := s.Drain(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if dropped != 5 {
		t.Fatalf("dropped = %d, want 5", dropped)
	}

	a, err := s.GetAgent(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if a.Dropped != 0 {
		t.Fatalf("agents.dropped after Drain = %d, want reset to 0", a.Dropped)
	}

	// A second Drain reports 0: nothing new was dropped.
	_, dropped2, err := s.Drain(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("second Drain: %v", err)
	}
	if dropped2 != 0 {
		t.Fatalf("second Drain dropped = %d, want 0", dropped2)
	}
}

func TestReplayOnlyShowsMessagesAfterADrain(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	id := mustSend(t, s, note("peek me"))

	if _, err := s.Inbox(ctx, "ws", "bob", 10); err != nil {
		t.Fatalf("Inbox (peek): %v", err)
	}
	replay, err := s.Replay(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Replay after peek: %v", err)
	}
	if len(replay) != 0 {
		t.Fatalf("Replay after a peek = %v, want empty (peeking must not ack)", ids(replay))
	}

	if _, _, err := s.Drain(ctx, "ws", "bob", 10); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	replay, err = s.Replay(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Replay after drain: %v", err)
	}
	if len(replay) != 1 || replay[0].ID != id {
		t.Fatalf("Replay after Drain = %v, want [%s]", ids(replay), id)
	}
}

// ---------------------------------------------------------------- ack ------

func TestAckRemovesFromInboxAndReplayKeeps(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	id1 := mustSend(t, s, note("one"))
	id2 := mustSend(t, s, note("two"))

	n, err := s.Ack(ctx, "ws", "bob", []string{id1})
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if n != 1 {
		t.Fatalf("Ack = %d, want 1", n)
	}

	// Acking the same id again is a no-op.
	n, err = s.Ack(ctx, "ws", "bob", []string{id1})
	if err != nil {
		t.Fatalf("Ack #2: %v", err)
	}
	if n != 0 {
		t.Fatalf("second Ack = %d, want 0 (idempotent)", n)
	}

	inbox, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox) != 1 || inbox[0].ID != id2 {
		t.Fatalf("Inbox after ack = %v, want [%s]", ids(inbox), id2)
	}

	replay, err := s.Replay(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(replay) != 1 || replay[0].ID != id1 {
		t.Fatalf("Replay = %v, want [%s]", ids(replay), id1)
	}
	if replay[0].AckedAt == nil {
		t.Error("replayed message has nil AckedAt")
	}
}

func TestAckIsScopedToRecipient(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)
	mustRegister(t, s, Agent{Workspace: "ws", Name: "carol", Cwd: "/c"})

	id1 := mustSend(t, s, note("for bob"))

	// Carol must not be able to retire bob's mail.
	n, err := s.Ack(ctx, "ws", "carol", []string{id1})
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if n != 0 {
		t.Fatalf("cross-agent Ack = %d, want 0", n)
	}
	if pending, _ := s.PendingCount(ctx, "ws", "bob"); pending != 1 {
		t.Fatalf("bob pending = %d, want 1", pending)
	}
}

func TestAckEmptyAndUnknownIDs(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	n, err := s.Ack(ctx, "ws", "bob", nil)
	if err != nil || n != 0 {
		t.Fatalf("Ack(nil) = %d, %v; want 0, nil", n, err)
	}
	n, err = s.Ack(ctx, "ws", "bob", []string{"nope", "also-nope"})
	if err != nil || n != 0 {
		t.Fatalf("Ack(unknown) = %d, %v; want 0, nil", n, err)
	}
}

// The ids must be bound as parameters, never spliced into SQL text.
func TestAckIDsAreNotInjectable(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)
	mustSend(t, s, note("keep me"))

	evil := "x'); DELETE FROM messages; --"
	if _, err := s.Ack(ctx, "ws", "bob", []string{evil}); err != nil {
		t.Fatalf("Ack(evil): %v", err)
	}
	if n, _ := s.PendingCount(ctx, "ws", "bob"); n != 1 {
		t.Fatalf("PendingCount = %d, want 1 (injection must not fire)", n)
	}
}

// ------------------------------------------------------------- threading ---

func TestReplyInheritsThreadID(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	rootID := mustSend(t, s, Message{
		FromName: "alice", FromWS: "ws", ToName: "bob", ToWS: "ws",
		Kind: KindQuestion, Body: "what is the plan?",
	})

	got, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if got[0].ThreadID != rootID {
		t.Fatalf("root ThreadID = %q, want its own id %q", got[0].ThreadID, rootID)
	}

	replyID := mustSend(t, s, Message{
		FromName: "bob", FromWS: "ws", ToName: "alice", ToWS: "ws",
		Kind: KindAnswer, Body: "ship it", ReplyTo: rootID,
	})

	back, err := s.Inbox(ctx, "ws", "alice", 10)
	if err != nil {
		t.Fatalf("Inbox(alice): %v", err)
	}
	if len(back) != 1 {
		t.Fatalf("alice inbox = %d, want 1", len(back))
	}
	if back[0].ID != replyID {
		t.Fatalf("reply id mismatch")
	}
	if back[0].ThreadID != rootID {
		t.Errorf("reply ThreadID = %q, want %q", back[0].ThreadID, rootID)
	}
	if back[0].ReplyTo != rootID {
		t.Errorf("reply ReplyTo = %q, want %q", back[0].ReplyTo, rootID)
	}

	// A grandchild reply stays on the same thread.
	grand := mustSend(t, s, Message{
		FromName: "alice", FromWS: "ws", ToName: "bob", ToWS: "ws",
		Body: "thanks", ReplyTo: replyID,
	})
	msgs, _ := s.Inbox(ctx, "ws", "bob", 10)
	var found bool
	for _, m := range msgs {
		if m.ID == grand {
			found = true
			if m.ThreadID != rootID {
				t.Errorf("grandchild ThreadID = %q, want %q", m.ThreadID, rootID)
			}
		}
	}
	if !found {
		t.Error("grandchild reply not delivered")
	}
}

func TestReplyToUnknownMessage(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	m := note("orphan")
	m.ReplyTo = "01ZZZZZZZZZZZZZZZZZZZZZZZZ"
	if _, err := s.Send(ctx, m); !errors.Is(err, ErrNoSuchMessage) {
		t.Fatalf("reply to unknown = %v, want ErrNoSuchMessage", err)
	}
	// The failed send must not have left anything behind.
	if n, _ := s.PendingCount(ctx, "ws", "bob"); n != 0 {
		t.Fatalf("PendingCount = %d, want 0 (transaction should roll back)", n)
	}
}

// ------------------------------------------------------------- sweep -------

func TestSweepDead(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now
	pair(t, s)

	oldID := mustSend(t, s, note("ancient"))

	clk.advance(3 * time.Hour)
	freshID := mustSend(t, s, note("recent"))

	n, err := s.SweepDead(ctx, time.Hour)
	if err != nil {
		t.Fatalf("SweepDead: %v", err)
	}
	if n != 1 {
		t.Fatalf("SweepDead = %d, want 1", n)
	}

	inbox, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox) != 1 || inbox[0].ID != freshID {
		t.Fatalf("Inbox after sweep = %v, want [%s]", ids(inbox), freshID)
	}
	if pending, _ := s.PendingCount(ctx, "ws", "bob"); pending != 1 {
		t.Fatalf("PendingCount = %d, want 1", pending)
	}

	// Sweeping again finds nothing new to mark.
	if n, err = s.SweepDead(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("second SweepDead = %d, %v; want 0, nil", n, err)
	}

	// Dead mail is not acked history either.
	replay, err := s.Replay(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	for _, m := range replay {
		if m.ID == oldID {
			t.Error("dead message showed up in Replay")
		}
	}

	// Acked messages are never swept.
	if _, err := s.Ack(ctx, "ws", "bob", []string{freshID}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	clk.advance(10 * time.Hour)
	if n, err = s.SweepDead(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("SweepDead over acked mail = %d, %v; want 0, nil", n, err)
	}
	if replay, _ = s.Replay(ctx, "ws", "bob", 10); len(replay) != 1 {
		t.Fatalf("Replay = %d, want 1", len(replay))
	}
}

// TestPurgeMessages proves the database plateaus instead of growing forever:
// read (acked) and dead mail past the retention window is actually deleted,
// not just marked, while pending mail is never touched regardless of age.
func TestPurgeMessages(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	clk := newClock()
	s.Now = clk.Now
	pair(t, s)

	ackedID := mustSend(t, s, note("read long ago"))
	_ = mustSend(t, s, note("never read"))
	if _, err := s.Ack(ctx, "ws", "bob", []string{ackedID}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	clk.advance(time.Second)
	if _, err := s.SweepDead(ctx, 0); err != nil { // cutoff = now, strictly after the dead message's created_at
		t.Fatalf("SweepDead: %v", err)
	}

	clk.advance(6 * 24 * time.Hour)
	pendingID := mustSend(t, s, note("still pending"))

	const retention = 7 * 24 * time.Hour
	clk.advance(2 * 24 * time.Hour) // ackedID/deadID now 8 days old, pendingID 2 days old

	n, err := s.PurgeMessages(ctx, retention)
	if err != nil {
		t.Fatalf("PurgeMessages: %v", err)
	}
	if n != 2 {
		t.Fatalf("PurgeMessages = %d, want 2 (the acked and dead messages)", n)
	}

	replay, err := s.Replay(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	for _, m := range replay {
		if m.ID == ackedID {
			t.Error("purged acked message still shows up in Replay")
		}
	}

	inbox, err := s.Inbox(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox) != 1 || inbox[0].ID != pendingID {
		t.Fatalf("Inbox after purge = %v, want only the still-pending message %s", ids(inbox), pendingID)
	}

	// Purging again finds nothing new.
	if n, err = s.PurgeMessages(ctx, retention); err != nil || n != 0 {
		t.Fatalf("second PurgeMessages = %d, %v; want 0, nil", n, err)
	}
}

// -------------------------------------------------------------- ordering ---

// No sleep between sends, so most share a millisecond and exercise monotonic ULID ordering.
func TestULIDOrderingPreservesSendOrder(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	const n = 50
	sent := make([]string, 0, n)
	for i := range n {
		sent = append(sent, mustSend(t, s, note(fmt.Sprintf("msg-%02d", i))))
	}

	got, err := s.Inbox(ctx, "ws", "bob", 100)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(got) != n {
		t.Fatalf("Inbox = %d messages, want %d", len(got), n)
	}
	if !equalStrings(ids(got), sent) {
		t.Fatalf("inbox order does not match send order\n got %v\nwant %v", ids(got), sent)
	}
	for i, m := range got {
		if want := fmt.Sprintf("msg-%02d", i); m.Body != want {
			t.Fatalf("message %d body = %q, want %q", i, m.Body, want)
		}
	}

	// A short limit takes the oldest messages first.
	head, err := s.Inbox(ctx, "ws", "bob", 5)
	if err != nil {
		t.Fatalf("Inbox(limit 5): %v", err)
	}
	if !equalStrings(ids(head), sent[:5]) {
		t.Fatalf("limited inbox = %v, want %v", ids(head), sent[:5])
	}
}

func TestReplayReturnsOldestFirst(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	// No sleep between sends: monotonic ULIDs order same-millisecond sends.
	var sent []string
	for i := range 5 {
		sent = append(sent, mustSend(t, s, note(fmt.Sprintf("m%d", i))))
	}
	if _, err := s.Ack(ctx, "ws", "bob", sent); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	all, err := s.Replay(ctx, "ws", "bob", 10)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !equalStrings(ids(all), sent) {
		t.Fatalf("Replay = %v, want %v", ids(all), sent)
	}

	// A limit keeps the most recent, still ascending.
	tail, err := s.Replay(ctx, "ws", "bob", 2)
	if err != nil {
		t.Fatalf("Replay(2): %v", err)
	}
	if !equalStrings(ids(tail), sent[3:]) {
		t.Fatalf("Replay(2) = %v, want %v", ids(tail), sent[3:])
	}
}

// ----------------------------------------------------------- concurrency ---

// Must pass under -race: nothing lost, nothing acked twice.
func TestConcurrentSendAndRead(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	const (
		senders        = 50
		perSender      = 4
		readers        = 10
		total          = senders * perSender
		inboxBatchSize = 25
	)

	var (
		mu       sync.Mutex
		seen     = make(map[string]int) // id -> times returned by Inbox
		ackedSum int                    // total rows actually retired
	)

	record := func(ms []Message) {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range ms {
			seen[m.ID]++
		}
	}
	addAcked := func(n int) {
		mu.Lock()
		defer mu.Unlock()
		ackedSum += n
	}

	stop := make(chan struct{})

	var readWG sync.WaitGroup
	for r := range readers {
		readWG.Add(1)
		go func(r int) {
			defer readWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ms, err := s.Inbox(ctx, "ws", "bob", inboxBatchSize)
				if err != nil {
					t.Errorf("reader %d Inbox: %v", r, err)
					return
				}
				if len(ms) == 0 {
					time.Sleep(time.Millisecond)
					continue
				}
				record(ms)
				n, err := s.Ack(ctx, "ws", "bob", ids(ms))
				if err != nil {
					t.Errorf("reader %d Ack: %v", r, err)
					return
				}
				addAcked(n)
			}
		}(r)
	}

	var sendWG sync.WaitGroup
	for g := range senders {
		sendWG.Add(1)
		go func(g int) {
			defer sendWG.Done()
			for i := range perSender {
				body := fmt.Sprintf("g%02d-i%d", g, i)
				if _, err := s.Send(ctx, note(body)); err != nil {
					t.Errorf("sender %d Send: %v", g, err)
					return
				}
			}
		}(g)
	}

	sendWG.Wait()
	close(stop)
	readWG.Wait()

	if t.Failed() {
		t.Fatal("concurrent phase reported errors")
	}

	// Drain whatever the readers did not get to.
	for {
		ms, err := s.Inbox(ctx, "ws", "bob", 100)
		if err != nil {
			t.Fatalf("drain Inbox: %v", err)
		}
		if len(ms) == 0 {
			break
		}
		record(ms)
		n, err := s.Ack(ctx, "ws", "bob", ids(ms))
		if err != nil {
			t.Fatalf("drain Ack: %v", err)
		}
		addAcked(n)
	}

	// Every message was acked exactly once: no loss, no double retirement.
	if ackedSum != total {
		t.Errorf("total acked rows = %d, want %d", ackedSum, total)
	}
	// Every message was observed at least once: no loss.
	if len(seen) != total {
		t.Errorf("distinct messages seen = %d, want %d", len(seen), total)
	}
	if n, err := s.PendingCount(ctx, "ws", "bob"); err != nil || n != 0 {
		t.Errorf("PendingCount = %d, %v; want 0, nil", n, err)
	}
	replay, err := s.Replay(ctx, "ws", "bob", total+10)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(replay) != total {
		t.Errorf("Replay = %d messages, want %d", len(replay), total)
	}

	bodies := make(map[string]bool, total)
	for _, m := range replay {
		if bodies[m.Body] {
			t.Errorf("duplicate body in history: %q", m.Body)
		}
		bodies[m.Body] = true
		if m.DeliveredAt == nil {
			t.Errorf("message %s has nil DeliveredAt", m.ID)
		}
		if m.AckedAt == nil {
			t.Errorf("message %s has nil AckedAt", m.ID)
		}
	}
	for g := range senders {
		for i := range perSender {
			if want := fmt.Sprintf("g%02d-i%d", g, i); !bodies[want] {
				t.Errorf("message %q was lost", want)
			}
		}
	}
}

// TestConcurrentRegisterSingleWinner verifies the guarded upsert has no
// read-then-write race: only one session can claim a free name.
func TestConcurrentRegisterSingleWinner(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const contenders = 20
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		taken int
	)
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := Agent{
				Workspace: "ws",
				Name:      "contested",
				SessionID: fmt.Sprintf("sess-%02d", i),
				Cwd:       "/tmp",
			}
			err := s.Register(ctx, a, s.now().Add(-time.Hour))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrNameTaken):
				taken++
			default:
				t.Errorf("Register: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("winners = %d, want exactly 1", wins)
	}
	if wins+taken != contenders {
		t.Errorf("wins+taken = %d, want %d", wins+taken, contenders)
	}
}

// ------------------------------------------------------------ depth cap ---

func TestSendEnforcesInboxDepthDropsOldest(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	const over = 5
	sent := make([]string, 0, maxInboxDepth+over)
	for i := 0; i < maxInboxDepth+over; i++ {
		sent = append(sent, mustSend(t, s, note(fmt.Sprintf("m%d", i))))
	}

	n, err := s.PendingCount(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if n != maxInboxDepth {
		t.Fatalf("PendingCount = %d, want %d", n, maxInboxDepth)
	}

	a, err := s.GetAgent(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if a.Dropped != over {
		t.Fatalf("agents.dropped = %d, want %d", a.Dropped, over)
	}

	wantDead := sent[:over]
	wantAlive := sent[over:]

	rows, err := s.w.QueryContext(ctx,
		`SELECT id FROM messages WHERE to_ws = ? AND to_name = ? AND dead = 1 ORDER BY id`,
		"ws", "bob")
	if err != nil {
		t.Fatalf("query dead: %v", err)
	}
	var gotDead []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan dead id: %v", err)
		}
		gotDead = append(gotDead, id)
	}
	_ = rows.Close()
	if !equalStrings(gotDead, wantDead) {
		t.Fatalf("dead ids = %v, want the %d oldest %v", gotDead, over, wantDead)
	}

	peek, err := s.Inbox(ctx, "ws", "bob", maxInboxDepth+over)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if !equalStrings(ids(peek), wantAlive) {
		t.Fatalf("Inbox after drops = %v, want %v", ids(peek), wantAlive)
	}

	drained, dropped, err := s.Drain(ctx, "ws", "bob", maxInboxDepth+over)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if dropped != over {
		t.Fatalf("Drain dropped = %d, want %d", dropped, over)
	}
	if !equalStrings(ids(drained), wantAlive) {
		t.Fatalf("Drain after drops = %v, want %v", ids(drained), wantAlive)
	}
}

// TestSendEnforcesInboxDepthPrefersDroppingNotes sends a handoff first, then
// floods the inbox past the cap with notes. Eviction must take the oldest
// notes, not the oldest message overall -- the handoff survives regardless
// of its age.
func TestSendEnforcesInboxDepthPrefersDroppingNotes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	handoff := note("the actual task")
	handoff.Kind = KindHandoff
	handoffID := mustSend(t, s, handoff)

	const over = 5
	notes := make([]string, 0, maxInboxDepth+over-1)
	for i := 0; i < maxInboxDepth+over-1; i++ {
		notes = append(notes, mustSend(t, s, note(fmt.Sprintf("m%d", i))))
	}

	a, err := s.GetAgent(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if a.Dropped != over {
		t.Fatalf("agents.dropped = %d, want %d", a.Dropped, over)
	}

	pending, err := s.Inbox(ctx, "ws", "bob", maxInboxDepth+1)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	wantAlive := append([]string{handoffID}, notes[over:]...)
	if !equalStrings(ids(pending), wantAlive) {
		t.Fatalf("pending after drops = %v, want %v", ids(pending), wantAlive)
	}
}

// TestSendOfANoteIntoAFullNonNoteInboxSurvives is the narrower eviction bug
// left by kind='note' DESC ordering: a note landing in an inbox already full
// of higher-priority kinds is the top drop candidate by that ordering alone,
// so naively it would be evicted in the very transaction that accepted it.
// The message just inserted is always excluded from the candidates, so it
// must survive; the oldest handoff is displaced in its place.
func TestSendOfANoteIntoAFullNonNoteInboxSurvives(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	handoffs := make([]string, 0, maxInboxDepth)
	for i := 0; i < maxInboxDepth; i++ {
		h := note(fmt.Sprintf("h%d", i))
		h.Kind = KindHandoff
		handoffs = append(handoffs, mustSend(t, s, h))
	}

	noteID := mustSend(t, s, note("just arrived"))

	pending, err := s.Inbox(ctx, "ws", "bob", maxInboxDepth+1)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	got := ids(pending)
	if !contains(got, noteID) {
		t.Fatalf("the note just sent was evicted in its own transaction: pending = %v", got)
	}
	if contains(got, handoffs[0]) {
		t.Fatalf("the oldest handoff should have been evicted in its place: pending = %v", got)
	}
	if len(got) != maxInboxDepth {
		t.Fatalf("pending = %d messages, want %d", len(got), maxInboxDepth)
	}
}

func TestSendAtExactlyMaxInboxDepthDropsNothing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	for i := 0; i < maxInboxDepth; i++ {
		mustSend(t, s, note(fmt.Sprintf("m%d", i)))
	}

	n, err := s.PendingCount(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if n != maxInboxDepth {
		t.Fatalf("PendingCount = %d, want %d", n, maxInboxDepth)
	}

	a, err := s.GetAgent(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if a.Dropped != 0 {
		t.Fatalf("agents.dropped = %d, want 0 at exactly the cap", a.Dropped)
	}
}

func TestInboxDepthDropCounterResetsOnlyOnDrain(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	const over = 3
	for i := 0; i < maxInboxDepth+over; i++ {
		mustSend(t, s, note(fmt.Sprintf("m%d", i)))
	}

	a, err := s.GetAgent(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if a.Dropped != over {
		t.Fatalf("agents.dropped = %d, want %d", a.Dropped, over)
	}

	// A peek must not touch the counter.
	if _, err := s.Inbox(ctx, "ws", "bob", maxInboxDepth); err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	a, err = s.GetAgent(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("GetAgent after peek: %v", err)
	}
	if a.Dropped != over {
		t.Fatalf("agents.dropped after peek = %d, want unchanged %d", a.Dropped, over)
	}

	// A real Drain reports it, then resets it.
	_, dropped, err := s.Drain(ctx, "ws", "bob", maxInboxDepth)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if dropped != over {
		t.Fatalf("Drain dropped = %d, want %d", dropped, over)
	}
	a, err = s.GetAgent(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("GetAgent after drain: %v", err)
	}
	if a.Dropped != 0 {
		t.Fatalf("agents.dropped after Drain = %d, want reset to 0", a.Dropped)
	}
}

// TestOversizedBodyAtDepthCapIsRejectedNotDropped checks the two caps stay
// independent: a body over MaxBodyBytes hits the existing size-rejection
// path, even against a recipient already sitting at the depth cap, and that
// rejection must never be counted as a drop -- a message that was never
// accepted was never "in" the inbox to be dropped from.
func TestOversizedBodyAtDepthCapIsRejectedNotDropped(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	pair(t, s)

	for i := 0; i < maxInboxDepth; i++ {
		mustSend(t, s, note(fmt.Sprintf("m%d", i)))
	}
	a, err := s.GetAgent(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if a.Dropped != 0 {
		t.Fatalf("agents.dropped before oversized send = %d, want 0", a.Dropped)
	}

	big := strings.Repeat("x", s.MaxBodyBytes+1)
	if _, err := s.Send(ctx, note(big)); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("oversized send at depth cap = %v, want ErrBodyTooLarge", err)
	}

	a, err = s.GetAgent(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("GetAgent after rejected send: %v", err)
	}
	if a.Dropped != 0 {
		t.Fatalf("agents.dropped after a rejected oversized send = %d, want unchanged 0", a.Dropped)
	}
	n, err := s.PendingCount(ctx, "ws", "bob")
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if n != maxInboxDepth {
		t.Fatalf("PendingCount after rejected send = %d, want unchanged %d", n, maxInboxDepth)
	}
}

func TestPendingByWorkspace(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	mustRegister(t, s, Agent{Workspace: "ws", Name: "alice", Cwd: "/a"})
	mustRegister(t, s, Agent{Workspace: "ws", Name: "bob", Cwd: "/b"})

	mustSend(t, s, Message{FromName: "alice", FromWS: "ws", ToName: "bob", ToWS: "ws", Kind: KindNote, Body: "one"})
	mustSend(t, s, Message{FromName: "alice", FromWS: "ws", ToName: "bob", ToWS: "ws", Kind: KindNote, Body: "two"})
	mustSend(t, s, Message{FromName: "bob", FromWS: "ws", ToName: "alice", ToWS: "ws", Kind: KindNote, Body: "three"})

	got, err := s.PendingByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("PendingByWorkspace: %v", err)
	}
	if got["bob"] != 2 {
		t.Errorf("pending for bob = %d, want 2", got["bob"])
	}
	if got["alice"] != 1 {
		t.Errorf("pending for alice = %d, want 1", got["alice"])
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
