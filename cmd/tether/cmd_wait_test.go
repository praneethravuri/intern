package main

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/praneethravuri/tether/internal/protocol"
)

func TestWaitReturnsZeroWhenMailIsPending(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.WaitResult{Pending: 2}))

	r := mustRun(t, newWaitCmd(), "", "--timeout", "30s")

	params := decodeParams[protocol.WaitParams](t, d.registerThen(t, protocol.MethodWait))
	if params.Name != "frontend" || params.Workspace != "storefront" {
		t.Fatalf("wait as %s@%s, want frontend@storefront", params.Name, params.Workspace)
	}
	if params.TimeoutMS != 30_000 {
		t.Fatalf("timeout_ms = %d, want 30000 (a Go duration converted to ms)", params.TimeoutMS)
	}

	requireContains(t, r.stdout, "2 messages pending for frontend@storefront", "stdout")
	requireContains(t, r.stdout, "tether inbox", "stdout")
	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0", got)
	}
}

func TestWaitDefaultTimeoutIsSixtySeconds(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.WaitResult{Pending: 1}))

	mustRun(t, newWaitCmd(), "")

	params := decodeParams[protocol.WaitParams](t, d.registerThen(t, protocol.MethodWait))
	if params.TimeoutMS != 60_000 {
		t.Fatalf("timeout_ms = %d, want 60000", params.TimeoutMS)
	}
}

func TestWaitTimeoutExitsFour(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.WaitResult{TimedOut: true}))

	r := run(t, newWaitCmd(), "", "--timeout", "1s")
	if got := r.exitCode(); got != exitTimeout {
		t.Fatalf("exit code = %d, want %d", got, exitTimeout)
	}

	// A timeout is a normal outcome, so it is reported on stdout in one line
	// and main prints nothing extra.
	requireContains(t, r.stdout, "no messages for frontend@storefront after 1s", "stdout")
	if msg := errorMessage(r.err); msg != "" {
		t.Fatalf("a timeout would also print %q to stderr; it should not", msg)
	}
}

// A daemon that reports both a timeout and pending mail (a race between the
// deadline and a delivery) means there is mail: exit 0.
func TestWaitPrefersPendingMailOverTheTimeoutFlag(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.WaitResult{Pending: 1, TimedOut: true}))

	r := run(t, newWaitCmd(), "", "--timeout", "1s")
	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0 when mail is pending", got)
	}
}

func TestWaitJSON(t *testing.T) {
	t.Run("timed out", func(t *testing.T) {
		setIdentity(t, "frontend", "storefront")
		newFakeDaemon(t, okHandler(protocol.WaitResult{TimedOut: true}))

		r := run(t, newWaitCmd(), "", "--timeout", "1s", "--json")

		var got protocol.WaitResult
		unmarshalJSON(t, r.stdout, &got)
		if !got.TimedOut || got.Pending != 0 {
			t.Fatalf("result = %+v, want a timeout", got)
		}
		if code := r.exitCode(); code != exitTimeout {
			t.Fatalf("exit code = %d, want %d", code, exitTimeout)
		}
	})

	t.Run("mail pending", func(t *testing.T) {
		setIdentity(t, "frontend", "storefront")
		newFakeDaemon(t, okHandler(protocol.WaitResult{Pending: 3}))

		r := mustRun(t, newWaitCmd(), "", "--json")

		var got protocol.WaitResult
		unmarshalJSON(t, r.stdout, &got)
		if got.Pending != 3 {
			t.Fatalf("pending = %d, want 3", got.Pending)
		}
	})
}

func TestWaitRejectsBadTimeouts(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "zero", arg: "0s", want: "greater than zero"},
		{name: "negative", arg: "-5s", want: "greater than zero"},
		{name: "absurd", arg: "48h", want: "must not exceed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setIdentity(t, "frontend", "storefront")
			d := newFakeDaemon(t, okHandler(protocol.WaitResult{}))

			r := run(t, newWaitCmd(), "", "--timeout", tc.arg)
			if r.err == nil {
				t.Fatalf("wait accepted --timeout %s", tc.arg)
			}
			requireContains(t, r.err.Error(), tc.want, "error")
			if n := len(d.requests()); n != 0 {
				t.Fatalf("a bad request reached the daemon %d times", n)
			}
		})
	}
}

func TestWaitRejectsANonDuration(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.WaitResult{}))

	r := run(t, newWaitCmd(), "", "--timeout", "60")
	if r.err == nil {
		t.Fatal("wait accepted a bare number as a duration")
	}
}

// -- H2: the CLI must genuinely block up to --timeout, not the daemon's own
// internal per-request cap (server.go's maxWaitPerRequest) -------------------

