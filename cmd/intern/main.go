// Command intern is the CLI for cross-harness agent messaging: one registry
// and inbox that any coding-agent harness can drive from the shell.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/intern/internal/protocol"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags)" ./cmd/intern
var version = "dev"

// Exit codes. These are part of the CLI's contract: scripts and agents branch
// on them, so they must never be reused for a different meaning.
const (
	// exitOK means the command did what it was asked to do.
	exitOK = 0
	// exitGeneral is any failure without a more specific code.
	exitGeneral = 1
	// exitNoDaemon means the daemon could not be reached.
	exitNoDaemon = 3
	// exitTimeout means `intern wait` returned with no mail.
	exitTimeout = 4
	// exitConflict means the request collided with existing state, most often
	// a name that is already registered in the workspace.
	exitConflict = 5
)

// exitError carries the process exit code a failure should produce.
//
// cobra's Execute reports a plain error, so commands return an *exitError and
// main unwraps it with errors.As to choose the code. A nil err means the
// command has already told the user what happened on stdout and main should
// exit quietly (this is how `wait` reports a timeout).
type exitError struct {
	code int
	err  error
}

// Error implements error.
func (e *exitError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

// Unwrap exposes the underlying cause so errors.As keeps working through it.
func (e *exitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// failf builds an *exitError with a formatted message.
func failf(code int, format string, a ...any) error {
	return &exitError{code: code, err: fmt.Errorf(format, a...)}
}

// fail wraps err so it exits with code.
func fail(code int, err error) error {
	return &exitError{code: code, err: err}
}

// silentExit exits with code without printing anything further; the command
// has already rendered its result.
func silentExit(code int) error {
	return &exitError{code: code}
}

// exitCodeFor maps an error returned by a command to a process exit code.
func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}

	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}

	// Safety net: a daemon-side conflict always exits 5 even if a command
	// forgot to translate it.
	var pe *protocol.Error
	if errors.As(err, &pe) && pe.Code == protocol.CodeConflict {
		return exitConflict
	}

	return exitGeneral
}

// errorMessage returns what main should print to stderr, or "" when the
// command has already reported the outcome itself.
func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	var ee *exitError
	if errors.As(err, &ee) && ee.err == nil {
		return ""
	}
	return err.Error()
}

func main() {
	err := newRootCmd().Execute()
	if err == nil {
		return
	}
	if msg := errorMessage(err); msg != "" {
		fmt.Fprintln(os.Stderr, "intern: "+msg)
	}
	os.Exit(exitCodeFor(err))
}

const rootLong = `intern is a local message bus for coding agents.

Agents register a name inside a workspace (the basename of the git root, or
$INTERN_WORKSPACE) and address each other as name@workspace. A bare name is
resolved against the current workspace.

Typical session:

  intern register --as frontend     # claim a name in this workspace
  intern ls                         # see who else is here
  intern send backend "..."         # send a message
  intern wait --timeout 60s         # block until mail arrives
  intern inbox                      # read it -- this also clears it
  intern inbox --peek               # look without clearing

Message bodies should be passed with --body-file (use "-" for stdin) whenever
they contain quotes, backticks, newlines or $ so the shell cannot mangle them.

Running intern with no arguments shows a quick glance at your own mail: how
many messages are pending, addressed as name@workspace. Run "intern start"
to run the daemon itself in the foreground -- this is what happens
automatically, detached, the first time any command needs one.

Exit codes:
  0  success
  1  general error
  3  no daemon running
  4  wait timed out
  5  conflict (for example, the name is already taken)`

// newRootCmd's bare form shows a live inbox glance (see runRoot); NoArgs
// makes a typo'd subcommand fail loudly instead of being silently ignored.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "intern",
		Short:         "Local message bus for coding agents",
		Long:          rootLong,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRoot(cmd)
		},
	}
	root.SetFlagErrorFunc(flagErrorFunc)

	root.AddCommand(
		newVersionCmd(),
		newStartCmd(),
		newRegisterCmd(),
		newSendCmd(),
		newInboxCmd(),
		newWaitCmd(),
		newLsCmd(),
		newClaimCmd(),
		newReleaseCmd(),
		newClaimsCmd(),
		newDoctorCmd(),
	)

	return root
}

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		},
	}
	return quiet(cmd)
}
