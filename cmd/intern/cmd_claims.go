package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/intern/internal/protocol"
)

const claimsLong = `List claims in this workspace: who holds what, and whether it's still live.

STATUS is computed fresh on every call: held (owner alive, TTL not elapsed),
expired (TTL elapsed), or gone (owner process no longer alive) -- the same
self-healing check ` + "`intern ls`" + ` uses for agent presence.`

type claimsOptions struct {
	identityFlags
	all bool
}

func newClaimsCmd() *cobra.Command {
	var opts claimsOptions

	cmd := &cobra.Command{
		Use:   "claims",
		Short: "List claims and who holds them",
		Long:  claimsLong,
		Example: "  intern claims\n" +
			"  intern claims --all\n" +
			"  intern claims --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runClaims(cmd, &opts)
		},
	}

	opts.addWorkspace(cmd)
	opts.addJSON(cmd)
	cmd.Flags().BoolVar(&opts.all, "all", false, "list claims in every workspace, not just this one")

	return quiet(cmd)
}

func runClaims(cmd *cobra.Command, opts *claimsOptions) error {
	ws, err := fleetWorkspace(opts.workspace, opts.all)
	if err != nil {
		return err
	}

	var res protocol.ClaimsResult
	if err := call(protocol.MethodClaims, protocol.ClaimsParams{Workspace: ws}, &res); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.jsonOut {
		if res.Claims == nil {
			res.Claims = []protocol.ClaimView{}
		}
		return printJSON(out, res)
	}

	if len(res.Claims) == 0 {
		return empty(out, "claims", "intern claim <key>")
	}
	return writeClaimsTable(out, res.Claims)
}

func writeClaimsTable(out io.Writer, claims []protocol.ClaimView) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "WORKSPACE\tKEY\tSTATUS\tHOLDER\tEXPIRES"); err != nil {
		return err
	}
	for _, c := range claims {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			sanitizeTerminal(c.Workspace), sanitizeTerminal(c.Key), dash(c.Status), dash(c.Holder),
			relExpiry(c.ExpiresAt)); err != nil {
			return err
		}
	}
	return tw.Flush()
}
