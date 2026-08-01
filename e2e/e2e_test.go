// Package e2e drives the compiled intern binary as a real subprocess over a
// real unix socket, in both its CLI and daemon roles. Skipped by -short since
// it forks processes and compiles a binary.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/praneethravuri/intern/internal/protocol"
)

// Exit codes mirror cmd/intern/main.go; redefined here since cmd/intern is
// package main, not importable.
const (
	exitNoDaemon = 3
	exitTimeout  = 4
	exitConflict = 5
)

// internBin is the path to the freshly compiled binary, set up once by
// TestMain before any test runs.
var internBin string

func TestMain(m *testing.M) {
	// A custom TestMain runs before testing flags (including -short) are
	// parsed, so the short-circuit below has to parse them itself.
	flag.Parse()
	os.Exit(runTestMain(m))
}

func runTestMain(m *testing.M) int {
	if testing.Short() {
		return m.Run() // every test skips itself; don't build binaries just to skip them
	}

	dir, err := os.MkdirTemp("", "intern-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: mkdtemp:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	internBin = filepath.Join(dir, "intern")

	if err := buildBinary(internBin, "../cmd/intern"); err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}

	return m.Run()
}

// buildBinary compiles pkgDir (a path relative to this package's directory,
// e.g. "../cmd/intern") to out, the same way an operator would.
func buildBinary(out, pkgDir string) error {
	cmd := exec.Command("go", "build", "-o", out, pkgDir)
	cmd.Env = os.Environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build -o %s %s: %w\n%s", out, pkgDir, err, stderr.String())
	}
	return nil
}

// -- process plumbing --------------------------------------------------------

// shortTempDir avoids t.TempDir(), whose nested subtest-name path can exceed
// the ~104-108 byte unix socket path limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "te2e")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// waitDialable polls path until something accepts a connection, rather than a
// fixed sleep or a racy stat of the socket file.
func waitDialable(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("socket %s never became dialable within %s: %v", path, timeout, lastErr)
}

// daemonEnv is the environment the daemon runs under: nothing inherited from
// the test process except PATH, so registration/harness detection in these
// tests never depends on what happens to be set in the environment running
// `go test`.
func daemonEnv(sock, db string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"INTERN_SOCK=" + sock,
		"INTERN_DB=" + db,
	}
}

// cliEnv is the environment the intern CLI runs under. See daemonEnv.
func cliEnv(sock string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"INTERN_SOCK=" + sock,
	}
}

// cliEnvForArgs is cliEnv with a per-agent INTERN_SESSION_ID derived from any
// --as value in args, so each simulated agent in this suite is a genuinely
// distinct session. See runIntern's doc comment for why this matters.
func cliEnvForArgs(sock string, args []string) []string {
	env := cliEnv(sock)
	for i, a := range args {
		if a == "--as" && i+1 < len(args) && args[i+1] != "" {
			return append(env, "INTERN_SESSION_ID=sess-"+args[i+1])
		}
	}
	return env
}

// testDaemon wraps internBin running with no arguments -- its daemon role.
type testDaemon struct {
	cmd     *exec.Cmd
	sock    string
	db      string
	log     *bytes.Buffer
	stopped bool
}

// startDaemon starts the daemon against sock/db and blocks until its socket
// is dialable. It does not register its own cleanup: TestEndToEnd owns
// exactly one cleanup that stops whichever daemon is current, which is what
// makes the restart-durability scenario (stop, start a second one on the same
// paths) safe to express without double-registering cleanups.
func startDaemon(t *testing.T, sock, db string) *testDaemon {
	t.Helper()

	cmd := exec.Command(internBin, "start")
	cmd.Env = daemonEnv(sock, db)
	logBuf := &bytes.Buffer{}
	cmd.Stdout = logBuf
	cmd.Stderr = logBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	d := &testDaemon{cmd: cmd, sock: sock, db: db, log: logBuf}
	waitDialable(t, sock, 10*time.Second)
	return d
}

