package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/pkg/protocol"
)

// defaultWaitTimeout is long enough to be useful in a shell loop and short
// enough that a forgotten `tether wait` eventually returns.
const defaultWaitTimeout = 60 * time.Second

// maxWaitTimeout keeps a typo like --timeout 60m0s0h from parking a process
// for a day.
const maxWaitTimeout = 24 * time.Hour

const waitLong = `Block until mail is waiting for this agent.

The timeout is a Go duration such as 30s, 5m or 1h30m. Exits 0 as soon as there
is something to read, and 4 if the timeout expires first, so a shell can branch
on it:

  if tether wait --timeout 2m; then tether inbox; fi

This is the polling-free way to idle: agents whose harness the daemon cannot
wake (the "universal" tier) should sit in wait rather than calling inbox in a loop.`

type waitOptions struct {
	identityFlags
	timeout time.Duration
}

func newWaitCmd() *cobra.Command {
	var opts waitOptions

	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Block until a message is waiting",
		Long:  waitLong,
		Example: "  tether wait\n" +
			"  tether wait --timeout 5m\n" +
			"  tether wait --as frontend --timeout 30s --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWait(cmd, &opts)
		},
	}

	opts.addIdentity(cmd)
	opts.addJSON(cmd)
	cmd.Flags().DurationVar(&opts.timeout, "timeout", defaultWaitTimeout,
		"how long to block, as a Go duration (30s, 5m, 1h30m)")

	return quiet(cmd)
}

func runWait(cmd *cobra.Command, opts *waitOptions) error {
	if opts.timeout <= 0 {
		return failf(exitGeneral, "--timeout must be greater than zero, got %s", opts.timeout)
	}
	if opts.timeout > maxWaitTimeout {
		return failf(exitGeneral, "--timeout must not exceed %s, got %s",
			maxWaitTimeout, opts.timeout)
	}

	name, workspace, err := resolveSelf(opts.name, opts.workspace)
	if err != nil {
		return err
	}

	name, err = ensureRegistered(name, workspace)
	if err != nil {
		return err
	}
	_, session := currentSession()

	res, err := waitUpTo(name, workspace, session, opts.timeout)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	addr := address(name, workspace)

	if opts.jsonOut {
		if err := printJSON(out, res); err != nil {
			return err
		}
	} else if res.TimedOut && res.Pending == 0 {
		if _, err := fmt.Fprintf(out, "no messages for %s after %s\n",
			addr, opts.timeout); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(out, "%s pending for %s — read with `tether inbox`\n",
			plural(res.Pending, "message", "messages"), addr); err != nil {
			return err
		}
	}

	if res.TimedOut && res.Pending == 0 {
		return silentExit(exitTimeout)
	}
	return nil
}

// waitUpTo blocks for up to total, transparently re-issuing wait past the
// daemon's internal per-request ceiling (WaitResult.Capped) so the CLI's
// contract is genuinely "blocks up to --timeout".
func waitUpTo(name, workspace, session string, total time.Duration) (protocol.WaitResult, error) {
	deadline := time.Now().Add(total)
	remaining := total

	for {
		params := protocol.WaitParams{
			Name:      name,
			Workspace: workspace,
			Session:   session,
			TimeoutMS: int(remaining.Milliseconds()),
		}

		var res protocol.WaitResult
		err := callTimeout(protocol.MethodWait, params, &res, waitCallTimeout(remaining))
		if err != nil {
			// A transport failure (the daemon was killed while this call was
			// parked, say) is worth reconnecting for -- callTimeout auto-starts
			// on the next dial if needed. A daemon-side error (bad request,
			// conflict) means something is genuinely wrong, so that still
			// returns immediately.
			if _, fromDaemon := daemonCode(err); fromDaemon {
				return protocol.WaitResult{}, err
			}
			remaining = time.Until(deadline)
			if remaining <= 0 {
				return protocol.WaitResult{}, err
			}
			continue
		}

		if !res.TimedOut || res.Pending > 0 {
			return res, nil
		}
		if !res.Capped {
			return res, nil // genuine timeout
		}

		remaining = time.Until(deadline)
		if remaining <= 0 {
			return protocol.WaitResult{Pending: 0, TimedOut: true}, nil // Capped is an internal detail by now
		}
	}
}
