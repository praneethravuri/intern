package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/pkg/protocol"
)

const explainLong = `Explain one agent's computed state and how much mail is waiting for it.

With no argument this reports on you (--as, or $TETHER_NAME). Pass an address to
inspect somebody else before sending them work.

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

	if len(args) == 1 {
		workspace, err = resolveWorkspace(opts.workspace)
		if err != nil {
			return err
		}
		name, workspace, err = resolveTarget(args[0], workspace)
	} else {
		// Only the self path registers implicitly; inspecting another agent is a read.
		name, workspace, err = resolveSelf(opts.name, opts.workspace)
		if err == nil {
			err = ensureRegistered(name, workspace)
		}
	}
	if err != nil {
		return err
	}

	var res protocol.StatusResult
	if err := call(protocol.MethodStatus,
		protocol.StatusParams{Name: name, Workspace: workspace}, &res); err != nil {
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
	return keyValues(out, pairs)
}
