package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/intern/internal/protocol"
)

func TestRetainedCommandHelpStatesJSONIsDefault(t *testing.T) {
	commands := map[string]func() *cobra.Command{
		"intern": newRootCmd,
		"send":   newSendCmd,
		"inbox":  newInboxCmd,
		"wait":   newWaitCmd,
		"ls":     newLsCmd,
		"claims": newClaimsCmd,
		"doctor": newDoctorCmd,
	}

	for name, build := range commands {
		t.Run(name, func(t *testing.T) {
			r := mustRun(t, build(), "", "--help")
			requireContains(t, r.stdout, "Output is JSON by default.", "help")
		})
	}
}

func TestRetainedCommandsEmitJSONAndWireRequests(t *testing.T) {
	const workspace = "contract-workspace"

	tests := []struct {
		name        string
		build       func() *cobra.Command
		args        []string
		responses   map[string]any
		method      string
		registers   bool
		assertParam func(t *testing.T, request recorded)
		assertJSON  func(t *testing.T, output string)
		setup       func(t *testing.T)
	}{
		{
			name:      "register",
			build:     newRegisterCmd,
			args:      []string{"sender"},
			responses: map[string]any{protocol.MethodRegister: protocol.RegisterResult{Address: "sender@contract-workspace", Name: "sender", Created: true}},
			method:    protocol.MethodRegister,
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.RegisterParams](t, request)
				if params.Name != "sender" || params.Workspace != workspace || params.PID <= 0 {
					t.Fatalf("register params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result protocol.RegisterResult
				unmarshalJSON(t, output, &result)
				if result.Address != "sender@contract-workspace" || !result.Created {
					t.Fatalf("register result = %+v", result)
				}
			},
		},
		{
			name:  "send",
			build: newSendCmd,
			args:  []string{"recipient", "handoff body"},
			responses: map[string]any{
				protocol.MethodRegister: protocol.RegisterResult{Name: "sender"},
				protocol.MethodSend:     protocol.SendResult{MessageID: "message-1", RecipientState: "working"},
			},
			method:    protocol.MethodSend,
			registers: true,
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.SendParams](t, request)
				if params.FromName != "sender" || params.FromWorkspace != workspace ||
					params.ToName != "recipient" || params.ToWorkspace != workspace ||
					params.Body != "handoff body" || params.Kind != kindNote || params.FromSession == "" {
					t.Fatalf("send params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result protocol.SendResult
				unmarshalJSON(t, output, &result)
				if result.MessageID != "message-1" || result.RecipientState != "working" {
					t.Fatalf("send result = %+v", result)
				}
			},
		},
		{
			name:  "inbox",
			build: newInboxCmd,
			args:  []string{"--peek", "--limit", "7"},
			responses: map[string]any{
				protocol.MethodRegister: protocol.RegisterResult{Name: "sender"},
				protocol.MethodInbox: protocol.InboxResult{Messages: []protocol.MessageView{{
					ID: "message-1", From: "other@contract-workspace", To: "sender@contract-workspace", Kind: kindHandoff, Body: "handoff body",
				}}, Pending: 1},
			},
			method:    protocol.MethodInbox,
			registers: true,
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.InboxParams](t, request)
				if params.Name != "sender" || params.Workspace != workspace || !params.Peek || params.Replay || params.Limit != 7 || params.Session == "" {
					t.Fatalf("inbox params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result protocol.InboxResult
				unmarshalJSON(t, output, &result)
				if len(result.Messages) != 1 || result.Messages[0].Body != "handoff body" || result.Pending != 1 {
					t.Fatalf("inbox result = %+v", result)
				}
			},
		},
		{
			name:  "wait",
			build: newWaitCmd,
			args:  []string{"--timeout", "3s"},
			responses: map[string]any{
				protocol.MethodRegister: protocol.RegisterResult{Name: "sender"},
				protocol.MethodWait:     protocol.WaitResult{Pending: 1},
			},
			method:    protocol.MethodWait,
			registers: true,
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.WaitParams](t, request)
				if params.Name != "sender" || params.Workspace != workspace || params.TimeoutMS != 3_000 || params.Session == "" {
					t.Fatalf("wait params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result protocol.WaitResult
				unmarshalJSON(t, output, &result)
				if result.Pending != 1 || result.TimedOut {
					t.Fatalf("wait result = %+v", result)
				}
			},
		},
		{
			name:      "ls",
			build:     newLsCmd,
			responses: map[string]any{protocol.MethodLs: protocol.LsResult{Agents: []protocol.AgentView{{Name: "sender", Workspace: workspace, Address: "sender@contract-workspace"}}}},
			method:    protocol.MethodLs,
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.LsParams](t, request)
				if params.Workspace != workspace || params.Name != "" {
					t.Fatalf("ls params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result protocol.LsResult
				unmarshalJSON(t, output, &result)
				if len(result.Agents) != 1 || result.Agents[0].Address != "sender@contract-workspace" {
					t.Fatalf("ls result = %+v", result)
				}
			},
		},
		{
			name:      "claim",
			build:     newClaimCmd,
			args:      []string{"src/main.go", "--holder", "editing"},
			responses: map[string]any{protocol.MethodClaim: protocol.ClaimResult{LeaseID: "lease-1", Holder: "editing", ExpiresAt: "2030-01-01T00:00:00Z"}},
			method:    protocol.MethodClaim,
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.ClaimParams](t, request)
				if params.Workspace != workspace || params.Key != "src/main.go" || params.Holder != "editing" || params.OwnerPID <= 0 {
					t.Fatalf("claim params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result protocol.ClaimResult
				unmarshalJSON(t, output, &result)
				if result.LeaseID != "lease-1" || result.Holder != "editing" {
					t.Fatalf("claim result = %+v", result)
				}
			},
		},
		{
			name:      "release",
			build:     newReleaseCmd,
			args:      []string{"src/main.go", "--if-claim-id", "lease-1"},
			responses: map[string]any{protocol.MethodRelease: protocol.ReleaseResult{}},
			method:    protocol.MethodRelease,
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.ReleaseParams](t, request)
				if params.Workspace != workspace || params.Key != "src/main.go" || params.LeaseID != "lease-1" {
					t.Fatalf("release params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result protocol.ReleaseResult
				unmarshalJSON(t, output, &result)
			},
		},
		{
			name:      "claims",
			build:     newClaimsCmd,
			responses: map[string]any{protocol.MethodClaims: protocol.ClaimsResult{Claims: []protocol.ClaimView{{Workspace: workspace, Key: "src/main.go", Status: "held"}}}},
			method:    protocol.MethodClaims,
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.ClaimsParams](t, request)
				if params.Workspace != workspace {
					t.Fatalf("claims params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result protocol.ClaimsResult
				unmarshalJSON(t, output, &result)
				if len(result.Claims) != 1 || result.Claims[0].Key != "src/main.go" {
					t.Fatalf("claims result = %+v", result)
				}
			},
		},
		{
			name:      "doctor",
			build:     newDoctorCmd,
			responses: map[string]any{protocol.MethodLs: protocol.LsResult{Agents: []protocol.AgentView{{Name: "sender", Workspace: workspace}}}},
			method:    protocol.MethodLs,
			setup: func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
			},
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.LsParams](t, request)
				if params.Workspace != workspace {
					t.Fatalf("doctor ls params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result doctorReport
				unmarshalJSON(t, output, &result)
				if !result.DaemonRunning || len(result.Agents) != 1 || result.Workspace != workspace {
					t.Fatalf("doctor result = %+v", result)
				}
			},
		},
		{
			name:      "bare intern",
			build:     newRootCmd,
			responses: map[string]any{protocol.MethodLs: protocol.LsResult{Agents: []protocol.AgentView{{Name: "sender", Workspace: workspace, Address: "sender@contract-workspace"}}}},
			method:    protocol.MethodLs,
			assertParam: func(t *testing.T, request recorded) {
				params := decodeParams[protocol.LsParams](t, request)
				if params.Workspace != workspace || params.Name != "" {
					t.Fatalf("bare intern ls params = %+v", params)
				}
			},
			assertJSON: func(t *testing.T, output string) {
				var result protocol.LsResult
				unmarshalJSON(t, output, &result)
				if len(result.Agents) != 1 || result.Agents[0].Name != "sender" {
					t.Fatalf("bare intern result = %+v", result)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setIdentity(t, "sender", workspace)
			if tc.setup != nil {
				tc.setup(t)
			}
			d := newFakeDaemon(t, resultHandler(tc.responses))

			r := mustRun(t, tc.build(), "", tc.args...)
			var request recorded
			if tc.registers {
				request = d.registerThen(t, tc.method)
			} else {
				request = d.only(t, tc.method)
			}
			tc.assertParam(t, request)
			tc.assertJSON(t, r.stdout)
		})
	}
}

