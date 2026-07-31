package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/internal/protocol"
)

// runBounded is run (helper_test.go), except it fails the test outright if
// the command has not returned within a generous bound, instead of trusting
// `go test`'s own overall timeout to eventually notice. That turns "the
// suite hung" into a specific, attributable failure: exactly which command
// is stuck, which matters here since the whole point of this file is to
// prove no command's success path blocks waiting on stdin.
//
// It does not reuse run (helper_test.go) directly: run calls t.Helper(),
// and that must never happen from a goroutine other than the one running the
// test, including the one this launches to race against the timeout.
func runBounded(t *testing.T, cmd *cobra.Command, stdin string, args ...string) runOut {
	t.Helper()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)

	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()

	select {
	case err := <-done:
		return runOut{stdout: out.String(), stderr: errOut.String(), err: err}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return within 5s -- it is blocking on something, most likely stdin", cmd.Name())
		return runOut{}
	}
}

// TestSuccessPathsNeverBlockAndKeepStderrClean is the stdout/stderr contract
// from a single vantage point, run once per command instead of scattered
// across each command's own test file: given a closed/empty stdin and a
// daemon that answers everything with success,
//
//  1. the command returns promptly -- nothing prompts, nothing waits on
//     input it was never told to read -- and
//  2. stderr is empty, EXCEPT for the handful of cases documented to warn
//     there on purpose (a dropped-mail count, an unrecognised harness).
//
// Every other command's success output -- the aggregate line, table rows,
// message bodies, "Next:" suggestions -- belongs on stdout alone.
func TestSuccessPathsNeverBlockAndKeepStderrClean(t *testing.T) {
	cases := []struct {
		name string
		// setup runs after setIdentity/clearHarnessEnv, for cases that need a
		// specific harness or a specific daemon response.
		newCmd     func() *cobra.Command
		args       []string
		handler    handlerFunc
		setup      func(t *testing.T)
		wantWarn   bool   // true for the documented stderr-warns-on-purpose cases
		wantInWarn string // substring the warning must contain, when wantWarn
	}{
		{
			name:    "version",
			newCmd:  newVersionCmd,
			handler: nil, // version never talks to the daemon at all
		},
		{
			name:    "register",
			newCmd:  newRegisterCmd,
			handler: okHandler(protocol.RegisterResult{Address: "frontend@storefront", Harness: "claude-code", Created: true}),
		},
		{
			name:    "send",
			newCmd:  newSendCmd,
			args:    []string{"backend", "hello"},
			handler: okHandler(protocol.SendResult{MessageID: "01K1QW8Z3M4T7V9XBCDEF2GH"}),
		},
		{
			name:    "inbox, nothing pending",
			newCmd:  newInboxCmd,
			handler: okHandler(protocol.InboxResult{}),
		},
		{
			name:   "inbox, mail was dropped",
			newCmd: newInboxCmd,
			handler: okHandler(protocol.InboxResult{
				Messages: []protocol.MessageView{{
					ID: "01K", From: "codex@storefront", Kind: kindNote, Body: "hi", CreatedAt: ago(time.Second),
				}},
				Cleared: 1,
				Dropped: 3,
			}),
			wantWarn:   true,
			wantInWarn: "dropped",
		},
		{
			name:    "wait, mail already pending",
			newCmd:  newWaitCmd,
			args:    []string{"--timeout", "2s"},
			handler: okHandler(protocol.WaitResult{Pending: 1}),
		},
		{
			name:    "ls",
			newCmd:  newLsCmd,
			handler: okHandler(agents()),
		},
		{
			name:    "explain",
			newCmd:  newExplainCmd,
			handler: okHandler(protocol.StatusResult{Agent: protocol.AgentView{Name: "frontend", Workspace: "storefront", Address: "frontend@storefront"}}),
		},
		{
			name:    "doctor, healthy",
			newCmd:  newDoctorCmd,
			handler: okHandler(protocol.WhoResult{}),
			setup:   func(t *testing.T) { t.Setenv("CLAUDECODE", "1") },
		},
		{
			name:       "doctor, unrecognised harness",
			newCmd:     newDoctorCmd,
			handler:    okHandler(protocol.WhoResult{}),
			wantWarn:   true,
			wantInWarn: "not recognised",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setIdentity(t, "frontend", "storefront")
			clearHarnessEnv(t)
			if tc.setup != nil {
				tc.setup(t)
			}
			if tc.handler != nil {
				newFakeDaemon(t, tc.handler)
			}

			r := runBounded(t, tc.newCmd(), "", tc.args...)
			if r.err != nil {
				t.Fatalf("%s failed on its success path: %v\nstdout:\n%s\nstderr:\n%s",
					tc.name, r.err, r.stdout, r.stderr)
			}

			if tc.wantWarn {
				requireContains(t, r.stderr, tc.wantInWarn, "stderr")
			} else if r.stderr != "" {
				t.Fatalf("%s: stderr is not documented to warn but got:\n%s", tc.name, r.stderr)
			}
		})
	}
}
