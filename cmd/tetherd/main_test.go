package main

import (
	"testing"

	"github.com/praneethravuri/tether/pkg/protocol"
)

func TestHandleCommand_Register(t *testing.T) {
	reg := NewRegistry()

	res := handleCommand(protocol.Request{ID: "1", Method: "register"}, 1234, reg)
	if res.Error != nil {
		t.Fatalf("register failed: %v", res.Error)
	}

	agents := reg.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent registered, got %d", len(agents))
	}
}

func TestHandleCommand_List(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register("agent-1", AgentMeta{Harness: "claude-code", PID: 1}); err != nil {
		t.Fatalf("seed register: %v", err)
	}

	// The CLI sends the lowercase method name "list" (see cmd/tether/client.go);
	// handleCommand must match it or every ls falls through to the unknown-method case.
	res := handleCommand(protocol.Request{ID: "1", Method: "list"}, 1234, reg)
	if res.Error != nil {
		t.Fatalf("list failed: %v", res.Error)
	}

	agents, ok := res.Result.(map[string]AgentMeta)
	if !ok {
		t.Fatalf("result type = %T, want map[string]AgentMeta", res.Result)
	}
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
}

func TestHandleCommand_UnknownMethod(t *testing.T) {
	reg := NewRegistry()

	res := handleCommand(protocol.Request{ID: "1", Method: "bogus"}, 1234, reg)
	if res.Error == nil {
		t.Fatal("expected error for unknown method, got nil")
	}
	if res.Error.Code != 2 {
		t.Errorf("got error code %d, want 2", res.Error.Code)
	}
}

func TestHandleCommand_RegisterDuplicatePID(t *testing.T) {
	reg := NewRegistry()

	if res := handleCommand(protocol.Request{ID: "1", Method: "register"}, 42, reg); res.Error != nil {
		t.Fatalf("first register failed: %v", res.Error)
	}
	res := handleCommand(protocol.Request{ID: "2", Method: "register"}, 42, reg)
	if res.Error == nil {
		t.Fatal("expected error registering the same PID twice, got nil")
	}
}
