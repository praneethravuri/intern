package main

import "testing"

func TestMailbox_RegisterSendInboxWho(t *testing.T) {
	mb := NewMailbox()
	mb.Register("alice")
	mb.Register("bob")

	if err := mb.Send("alice", "bob", "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs, err := mb.Inbox("bob")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "hi" || msgs[0].From != "alice" {
		t.Fatalf("unexpected inbox: %+v", msgs)
	}

	// read-once: a second read is empty
	if msgs, err := mb.Inbox("bob"); err != nil || len(msgs) != 0 {
		t.Fatalf("expected empty inbox on second read, got %+v, err=%v", msgs, err)
	}

	who := mb.Who()
	if len(who) != 2 {
		t.Fatalf("expected 2 registered agents, got %v", who)
	}
}

func TestMailbox_BroadcastSkipsSender(t *testing.T) {
	mb := NewMailbox()
	mb.Register("alice")
	mb.Register("bob")
	mb.Register("carol")

	if err := mb.Send("alice", "*", "hello all"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if msgs, _ := mb.Inbox("alice"); len(msgs) != 0 {
		t.Fatalf("sender should not receive its own broadcast, got %+v", msgs)
	}
	if msgs, _ := mb.Inbox("bob"); len(msgs) != 1 {
		t.Fatalf("bob should have received the broadcast, got %+v", msgs)
	}
	if msgs, _ := mb.Inbox("carol"); len(msgs) != 1 {
		t.Fatalf("carol should have received the broadcast, got %+v", msgs)
	}
}

func TestMailbox_SendToUnregisteredFails(t *testing.T) {
	mb := NewMailbox()
	mb.Register("alice")

	if err := mb.Send("alice", "ghost", "hi"); err == nil {
		t.Fatal("expected error sending to an unregistered agent")
	}
}