// stop sends SIGTERM and waits for a clean exit, falling back to SIGKILL if
// the daemon does not honour it promptly. It is idempotent so it is safe to
// call both explicitly (restart scenario) and from the top-level cleanup.
func (d *testDaemon) stop(t *testing.T) {
	t.Helper()
	if d.stopped {
		return
	}
	d.stopped = true

	if d.cmd.Process == nil {
		return
	}

	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Logf("signal daemon: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = d.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Logf("daemon did not exit within 5s of SIGTERM; killing\nlog:\n%s", d.log.String())
		_ = d.cmd.Process.Kill()
		<-done
	}
}

// stopAutoStarted best-effort SIGTERMs the daemon that auto-start spawned for
// sock, using the pid recorded in its lock file -- intern has no shutdown
// RPC, and the daemon is otherwise untracked by this test process (it was
// spawned detached, by the intern subprocess that needed it, not by us).
func stopAutoStarted(t *testing.T, sock string) {
	t.Helper()
	raw, err := os.ReadFile(sock + ".lock")
	if err != nil {
		t.Logf("stopAutoStarted: read lock file: %v", err)
		return
	}
	var pid int
	if _, err := fmt.Sscanf(string(raw), "%d", &pid); err != nil {
		t.Logf("stopAutoStarted: parse pid from %q: %v", raw, err)
		return
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Logf("stopAutoStarted: kill %d: %v", pid, err)
	}
}

// -- CLI driving --------------------------------------------------------------

// cliResult is the outcome of one intern invocation.
type cliResult struct {
	stdout string
	stderr string
	code   int
}

// runIntern runs the real intern binary against sock and waits for it to
// finish. stdin is fed to the process verbatim (relevant for
// `send --body-file -`). A generous bound stops a hung subprocess from
// hanging the whole test run; every real command here finishes in
// milliseconds to low seconds.
//
// Every invocation in this file shares one parent process (this test
// binary), so without help every call would derive the same synthetic
// session id. That was harmless before the daemon could rename a session's
// existing registration, but registering a second --as name from what looks
// like the same session is now a rename, not an independent agent -- so the
// environment gets a INTERN_SESSION_ID derived from any --as value in args,
// giving each simulated agent its own session. Scenarios that need a
// specific session relationship (a real conflict, a dead-session reclaim)
// still build their own env explicitly via runInternEnv.
func runIntern(t *testing.T, sock, stdin string, args ...string) cliResult {
	t.Helper()
	return runInternEnv(t, cliEnvForArgs(sock, args), stdin, args...)
}

// runInternEnv is runIntern with an explicit environment, for scenarios that
// need to look like a different caller than the rest of the suite -- see
// "registering a taken name exits 5" below, which has to simulate a second,
// genuinely distinct session on purpose.
func runInternEnv(t *testing.T, env []string, stdin string, args ...string) cliResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, internBin, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run intern %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
	}
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// startBackground starts the intern binary without waiting for it, for the
// wait scenarios that need to overlap with another command.
func startBackground(t *testing.T, sock string, args ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(internBin, args...)
	cmd.Env = cliEnvForArgs(sock, args)
	cmd.Stdin = strings.NewReader("")
	out := &bytes.Buffer{}
	cmd.Stdout = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start intern %v: %v", args, err)
	}
	return cmd, out
}

func requireExit(t *testing.T, r cliResult, want int, what string) {
	t.Helper()
	if r.code != want {
		t.Fatalf("%s: exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			what, r.code, want, r.stdout, r.stderr)
	}
}

func requireContains(t *testing.T, got, want, what string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s does not contain %q:\n%s", what, want, got)
	}
}

func unmarshalJSON(t *testing.T, raw string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, raw)
	}
}

func findMessage(msgs []protocol.MessageView, body string) (protocol.MessageView, bool) {
	for _, m := range msgs {
		if m.Body == body {
			return m, true
		}
	}
	return protocol.MessageView{}, false
}

