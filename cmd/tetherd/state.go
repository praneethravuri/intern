package main

import (
	"fmt"
	"time"

	"github.com/praneethravuri/tether/internal/proc"
	"github.com/praneethravuri/tether/internal/store"
)

// stateReport is the computed, never-persisted answer for "what is this agent doing?"
type stateReport struct {
	State  string        // gone | blocked | working | idle | unknown
	Source string        // pid | wait | observation | registration
	Age    time.Duration // age of the evidence, not of the agent itself
	Detail string        // human-readable evidence, for `tether explain`
}

// workingWindow is how recently an agent must have run a tether command to
// read as working rather than idle.
const workingWindow = 60 * time.Second

// computeState answers in strict priority: gone > blocked > working > idle > unknown.
// blocked comes from the live wait registry, not the observation log, since a
// parked wait can't outlive the process holding it.
func computeState(a store.Agent, last store.Observation, blocked bool, now time.Time) stateReport {
	if a.PID > 0 && !proc.AliveAt(a.PID, a.PIDStart) {
		return stateReport{State: "gone", Source: "pid", Age: now.Sub(a.LastSeen),
			Detail: fmt.Sprintf("pid %d is gone", a.PID)}
	}
	if blocked {
		return stateReport{State: "blocked", Source: "wait", Age: 0,
			Detail: "parked in tether wait"}
	}
	if !last.At.IsZero() {
		age := now.Sub(last.At)
		if age < workingWindow {
			return stateReport{State: "working", Source: "observation", Age: age,
				Detail: fmt.Sprintf("ran tether %s", last.Kind)}
		}
		return stateReport{State: "idle", Source: "observation", Age: age,
			Detail: fmt.Sprintf("last ran tether %s", last.Kind)}
	}
	return stateReport{State: "unknown", Source: "registration", Age: now.Sub(a.RegisteredAt),
		Detail: "registered; nothing observed since"}
}
