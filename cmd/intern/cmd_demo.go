package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

const demoLong = `Watch two agents actually talk, in one terminal.

Spins up its own isolated daemon (its own socket, database, and $HOME --
never touching your real ~/.intern), registers two agents named frontend and
backend, and runs a real handoff between them over the real protocol: a
send, a wait, and an inbox read each way. Everything is torn down when it
finishes or when you press Ctrl-C.

This is a demonstration, not a REPL: it exits on its own after a few
seconds.`

// demoWorkspace is the scratch workspace name the two demo agents share.
const demoWorkspace = "demo"

// demoStepPause is how long to sit on each printed step before the next one,
// so the transcript reads like a conversation instead of a firehose. Total
// demo runtime is roughly len(demoSteps)*demoStepPause plus subprocess time.
const demoStepPause = 400 * time.Millisecond

func newDemoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Watch two real agents exchange a message, end to end",
		Long:  demoLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDemo(cmd)
		},
	}
	return quiet(cmd)
}

func runDemo(cmd *cobra.Command) error {
	exe, err := os.Executable()
	if err != nil {
		return failf(exitGeneral, "cannot find my own executable: %v", err)
	}

	dir, err := os.MkdirTemp("", "intern-demo")
	if err != nil {
		return failf(exitGeneral, "cannot create a scratch workspace: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	env := demoEnv(dir)
	sock := filepath.Join(dir, "sock")
	out := cmd.OutOrStdout()

	if _, err := fmt.Fprintf(out,
		"intern demo -- spinning up an isolated daemon at %s (never your real ~/.intern)\n\n", sock); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var daemonLog bytes.Buffer
	daemon := exec.Command(exe, "start")
	daemon.Env = env
	daemon.Stdout = &daemonLog
	daemon.Stderr = &daemonLog
	if err := daemon.Start(); err != nil {
		return failf(exitGeneral, "cannot start the demo daemon: %v", err)
	}
	defer func() {
		_ = daemon.Process.Signal(syscall.SIGTERM)
		_, _ = daemon.Process.Wait()
	}()

	if !dialableWithin(sock, 5*time.Second) {
		return failf(exitGeneral, "the demo daemon never became reachable; its log:\n%s", daemonLog.String())
	}

	if err := runDemoSteps(ctx, out, exe, env); err != nil {
		if errors.Is(err, context.Canceled) {
			_, werr := fmt.Fprintln(out, "demo interrupted -- tearing down the isolated daemon")
			return werr
		}
		return err
	}

	_, err = fmt.Fprintln(out, "demo complete -- tearing down the isolated daemon")
	return err
}

// demoEnv is the environment the demo's daemon and CLI subprocesses run
// under: everything isolated to dir, so nothing here can touch a real
// ~/.intern or collide with an already-running daemon.
func demoEnv(dir string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"INTERN_SOCK=" + filepath.Join(dir, "sock"),
		"INTERN_DB=" + filepath.Join(dir, "intern.db"),
	}
}

// demoStep is one line of the scripted handoff: which agent runs it, and the
// intern CLI arguments to run.
type demoStep struct {
	agent string
	args  []string
}

// demoSteps is the scripted exchange: frontend hands backend a real task,
// backend picks it up and answers, both read their mail. Pure and
// argument-free so its shape is unit-testable without spawning anything.
func demoSteps() []demoStep {
	const handoffBody = "the /orders endpoint now returns a cursor, not an offset -- your move"
	const answerBody = "got it, updating the client to page on the cursor now"

	return []demoStep{
		{"frontend", []string{"register", "--as", "frontend", "--workspace", demoWorkspace}},
		{"backend", []string{"register", "--as", "backend", "--workspace", demoWorkspace}},
		{"frontend", []string{"send", "--as", "frontend", "--workspace", demoWorkspace,
			"backend", handoffBody, "--kind", "handoff"}},
		{"backend", []string{"wait", "--as", "backend", "--workspace", demoWorkspace, "--timeout", "5s"}},
		{"backend", []string{"inbox", "--as", "backend", "--workspace", demoWorkspace}},
		{"backend", []string{"send", "--as", "backend", "--workspace", demoWorkspace,
			"frontend", answerBody, "--kind", "answer"}},
		{"frontend", []string{"inbox", "--as", "frontend", "--workspace", demoWorkspace}},
	}
}

// runDemoSteps runs the scripted handoff, printing each command and its real
// output before moving to the next, stopping early if ctx is cancelled.
func runDemoSteps(ctx context.Context, out io.Writer, exe string, env []string) error {
	for _, step := range demoSteps() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runDemoStep(out, exe, env, step); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(demoStepPause):
		}
	}
	return nil
}

// runDemoStep prints "[agent] $ intern ..." followed by the command's real,
// captured output, then runs it.
func runDemoStep(out io.Writer, exe string, env []string, step demoStep) error {
	if _, err := fmt.Fprintf(out, "[%s] $ intern %s\n", step.agent, displayArgs(step.args)); err != nil {
		return err
	}

	c := exec.Command(exe, step.args...)
	// Every step shares one parent (this process), so without a distinct
	// session id per agent, the daemon would derive the same synthetic
	// session for both and see the second register as a rename, not a
	// second agent -- see e2e_test.go's cliEnvForArgs for the same fix.
	c.Env = append(append([]string{}, env...), "INTERN_SESSION_ID=demo-"+step.agent)
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	runErr := c.Run()

	if _, err := fmt.Fprintln(out, indent(strings.TrimRight(buf.String(), "\n"), "  ")); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if runErr != nil {
		return failf(exitGeneral, "intern %s failed: %v", displayArgs(step.args), runErr)
	}
	return nil
}

// displayArgs renders args as a shell-readable command line for the
// transcript -- cosmetic only, quoting is not exec-safe and must never be
// used to actually run anything.
func displayArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t") {
			parts[i] = `"` + a + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