func resultHandler(results map[string]any) handlerFunc {
	return func(request protocol.Request) protocol.Response {
		result, ok := results[request.Method]
		if !ok {
			return protocol.Fail(request.ID, protocol.CodeBadRequest, "unexpected method "+request.Method)
		}
		return protocol.OK(request.ID, result)
	}
}

func TestRealProcessHandoff(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "intern")
	// #nosec G204 -- the test controls the go tool and its temporary output path.
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build intern binary: %v\n%s", err, output)
	}

	socket := filepath.Join(dir, "sock")
	database := filepath.Join(dir, "intern.db")
	workspace := "handoff-workspace"
	// #nosec G204 -- binary was built by this test in t.TempDir.
	daemon := exec.Command(binary, "start")
	daemon.Env = handoffEnv(socket, database, dir, "daemon-session")
	if err := daemon.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		if daemon.Process == nil {
			return
		}
		if err := daemon.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("interrupt daemon: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- daemon.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("wait for daemon: %v", err)
			}
		case <-time.After(5 * time.Second):
			if err := daemon.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("kill daemon: %v", err)
			}
			<-done
		}
	})
	waitForSocket(t, socket)

	run := func(session string, args ...string) string {
		t.Helper()
		// #nosec G204 -- binary was built by this test in t.TempDir; args are literals below.
		cmd := exec.Command(binary, args...)
		cmd.Env = handoffEnv(socket, database, dir, session)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("intern %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		return string(output)
	}

	var sender protocol.RegisterResult
	unmarshalJSON(t, run("sender-session", "register", "sender", "--workspace", workspace), &sender)
	if sender.Name != "sender" || sender.Address != "sender@handoff-workspace" {
		t.Fatalf("sender registration = %+v", sender)
	}

	var recipient protocol.RegisterResult
	unmarshalJSON(t, run("recipient-session", "register", "recipient", "--workspace", workspace), &recipient)
	if recipient.Name != "recipient" || recipient.Address != "recipient@handoff-workspace" {
		t.Fatalf("recipient registration = %+v", recipient)
	}

	var sent protocol.SendResult
	unmarshalJSON(t, run("sender-session", "send", "recipient", "--workspace", workspace,
		"--kind", "handoff", "handoff body"), &sent)
	if sent.MessageID == "" {
		t.Fatalf("send result has no message id: %+v", sent)
	}

	var waited protocol.WaitResult
	unmarshalJSON(t, run("recipient-session", "wait", "--as", "recipient", "--workspace", workspace,
		"--timeout", "3s"), &waited)
	if waited.Pending != 1 || waited.TimedOut {
		t.Fatalf("wait result = %+v, want one pending handoff", waited)
	}

	var inbox protocol.InboxResult
	unmarshalJSON(t, run("recipient-session", "inbox", "--as", "recipient", "--workspace", workspace), &inbox)
	if len(inbox.Messages) != 1 || inbox.Messages[0].ID != sent.MessageID ||
		inbox.Messages[0].Kind != kindHandoff || inbox.Messages[0].Body != "handoff body" || inbox.Cleared != 1 {
		t.Fatalf("inbox result = %+v", inbox)
	}
}

func handoffEnv(socket, database, home, session string) []string {
	removed := map[string]bool{
		"INTERN_SOCK":            true,
		"INTERN_DB":              true,
		"INTERN_WORKSPACE":       true,
		"INTERN_SESSION_ID":      true,
		"CLAUDE_CODE_SESSION_ID": true,
		"CLAUDECODE":             true,
		"GEMINI_SESSION_ID":      true,
		"COPILOT_HOME":           true,
		"COPILOT_SESSION_ID":     true,
		"OPENCODE_SESSION_ID":    true,
		"AMP_THREAD_ID":          true,
		"HOME":                   true,
	}
	env := make([]string, 0, len(os.Environ())+4)
	for _, pair := range os.Environ() {
		key, _, _ := strings.Cut(pair, "=")
		if !removed[key] && !strings.HasPrefix(key, "OPENCODE_") {
			env = append(env, pair)
		}
	}
	return append(env,
		"INTERN_SOCK="+socket,
		"INTERN_DB="+database,
		"INTERN_SESSION_ID="+session,
		"HOME="+home,
	)
}

func waitForSocket(t *testing.T, socket string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socket, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("daemon did not start listening on %s", socket)
}
