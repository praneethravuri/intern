package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/intern/internal/hooks/claudecode"
	"github.com/praneethravuri/intern/internal/protocol"
)

// hookStopWaitTimeout is how long run-stop blocks for mail before letting
// the stop proceed, kept safely under Claude Code's documented default
// 600s command-hook timeout rather than assuming an unpublished higher
// ceiling actually works.
const hookStopWaitTimeout = 9 * time.Minute

// hookStopBudgetCap bounds consecutive Stop blocks for one session, well
// under Claude Code's own hard ceiling of 8 (CLAUDE_CODE_STOP_HOOK_BLOCK_CAP)
// -- stop_hook_active alone can't tell "our earlier block" from "some other
// hook's", so this is tracked independently.
const hookStopBudgetCap = 3

// mailBodyLimit caps how much rendered mail text one envelope carries, so a
// very full inbox can't blow past a hook's own output limits.
const mailBodyLimit = 8000

const hooksRunStopLong = `Claude Code's Stop-hook entry point. Not for direct use --
"intern hooks install" wires this into settings.json as an async hook.

Blocks on mail via the same daemon call as "intern wait", then either exits
2 with the mail on stderr (which Claude Code re-feeds as a system reminder
and keeps the session going) or exits 0 to let the stop proceed normally.
Fails open on any uncertainty: a daemon it cannot reach, an identity it
cannot resolve, or a per-session block budget already spent.`

const hooksRunSessionStartLong = `Claude Code's SessionStart-hook entry point. Not for direct use --
"intern hooks install" wires this into settings.json.

Delivers whatever mail is already waiting when a session starts, as
additionalContext, for mail that arrived while nobody was listening. Prints
nothing and exits 0 when there is none, or when the daemon can't be reached.`

type hooksRunStopOptions struct {
	identityFlags
	timeout time.Duration
}

func newHooksRunStopCmd() *cobra.Command {
	var opts hooksRunStopOptions

	cmd := &cobra.Command{
		Use:    "run-stop",
		Short:  "Claude Code Stop-hook entry point (internal)",
		Long:   hooksRunStopLong,
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHooksRunStop(cmd, &opts)
		},
	}
	opts.addIdentity(cmd)
	cmd.Flags().DurationVar(&opts.timeout, "timeout", hookStopWaitTimeout,
		"how long to block for mail before letting the stop proceed, as a Go duration")
	return quiet(cmd)
}

func newHooksRunSessionStartCmd() *cobra.Command {
	var opts identityFlags

	cmd := &cobra.Command{
		Use:    "run-session-start",
		Short:  "Claude Code SessionStart-hook entry point (internal)",
		Long:   hooksRunSessionStartLong,
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHooksRunSessionStart(cmd, &opts)
		},
	}
	opts.addIdentity(cmd)
	return quiet(cmd)
}

// runHooksRunStop implements the Stop-hook contract: read stdin fully before
// doing anything else (a slow reader can wedge Claude Code's write side of
// the pipe), single-flight under a session-scoped lock (Claude Code does not
// dedupe concurrent async hook firings itself), then block for mail and
// either hand it back via a blocking exit 2 or fail open with a silent exit
// 0. Every early return here is deliberately silent and successful: none of
// these conditions are the user's problem.
func runHooksRunStop(cmd *cobra.Command, opts *hooksRunStopOptions) error {
	drainStdin(cmd)

	name, workspace, err := resolveSelf(opts.name, opts.workspace)
	if err != nil {
		return nil // fail open: cannot resolve identity
	}
	_, session := currentSession()

	stateDir, err := hooksStateDir()
	if err != nil {
		return nil
	}
	key := "stop-" + session

	release, ok, err := claudecode.TryLock(filepath.Join(stateDir, "locks"), key)
	if err != nil || !ok {
		return nil // a concurrent firing already owns this Stop event, or the lock state is unreadable
	}
	defer release()

	budgetDir := filepath.Join(stateDir, "budget")
	if claudecode.LoadBudget(budgetDir, key) >= hookStopBudgetCap {
		claudecode.ResetBudget(budgetDir, key)
		return nil // budget spent: let this stop proceed even if mail is still pending
	}

	name, err = ensureRegistered(name, workspace)
	if err != nil {
		return nil
	}

	res, err := waitUpTo(name, workspace, session, opts.timeout)
	if err != nil {
		return nil // daemon unreachable or otherwise unwell: fail open
	}
	if res.Pending == 0 {
		claudecode.ResetBudget(budgetDir, key)
		return nil
	}

	body, err := drainMailBody(name, workspace, session)
	if err != nil || body == "" {
		return nil
	}

	// Best effort: a budget write failure must never suppress mail already
	// drained from the inbox. Worst case the loop-prevention degrades, which
	// is still bounded by Claude Code's own hard block cap.
	_ = claudecode.IncrementBudget(budgetDir, key)

	if _, err := fmt.Fprint(cmd.ErrOrStderr(), claudecode.FormatEnvelope(claudecode.KindMail, body)); err != nil {
		return err
	}
	return silentExit(exitHookBlock)
}

