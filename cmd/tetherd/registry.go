// Package main implements the tetherd background daemon and state registry.
package main

import (
	"errors"
	"sync"
	"time"
)

// AgentMeta holds the OS-level state of a connected harness
type AgentMeta struct {
	Harness   string
	PID       int
	StartTime time.Time
}

// Registry is a thread-safe store of active agents
type Registry struct {
	mu     sync.RWMutex
	agents map[string]AgentMeta
}

// NewRegistry initializes the map
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]AgentMeta),
	}
}

// Registry adds a new agent. It fails if the name is already taken
func (r *Registry) Register(name string, meta AgentMeta) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Memory exhaustion protection
	if len(r.agents) > 100 {
		return errors.New("registry full: maximum agent limit reached")
	}

	if _, exists := r.agents[name]; exists {
		return errors.New("agent name already in use")
	}

	r.agents[name] = meta

	return nil
}

// Lists returns a snapshot of all registered agents
func (r *Registry) List() map[string]AgentMeta {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Return a copy so the caller can't accidentally mutate the internal map
	snapshot := make(map[string]AgentMeta, len(r.agents))
	for k, v := range r.agents {
		snapshot[k] = v
	}

	return snapshot
}
