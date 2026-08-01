package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/internal/protocol"
)

const explainLong = `Explain one agent's computed state and how much mail is waiting for it.

With no argument this reports on you (--as, or whatever this session already
registered). Pass an address to inspect somebody else before sending them work.

state/source/seen/detail are computed fresh on every call, never stored: seen
is how old the evidence behind state is, and detail says what that evidence
was.`

func newExplainCmd() *cobra.Command {
	var opts identityFlags

	cmd := &cobra.Command{
		Use:     "explain [name[@workspace]]",
		Aliases: []string{"status"},
		Short:   "Explain one agent's state and pending count",
		Long:    explainLong,
		Example: "  tether explain\n" +
			"  tether explain backend\n" +
			"  tether explain backend@storefront --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExplain(cmd, args, &opts)
		},
	}

	opts.addIdentity(cmd)
	opts.addJSON(cmd)

	return quiet(cmd)
}

func runExplain(cmd *cobra.Command, args []string, opts *identityFlags) error {
	var (
		name      string
		workspace string
		err       error
	)

	explainingSelf := len(args) == 0
	if !explainingSelf {
		workspace, err = resolveWorkspace(opts.workspace)
		if err != nil {
			return err
		}
		name, workspace, err = resolveTarget(args[0], workspace)
	} else {
		// Only the self path registers implicitly; inspecting another agent is a read.
		name, workspace, err = resolveSelf(opts.name, opts.workspace)
		if err == nil {
			name, err = ensureRegistered(name, workspace)
		}
	}
	if err != nil {
		return err
	}

	var res protocol.ExplainResult
	if err := call(protocol.MethodExplain,
		protocol.ExplainParams{Name: name, Workspace: workspace}, &res); err != nil {
		// "nobody was there" shares wait/send's timeout exit code, not the general one.
		if code, ok := daemonCode(err); ok && code == protocol.CodeNotFound {
			return fail(exitTimeout, err)
		}
		return err
	}

	out := cmd.OutOrStdout()
	if opts.jsonOut {
		return printJSON(out, res)
	}

	a := res.Agent
	addr := agentAddress(a)
	if addr == "@" {
		addr = address(name, workspace)
	}

	if _, err := fmt.Fprintln(out, addr); err != nil {
		return err
	}

	pairs := [][2]string{
		{"state", dash(a.State)},
		{"source", dash(a.StateSource)},
		{"seen", humanSince(time.Duration(a.StateAgeMS) * time.Millisecond)},
		{"detail", dash(a.StateDetail)},
		{"harness", dash(a.Harness)},
		{"pending", plural(a.Pending, "message", "messages")},
	}
	if a.Dropped > 0 {
		pairs = append(pairs, [2]string{"dropped", fmt.Sprintf("%d", a.Dropped)})
	}
	pairs = append(pairs,
		[2]string{"registered", relTime(a.RegisteredAt)},
		[2]string{"pid", fmt.Sprintf("%d", a.PID)},
		[2]string{"cwd", dash(a.Cwd)},
	)
	if err := keyValues(out, pairs); err != nil {
		return err
	}

	// A next-step hint only makes sense when you just inspected somebody
	// else -- explaining yourself has no "now send to yourself" follow-up.
	if explainingSelf {
		return nil
	}
	return next(out, fmt.Sprintf("tether send %s \"...\"", addr))
}
