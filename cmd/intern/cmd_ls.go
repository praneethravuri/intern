package main

import (
	"github.com/spf13/cobra"

	"github.com/praneethravuri/intern/internal/protocol"
)

const lsLong = `List the agents registered with the daemon, and what each one is doing.

Only this workspace is shown unless --all is given. NAME is the full address:
copy it straight into ` + "`intern send`" + `.

STATE is computed fresh on every call -- see ` + "`intern explain`" + ` for the
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
		Example: "  intern ls\n" +
			"  intern ls --all\n" +
			"  intern ls --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLs(cmd, &opts)
		},
	}

	opts.addWorkspace(cmd)
	cmd.Flags().BoolVar(&opts.all, "all", false, "list agents in every workspace, not just this one")

	return quiet(cmd)
}

func runLs(cmd *cobra.Command, opts *lsOptions) error {
	workspace, err := fleetWorkspace(opts.workspace, opts.all)
	if err != nil {
		return err
	}

	var res protocol.LsResult
	if err := call(protocol.MethodLs,
		protocol.LsParams{Workspace: workspace}, &res); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if res.Agents == nil {
		res.Agents = []protocol.AgentView{}
	}
	return printJSON(out, res)
}

// fleetWorkspace resolves the workspace for a fleet-view command (ls, top):
// every workspace when all is set, otherwise the usual flag/cwd resolution.
// Shared so the two commands can never disagree on what --all means.
func fleetWorkspace(workspaceFlag string, all bool) (string, error) {
	if all {
		return workspaceFlag, nil
	}
	return resolveWorkspace(workspaceFlag)
}

// fleetSummaryParts builds the aggregate line, e.g. ["3 agents", "1 quiet"];
// only states that occur are listed.

// writeAgentTable renders the agent list as an aligned table.

// pendingCell renders the PENDING column, including any dropped count.

// agentAddress prefers the daemon-reported address, falling back to composing
// one; sanitised for the terminal.