// TestWaitTransparentlyLoopsPastACappedResponse proves waitUpTo re-issues
// "wait" when the daemon reports Capped=true instead of surfacing that
// internal cap to the caller. The fake daemon here stands in for a daemon
// whose maxWaitPerRequest is far shorter than what was actually asked for:
// it answers capped-timeout a few times, then finds mail. The overall
// `tether wait --timeout 30s` call must still succeed quickly, without
// waiting anywhere near 30s, and without the caller ever seeing a capped
// response.
func TestWaitTransparentlyLoopsPastACappedResponse(t *testing.T) {
	setIdentity(t, "frontend", "storefront")

	var calls int32
	d := newFakeDaemon(t, func(req protocol.Request) protocol.Response {
		if req.Method == protocol.MethodRegister {
			return protocol.OK(req.ID, protocol.RegisterResult{Created: true})
		}
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return protocol.OK(req.ID, protocol.WaitResult{TimedOut: true, Capped: true})
		}
		return protocol.OK(req.ID, protocol.WaitResult{Pending: 2})
	})

	start := time.Now()
	r := mustRun(t, newWaitCmd(), "", "--timeout", "30s", "--json")
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("wait took %v; a capped response should be retried immediately, not waited out", elapsed)
	}
	if n := d.countMethod(protocol.MethodWait); n < 3 {
		t.Fatalf("daemon received %d wait requests, want at least 3 (the loop must have re-issued wait)", n)
	}

	var got protocol.WaitResult
	unmarshalJSON(t, r.stdout, &got)
	if got.Pending != 2 || got.TimedOut {
		t.Fatalf("final result = %+v, want the mail that arrived on the 3rd try", got)
	}
	// The capped internal detail must never reach the caller once mail is
	// found: it is not part of what "wait" promises.
	if got.Capped {
		t.Fatalf("final result reports capped=true; that is an internal artifact, not part of the contract")
	}
}

// TestWaitEventuallyTimesOutIfEveryResponseIsCapped is the other half: if
// the daemon NEVER finds mail and every single response is capped, the CLI
// must still stop once the caller's own --timeout has elapsed and report an
// ordinary, uncapped timeout -- not loop forever. The fake daemon answers
// instantly regardless of the requested timeout_ms, so this proves
// termination without the test actually waiting out a long timeout.
func TestWaitEventuallyTimesOutIfEveryResponseIsCapped(t *testing.T) {
	setIdentity(t, "frontend", "storefront")

	d := newFakeDaemon(t, okHandler(protocol.WaitResult{TimedOut: true, Capped: true}))

	start := time.Now()
	r := run(t, newWaitCmd(), "", "--timeout", "300ms", "--json")
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("wait took %v to give up on a 300ms budget", elapsed)
	}
	if got := r.exitCode(); got != exitTimeout {
		t.Fatalf("exit code = %d, want %d", got, exitTimeout)
	}
	if n := d.countMethod(protocol.MethodWait); n < 2 {
		t.Fatalf("daemon received only %d wait request(s); the loop should have retried at least once in 300ms", n)
	}

	var got protocol.WaitResult
	unmarshalJSON(t, r.stdout, &got)
	if !got.TimedOut || got.Pending != 0 {
		t.Fatalf("final result = %+v, want a plain timeout", got)
	}
	if got.Capped {
		t.Fatalf("final result reports capped=true; the caller's own timeout genuinely elapsed")
	}
}

// TestWaitDoesNotLoopOnAGenuineUncappedTimeout is the control case: a
// TimedOut response with Capped=false is a real answer (the daemon actually
// waited what was asked), and must be reported immediately rather than
// retried.
func TestWaitDoesNotLoopOnAGenuineUncappedTimeout(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.WaitResult{TimedOut: true, Capped: false}))

	r := run(t, newWaitCmd(), "", "--timeout", "5s")
	if got := r.exitCode(); got != exitTimeout {
		t.Fatalf("exit code = %d, want %d", got, exitTimeout)
	}
	if n := d.countMethod(protocol.MethodWait); n != 1 {
		t.Fatalf("daemon received %d wait requests, want exactly 1: an uncapped timeout must not be retried", n)
	}
}

func TestWaitWithoutADaemonExitsThree(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	r := run(t, newWaitCmd(), "", "--timeout", "1s")
	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
}

// TestWaitBoundsAutoStartRetriesOnRepeatedTransportFailure is C2/6.7: a
// daemon that never becomes reachable must not be re-spawned on every retry
// within one wait call -- that is the unbounded-respawn-loop bug. spawnDaemon
// is allowed to run at most twice for the whole command (the implicit
// register ensureRegistered issues before wait, plus wait's own first
// attempt), never once per retry no matter how many retries the timeout
// budget allows.
func TestWaitBoundsAutoStartRetriesOnRepeatedTransportFailure(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	var spawns int32
	restoreSpawn(t, func(string) error {
		atomic.AddInt32(&spawns, 1)
		return errors.New("spawn always fails in this test")
	})

	start := time.Now()
	r := run(t, newWaitCmd(), "", "--timeout", "500ms")
	elapsed := time.Since(start)

	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("wait took %v to give up on a 500ms budget with no daemon ever reachable", elapsed)
	}
	if n := atomic.LoadInt32(&spawns); n > 2 {
		t.Fatalf("spawnDaemon was called %d times, want at most 2 -- not once per retry", n)
	}
}
