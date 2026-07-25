package main

import (
	"testing"
	"time"
)

func TestRegistry_RegisterAndList(t *testing.T) {
	reg := NewRegistry()

	meta := AgentMeta{
		Harness:   "claude-code",
		PID:       1234,
		StartTime: time.Now(),
	}

	// register an agent
	err := reg.Register("api", meta)
	if err != nil {
		t.Fatalf("failed to register: %v", err)
	}

	// prevent duplicate name
	err = reg.Register("api", meta)
	if err == nil {
		t.Error("expected error when registering duplicate name, go nil")
	}

	agents := reg.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	if agents["api"].Harness != "claude-code" {
		t.Errorf("got harness %q, want claude-code", agents["api"].Harness)
	}

}