func containsBody(msgs []protocol.MessageView, body string) bool {
	_, ok := findMessage(msgs, body)
	return ok
}

func bodiesOf(msgs []protocol.MessageView) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Body
	}
	return out
}

// doctorJSON is the subset of `intern doctor --json`'s output this test
// cares about. The full shape (doctorReport) is unexported in cmd/intern, and
// cmd/intern is package main so it cannot be imported anyway -- this mirrors
// only the fields under test, which is exactly what `doctor` promises to
// keep stable.
type doctorJSON struct {
	DaemonRunning bool                 `json:"daemon_running"`
	Agents        []protocol.AgentView `json:"agents"`
}

// -- the end-to-end test ------------------------------------------------------

// TestEndToEnd drives the compiled intern binary, in both its CLI and daemon
// roles, through a full session: registration, messaging, the ack/replay
// contract, the wake path, conflicts, failure modes, doctor, and a daemon
// restart. See the package doc for why this exists alongside the unit tests.
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	if internBin == "" {
		t.Fatal("binary was not built by TestMain")
	}

	const ws = "acme"

	dir := shortTempDir(t)
	sock := filepath.Join(dir, "sock")
	db := filepath.Join(dir, "intern.db")

	// Exactly one cleanup owns stopping "whatever daemon is current", so the
	// restart scenario can swap the pointer without double-registering.
	var d *testDaemon
	t.Cleanup(func() {
		if d != nil {
			d.stop(t)
		}
	})
	d = startDaemon(t, sock, db)

	// 1. register --as frontend and --as backend both succeed.
	t.Run("register both agents", func(t *testing.T) {
		r := runIntern(t, sock, "", "register", "--as", "frontend", "--workspace", ws)
		requireExit(t, r, 0, "register frontend")
		requireContains(t, r.stdout, "registered frontend@"+ws, "register frontend stdout")

		r = runIntern(t, sock, "", "register", "--as", "backend", "--workspace", ws)
		requireExit(t, r, 0, "register backend")
		requireContains(t, r.stdout, "registered backend@"+ws, "register backend stdout")
	})

	// 2. ls lists both.
	t.Run("ls lists both", func(t *testing.T) {
		r := runIntern(t, sock, "", "ls", "--workspace", ws, "--json")
		requireExit(t, r, 0, "ls")

		var res protocol.LsResult
		unmarshalJSON(t, r.stdout, &res)

		names := map[string]bool{}
		for _, a := range res.Agents {
			names[a.Name] = true
		}
		if !names["frontend"] || !names["backend"] {
			t.Fatalf("who did not list both agents: %+v", res.Agents)
		}
	})

	// 3. backend sends to frontend; inbox --peek --json contains it with the
	// right from, without consuming it -- later subtests need it still
	// pending.
	const helloBody = "hello frontend, this is backend"
	var helloID string
	t.Run("send then inbox has it with correct from", func(t *testing.T) {
		// Positional <to>, not --to: this is the primary Phase 6 invocation
		// shape now, and running it through the real compiled binary is what
		// proves cobra's Args wiring for "send <to> [body]" actually works,
		// not just the in-process cmd_send_test.go coverage.
		r := runIntern(t, sock, "", "send", "--as", "backend", "--workspace", ws,
			"frontend", helloBody)
		requireExit(t, r, 0, "send backend->frontend")

		r = runIntern(t, sock, "", "inbox", "--as", "frontend", "--workspace", ws, "--peek", "--json")
		requireExit(t, r, 0, "inbox frontend")

		var res protocol.InboxResult
		unmarshalJSON(t, r.stdout, &res)

		m, ok := findMessage(res.Messages, helloBody)
		if !ok {
			t.Fatalf("inbox does not contain the sent message; got bodies %v", bodiesOf(res.Messages))
		}
		if want := "backend@" + ws; m.From != want {
			t.Fatalf("message From = %q, want %q", m.From, want)
		}
		helloID = m.ID
	})

	// 4. --peek twice still shows it, unconsumed; the default drains it;
	// draining again is empty; --replay shows the drained message.
	t.Run("inbox drains; peek does not; replay shows history after a drain", func(t *testing.T) {
		if helloID == "" {
			t.Fatal("previous subtest did not capture a message id")
		}

		for i := 0; i < 2; i++ {
			r := runIntern(t, sock, "", "inbox", "--as", "frontend", "--workspace", ws, "--peek", "--json")
			requireExit(t, r, 0, fmt.Sprintf("inbox --peek #%d", i+1))

			var res protocol.InboxResult
			unmarshalJSON(t, r.stdout, &res)
			if !containsBody(res.Messages, helloBody) {
				t.Fatalf("inbox --peek #%d lost the message: %v", i+1, bodiesOf(res.Messages))
			}
		}

		r := runIntern(t, sock, "", "inbox", "--as", "frontend", "--workspace", ws, "--json")
		requireExit(t, r, 0, "inbox (drain)")
		var drained protocol.InboxResult
		unmarshalJSON(t, r.stdout, &drained)
		if !containsBody(drained.Messages, helloBody) {
			t.Fatalf("draining inbox did not return the message: %v", bodiesOf(drained.Messages))
		}

		r = runIntern(t, sock, "", "inbox", "--as", "frontend", "--workspace", ws, "--json")
		requireExit(t, r, 0, "inbox after drain")
		var afterDrain protocol.InboxResult
		unmarshalJSON(t, r.stdout, &afterDrain)
		if len(afterDrain.Messages) != 0 {
			t.Fatalf("inbox after drain = %v, want empty", bodiesOf(afterDrain.Messages))
		}

		r = runIntern(t, sock, "", "inbox", "--as", "frontend", "--workspace", ws, "--replay", "--json")
		requireExit(t, r, 0, "inbox --replay")
		var replay protocol.InboxResult
		unmarshalJSON(t, r.stdout, &replay)
		if !containsBody(replay.Messages, helloBody) {
			t.Fatalf("inbox --replay lost the drained message: %v", bodiesOf(replay.Messages))
		}
	})

	// 4b. Broadcast: `send '*'` reaches every OTHER registered agent in the
	// workspace and never the sender. A third real agent is registered here
	// specifically so the broadcast has more than one recipient to land in.
	t.Run("send '*' broadcasts to every other agent, not the sender", func(t *testing.T) {
		r := runIntern(t, sock, "", "register", "--as", "reviewer", "--workspace", ws)
		requireExit(t, r, 0, "register reviewer")

		const broadcastBody = "deploying in 5, heads up"
		r = runIntern(t, sock, "", "send", "--as", "backend", "--workspace", ws,
			"*", broadcastBody)
		requireExit(t, r, 0, "broadcast send")
		requireContains(t, r.stdout, "2 agents", "broadcast send stdout")

		r = runIntern(t, sock, "", "inbox", "--as", "frontend", "--workspace", ws, "--peek", "--json")
		requireExit(t, r, 0, "inbox frontend after broadcast")
		var frontendInbox protocol.InboxResult
		unmarshalJSON(t, r.stdout, &frontendInbox)
		if !containsBody(frontendInbox.Messages, broadcastBody) {
			t.Fatalf("frontend did not receive the broadcast: %v", bodiesOf(frontendInbox.Messages))
		}

		r = runIntern(t, sock, "", "inbox", "--as", "reviewer", "--workspace", ws, "--peek", "--json")
		requireExit(t, r, 0, "inbox reviewer after broadcast")
		var reviewerInbox protocol.InboxResult
		unmarshalJSON(t, r.stdout, &reviewerInbox)
		if !containsBody(reviewerInbox.Messages, broadcastBody) {
			t.Fatalf("reviewer did not receive the broadcast: %v", bodiesOf(reviewerInbox.Messages))
		}

		r = runIntern(t, sock, "", "inbox", "--as", "backend", "--workspace", ws, "--peek", "--json")
		requireExit(t, r, 0, "inbox backend (the sender) after broadcast")
		var senderInbox protocol.InboxResult
		unmarshalJSON(t, r.stdout, &senderInbox)
		if containsBody(senderInbox.Messages, broadcastBody) {
			t.Fatalf("the sender received its own broadcast: %v", bodiesOf(senderInbox.Messages))
		}
	})

	// Headline scenario: intern wait wakes promptly on a real send from a
	// real second process. No sleep needed: handleWait subscribes before
	// checking pending count, so either ordering wakes correctly.
	t.Run("wait wakes on send (headline scenario)", func(t *testing.T) {
		r := runIntern(t, sock, "", "register", "--as", "sleepy", "--workspace", ws)
		requireExit(t, r, 0, "register sleepy")

		start := time.Now()
		waitCmd, waitOut := startBackground(t, sock,
			"wait", "--as", "sleepy", "--workspace", ws, "--timeout", "30s", "--json")

		done := make(chan error, 1)
		go func() { done <- waitCmd.Wait() }()

		r = runIntern(t, sock, "", "send", "--as", "backend", "--workspace", ws,
			"sleepy", "wake up")
		requireExit(t, r, 0, "send to sleepy")

		select {
		case err := <-done:
			elapsed := time.Since(start)
			if elapsed >= 10*time.Second {
				t.Fatalf("wait took %s, want well under its 30s timeout", elapsed)
			}
			code := 0
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					code = ee.ExitCode()
				} else {
					t.Fatalf("wait process error: %v", err)
				}
			}
			if code != 0 {
				t.Fatalf("wait exit code = %d, want 0\noutput:\n%s", code, waitOut.String())
			}

			var wr protocol.WaitResult
			unmarshalJSON(t, waitOut.String(), &wr)
			if wr.Pending < 1 {
				t.Fatalf("wait result pending = %d, want >= 1", wr.Pending)
			}
			if wr.TimedOut {
				t.Fatalf("wait result timed_out = true, want false")
			}
			t.Logf("wait woke after %s (well under the 30s timeout)", elapsed)
		case <-time.After(15 * time.Second):
			_ = waitCmd.Process.Kill()
			t.Fatal("wait process did not return within 15s; it did not wake on the send")
		}
	})

	// 6. wait with a short timeout and no mail exits 4.
	t.Run("wait times out with no mail", func(t *testing.T) {
		r := runIntern(t, sock, "", "register", "--as", "lonely", "--workspace", ws)
		requireExit(t, r, 0, "register lonely")

		start := time.Now()
		r = runIntern(t, sock, "", "wait", "--as", "lonely", "--workspace", ws, "--timeout", "1s")
		elapsed := time.Since(start)

		requireExit(t, r, exitTimeout, "wait with no mail")
		if elapsed > 10*time.Second {
			t.Fatalf("wait --timeout 1s took %s, want it to return promptly", elapsed)
		}
		requireContains(t, r.stdout, "no messages", "wait timeout stdout")
	})

	// Every invocation in this file shares one parent process, so without
	// INTERN_SESSION_ID they'd all derive the same synthetic session id
	// (correctly idempotent). A real conflict needs an explicit distinct session.
	t.Run("registering a taken name exits 5", func(t *testing.T) {
		env := append(append([]string{}, cliEnv(sock)...), "INTERN_SESSION_ID=intruder-session")
		r := runInternEnv(t, env, "", "register", "--as", "frontend", "--workspace", ws)
		requireExit(t, r, exitConflict, "re-register frontend")
		requireContains(t, r.stderr, "held by a live agent", "conflict stderr")
	})

	t.Run("double register with no recognised harness is idempotent", func(t *testing.T) {
		r := runIntern(t, sock, "", "register", "--as", "idempotent", "--workspace", ws)
		requireExit(t, r, 0, "first register")
		requireContains(t, r.stdout, "registered idempotent@"+ws, "first register stdout")

		r = runIntern(t, sock, "", "register", "--as", "idempotent", "--workspace", ws)
		requireExit(t, r, 0, "second register")
		requireContains(t, r.stdout, "refreshed registration for idempotent@"+ws, "second register stdout")
	})

	// A name held by a session whose pid has actually died is reclaimable
	// immediately, not after StaleAfter. handleRegister rejects a dead pid
	// outright, so the incumbent has to be seeded directly over the wire.
	t.Run("a name held by a now-dead session is reclaimable immediately", func(t *testing.T) {
		holder := exec.Command("sleep", "300")
		if err := holder.Start(); err != nil {
			t.Fatalf("start holder process: %v", err)
		}
		holderPID := holder.Process.Pid
		t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })

		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		enc := json.NewEncoder(conn)
		dec := json.NewDecoder(conn)

		reg := protocol.RegisterParams{
			Name: "doomed", Workspace: ws, Harness: "test",
			SessionID: "doomed-session", Cwd: dir, PID: holderPID,
		}
		raw, err := json.Marshal(reg)
		if err != nil {
			t.Fatalf("marshal register params: %v", err)
		}
		if err := enc.Encode(protocol.Request{ID: "seed", V: protocol.Version, Method: protocol.MethodRegister, Params: raw}); err != nil {
			t.Fatalf("encode seed register: %v", err)
		}
		var resp protocol.Response
		if err := dec.Decode(&resp); err != nil || resp.Error != nil {
			t.Fatalf("seed register failed: decode err=%v resp=%+v", err, resp)
		}
		_ = conn.Close()

		if err := holder.Process.Kill(); err != nil {
			t.Fatalf("kill holder: %v", err)
		}
		_ = holder.Wait()

		env := append(append([]string{}, cliEnv(sock)...), "INTERN_SESSION_ID=rescuer-session")
		r := runInternEnv(t, env, "", "register", "--as", "doomed", "--workspace", ws)
		requireExit(t, r, 0, "reclaim of a dead session's name")
	})

	// send --as X from a session that doesn't hold X must fail, not forge mail.
	t.Run("send --as an unowned name from an intruder session exits 5", func(t *testing.T) {
		env := append(append([]string{}, cliEnv(sock)...), "INTERN_SESSION_ID=send-intruder-session")
		r := runInternEnv(t, env, "", "send", "--as", "frontend", "--workspace", ws,
			"backend", "forged message")
		requireExit(t, r, exitConflict, "send as an unowned name")
	})

	// 7e. unregister and heartbeat never existed as CLI subcommands, and
	// who/status were renamed to ls/explain -- either way, the retired wire
	// spellings now 400 like any other unknown method.
	t.Run("unregister, heartbeat, who and status are gone from the wire", func(t *testing.T) {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		enc := json.NewEncoder(conn)
		dec := json.NewDecoder(conn)

		for _, method := range []string{"unregister", "heartbeat", "who", "status"} {
			if err := enc.Encode(protocol.Request{ID: method, V: protocol.Version, Method: method}); err != nil {
				t.Fatalf("encode %s: %v", method, err)
			}
			var resp protocol.Response
			if err := dec.Decode(&resp); err != nil {
				t.Fatalf("decode %s: %v", method, err)
			}
			if resp.Error == nil || resp.Error.Code != protocol.CodeBadRequest {
				t.Fatalf("%s response = %+v, want a 400", method, resp)
			}
		}
	})

	// 8. A shell-hostile body arrives byte for byte via --body-file -.
	t.Run("body with quotes, backticks, $VAR and newlines survives byte for byte", func(t *testing.T) {
		body := "quote:\" apostrophe:' backtick:`inline`\n" +
			"dollar:$HOME and $(whoami) and ${PATH}\n" +
			"  leading and trailing spaces  \n" +
			"\ttab\tindented\n" +
			"final line, no trailing newline"

		r := runIntern(t, sock, body, "send", "--as", "backend", "--workspace", ws,
			"frontend", "--body-file", "-")
		requireExit(t, r, 0, "send with --body-file -")

		r = runIntern(t, sock, "", "inbox", "--as", "frontend", "--workspace", ws, "--json")
		requireExit(t, r, 0, "inbox after hostile body send")

		var res protocol.InboxResult
		unmarshalJSON(t, r.stdout, &res)

		m, ok := findMessage(res.Messages, body)
		if !ok {
			t.Fatalf("hostile body did not arrive intact; got bodies: %v", bodiesOf(res.Messages))
		}
		if m.Body != body {
			t.Fatalf("body mismatch:\n got  %q\nwant %q", m.Body, body)
		}
	})

	// 9. No daemon (INTERN_SOCK pointing at a nonexistent path) auto-starts
	// one instead of failing -- the Phase 3 headline behavior. Isolated
	// socket/db/home so the daemon it spawns can't leak into other
	// scenarios, and cleaned up via the pid its own lock file records
	// (intern has no shutdown RPC).
	t.Run("no daemon auto-starts one", func(t *testing.T) {
		autoDir := shortTempDir(t)
		autoHome := filepath.Join(autoDir, "home")
		if err := os.MkdirAll(autoHome, 0o700); err != nil {
			t.Fatalf("mkdir home: %v", err)
		}
		autoSock := filepath.Join(autoDir, "sock")
		env := []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + autoHome,
			"INTERN_SOCK=" + autoSock,
			"INTERN_DB=" + filepath.Join(autoDir, "intern.db"),
		}

		r := runInternEnv(t, env, "", "ls", "--workspace", ws)
		requireExit(t, r, 0, "ls with no daemon (should auto-start one)")
		requireContains(t, r.stdout, "0 agents", "auto-started daemon's ls output")

		waitDialable(t, autoSock, 3*time.Second)
		t.Cleanup(func() { stopAutoStarted(t, autoSock) })
	})

	// 10. doctor never auto-starts: it diagnoses rather than fixes, so
	// against the same kind of dead socket it still exits 3 and creates
	// nothing.
	t.Run("doctor never auto-starts", func(t *testing.T) {
		deadSock := filepath.Join(dir, "no-such-socket-for-doctor")
		r := runIntern(t, deadSock, "", "doctor", "--workspace", ws)
		requireExit(t, r, exitNoDaemon, "doctor with no daemon")
		requireContains(t, r.stdout, "no daemon running", "doctor stdout")
		if _, err := os.Stat(deadSock); err == nil {
			t.Fatal("doctor must not create a socket by auto-starting a daemon")
		}
	})

	// 11. doctor reports the daemon reachable and lists agents.
	t.Run("doctor reports daemon reachable and lists agents", func(t *testing.T) {
		r := runIntern(t, sock, "", "doctor", "--workspace", ws, "--json")
		requireExit(t, r, 0, "doctor")

		var rep doctorJSON
		unmarshalJSON(t, r.stdout, &rep)
		if !rep.DaemonRunning {
			t.Fatalf("doctor reports the daemon is not running: %s", r.stdout)
		}

		names := map[string]bool{}
		for _, a := range rep.Agents {
			names[a.Name] = true
		}
		if !names["frontend"] || !names["backend"] {
			t.Fatalf("doctor did not list frontend and backend: %+v", rep.Agents)
		}
	})

	// 12. Restart durability: a message survives a SIGTERM + restart against
	// the same INTERN_DB. This is the last scenario to use the daemon.
	t.Run("message survives a daemon restart", func(t *testing.T) {
		r := runIntern(t, sock, "", "register", "--as", "durable", "--workspace", ws)
		requireExit(t, r, 0, "register durable")

		const survivorBody = "this message must survive a restart"
		r = runIntern(t, sock, "", "send", "--as", "backend", "--workspace", ws,
			"durable", survivorBody)
		requireExit(t, r, 0, "send to durable")

		// --peek: this must not consume the message, or "after restart"
		// below would find nothing and prove nothing about durability.
		r = runIntern(t, sock, "", "inbox", "--as", "durable", "--workspace", ws, "--peek", "--json")
		requireExit(t, r, 0, "inbox before restart")
		var before protocol.InboxResult
		unmarshalJSON(t, r.stdout, &before)
		if !containsBody(before.Messages, survivorBody) {
			t.Fatalf("message not visible before restart: %v", bodiesOf(before.Messages))
		}

		// SIGTERM the daemon and restart it against the same INTERN_DB.
		d.stop(t)
		d = startDaemon(t, sock, db)

		r = runIntern(t, sock, "", "inbox", "--as", "durable", "--workspace", ws, "--json")
		requireExit(t, r, 0, "inbox after restart")
		var after protocol.InboxResult
		unmarshalJSON(t, r.stdout, &after)
		if !containsBody(after.Messages, survivorBody) {
			t.Fatalf("message did not survive the restart: %v", bodiesOf(after.Messages))
		}
	})

	// 13. intern version prints something. Does not touch the daemon.
	t.Run("version prints something", func(t *testing.T) {
		r := runIntern(t, sock, "", "version")
		requireExit(t, r, 0, "version")
		if strings.TrimSpace(r.stdout) == "" {
			t.Fatal("intern version produced no output")
		}
	})
}