// runHooksRunSessionStart implements the SessionStart-hook contract: deliver
// whatever mail is already pending as additionalContext, so a fresh session
// sees it immediately instead of needing to poll for it. Every failure here
// is also silent and successful -- SessionStart has no blocking mechanism to
// fall back on, so there is nothing to report except the mail itself.
func runHooksRunSessionStart(cmd *cobra.Command, opts *identityFlags) error {
	drainStdin(cmd)

	name, workspace, err := resolveSelf(opts.name, opts.workspace)
	if err != nil {
		return nil
	}
	name, err = ensureRegistered(name, workspace)
	if err != nil {
		return nil
	}
	_, session := currentSession()

	body, err := drainMailBody(name, workspace, session)
	if err != nil || body == "" {
		return nil
	}

	var out sessionStartOutput
	out.HookSpecificOutput.HookEventName = "SessionStart"
	out.HookSpecificOutput.AdditionalContext = claudecode.FormatEnvelope(claudecode.KindMail, body)
	return printJSON(cmd.OutOrStdout(), out)
}

// exitHookBlock is the Claude Code Stop-hook contract's own exit code, not
// one of this CLI's normal exit codes: it means "block the stop, stderr is
// the reason", documented behaviour Claude Code itself interprets.
const exitHookBlock = 2

// drainStdin reads and discards cmd's stdin fully before doing anything
// else. Claude Code pipes a JSON payload to every hook command; this
// process never needs any of it (identity comes from the environment, the
// same way every other command resolves it), but a reader that doesn't
// drain a larger-than-pipe-buffer write can wedge Claude Code's side shut.
func drainStdin(cmd *cobra.Command) {
	_, _ = io.Copy(io.Discard, cmd.InOrStdin())
}

// drainMailBody drains this agent's inbox (the same default, destructive
// drain "intern inbox" performs) and renders it as plain text for an
// envelope body. "" with a nil error means there was nothing to deliver.
func drainMailBody(name, workspace, session string) (string, error) {
	var res protocol.InboxResult
	if err := call(protocol.MethodInbox, protocol.InboxParams{
		Name: name, Workspace: workspace, Session: session,
	}, &res); err != nil {
		return "", err
	}
	return renderMailBody(res.Messages, res.Pending), nil
}

// renderMailBody renders drained messages as plain text, sanitised the same
// way the human-readable "intern inbox" view is: a message body is another
// agent's data, never this process's own trusted text.
func renderMailBody(messages []protocol.MessageView, pendingAfter int) string {
	if len(messages) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s delivered:\n", plural(len(messages), "message", "messages"))
	for _, m := range messages {
		body := sanitizeTerminal(strings.TrimRight(m.Body, "\n"))
		fmt.Fprintf(&b, "- from %s (%s): %s\n", sanitizeTerminal(m.From), sanitizeTerminal(m.Kind), body)
	}
	if pendingAfter > 0 {
		fmt.Fprintf(&b, "%d more pending — run `intern inbox`.\n", pendingAfter)
	}

	out := strings.TrimRight(b.String(), "\n")
	if r := []rune(out); len(r) > mailBodyLimit {
		out = string(r[:mailBodyLimit]) + "\n... (truncated, run `intern inbox` for the rest)"
	}
	return out
}

// sessionStartOutput is Claude Code's documented SessionStart hook JSON
// shape for injecting additional context.
type sessionStartOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}
