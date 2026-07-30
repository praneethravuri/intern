package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/pkg/protocol"
)

const lsLong = `List the agents registered with the daemon, and what each one is doing.

Only this workspace is shown unless --all is given. NAME is the full address:
copy it straight into ` + "`tether send --to`" + `.

STATE is computed fresh on every call -- see ` + "`tether explain`" + ` for the
evidence behind one agent's state.`

type lsOptions struct {
	identityFlags
	all bool
}

func newLsCmd() *cobra.Command {
	var opts lsOptions

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List registered agents and what each is doing",
		Long:  lsLong,
		Example: "  tether ls\n" +
			"  tether ls --all\n" +
			"  tether ls --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLs(cmd, &opts)
		},
	}

	opts.addWorkspace(cmd)
	opts.addJSON(cmd)
	cmd.Flags().BoolVar(&opts.all, "all", false, "list agents in every workspace, not just this one")

	return quiet(cmd)
}

func runLs(cmd *cobra.Command, opts *lsOptions) error {
	var workspace string
	if !opts.all {
		ws, err := resolveWorkspace(opts.workspace)
		if err != nil {
			return err
		}
		workspace = ws
	} else {
		workspace = opts.workspace
	}

	var res protocol.WhoResult
	if err := call(protocol.MethodLs,
		protocol.WhoParams{Workspace: workspace}, &res); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.jsonOut {
		if res.Agents == nil {
			res.Agents = []protocol.AgentView{}
		}
		return printJSON(out, res)
	}

	if len(res.Agents) == 0 {
		return empty(out, "agents", "tether register --as <name>")
	}

	if err := aggregate(out, fleetSummaryParts(res.Agents)...); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if err := writeAgentTable(out, res.Agents); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	return next(out, fmt.Sprintf("tether send --to %s \"...\"", agentAddress(res.Agents[0])))
}

// fleetSummaryParts builds the aggregate line, e.g. ["3 agents", "1 idle"];
// only states that occur are listed.
func fleetSummaryParts(agents []protocol.AgentView) []string {
	counts := map[string]int{}
	for _, a := range agents {
		counts[a.State]++
	}

	parts := []string{plural(len(agents), "agent", "agents")}
	for _, state := range []string{"gone", "blocked", "working", "idle", "unknown"} {
		if n := counts[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, state))
		}
	}
	return parts
}

// writeAgentTable renders the agent list as an aligned table.
func writeAgentTable(out io.Writer, agents []protocol.AgentView) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tHARNESS\tSTATE\tPENDING\tLAST SEEN"); err != nil {
		return err
	}
	for _, a := range agents {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			agentAddress(a), dash(a.Harness), dash(a.State), pendingCell(a),
			relTime(a.LastSeen)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// pendingCell renders the PENDING column, including any dropped count.
func pendingCell(a protocol.AgentView) string {
	if a.Dropped > 0 {
		return fmt.Sprintf("%d (+%d dropped)", a.Pending, a.Dropped)
	}
	return fmt.Sprintf("%d", a.Pending)
}

// agentAddress prefers the daemon-reported address, falling back to composing
// one; sanitised for the terminal.
func agentAddress(a protocol.AgentView) string {
	addr := a.Address
	if addr == "" {
		addr = address(a.Name, a.Workspace)
	}
	return sanitizeTerminal(addr)
}