// TestStartRunsTheDaemon is the one-binary headline scenario: no separate
// daemon binary exists, so `intern start` must itself become the daemon --
// print the banner, accept connections, and shut down cleanly on SIGINT.
func TestStartRunsTheDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := shortTempDir(t)
	sock := filepath.Join(dir, "sock")
	db := filepath.Join(dir, "intern.db")

	cmd := exec.Command(internBin, "start")
	cmd.Env = daemonEnv(sock, db)
	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Start(); err != nil {
		t.Fatalf("start intern start: %v", err)
	}
	waitDialable(t, sock, 10*time.Second)

	r := runIntern(t, sock, "", "ls", "--json")
	requireExit(t, r, 0, "ls against the intern start daemon")

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal SIGINT: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("intern start did not exit cleanly on SIGINT: %v\noutput:\n%s", err, out.String())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("intern start did not exit within 5s of SIGINT")
	}

	// out is only safe to read now that the process (and its stdout-copying
	// goroutine) has exited.
	requireContains(t, out.String(), "running the daemon in the foreground", "banner")
	requireContains(t, out.String(), sock, "banner")
}

// TestDemo runs `intern demo` as a real subprocess: it must spin up its own
// daemon, actually exchange a message between two agents it registers
// itself, print the exchange, and exit 0 well inside its own bounded
// runtime. This is `demo`'s safety-in-CI bar (see cmd/intern/cmd_demo.go),
// distinct from the unit-level coverage of its pure helpers in
// cmd/intern/cmd_demo_test.go.
func TestDemo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	if internBin == "" {
		t.Fatal("binary was not built by TestMain")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, internBin, "demo")
	cmd.Env = os.Environ()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		t.Fatalf("intern demo failed: %v\noutput:\n%s", err, out.String())
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("intern demo took %s, want well under 20s", elapsed)
	}

	transcript := out.String()
	requireContains(t, transcript, "isolated daemon", "demo transcript")
	requireContains(t, transcript, "[frontend]", "demo transcript")
	requireContains(t, transcript, "[backend]", "demo transcript")
	requireContains(t, transcript, "cursor, not an offset", "demo transcript")
	requireContains(t, transcript, "updating the client", "demo transcript")
	requireContains(t, transcript, "demo complete", "demo transcript")
}
