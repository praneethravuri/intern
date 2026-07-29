package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/pkg/protocol"
)

const registerLong = `Register this agent with tetherd so other agents can address it.

The name is claimed inside a workspace, which is the basename of the git root
of the current directory unless --workspace or $TETHER_WORKSPACE says
otherwise. Other agents then reach you at name@workspace.

Every other command registers implicitly before its real request, so running
this explicitly is optional -- it exists to let you pick a name and see it
confirmed up front, and to check a name is free before you start relying on
it.

Export the name so later commands do not need --as:

  export TETHER_NAME=frontend`

func newRegisterCmd() *cobra.Command {
	var opts identityFlags

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register this agent so others can reach it",
		Long:  registerLong,
		Example: "  tether register --as frontend\n" +
			"  tether register --as backend --workspace storefront\n" +
			"  tether register --as reviewer --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRegister(cmd, &opts)
		},
	}

	opts.addIdentity(cmd)
	opts.addJSON(cmd)

	return quiet(cmd)
}

func runRegister(cmd *cobra.Command, opts *identityFlags) error {
	name, workspace, err := resolveSelf(opts.name, opts.workspace)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return failf(exitGeneral, "cannot determine the current directory: %v", err)
	}

	harness, sessionID := currentSession()

	params := protocol.RegisterParams{
		Name:      name,
		Workspace: workspace,
		Harness:   harness,
		SessionID: sessionID,
		Cwd:       cwd,
		PID:       os.Getppid(), // the shell, not this short-lived CLI process
	}

	var res protocol.RegisterResult
	if err := call(protocol.MethodRegister, params, &res); err != nil {
		return registerError(name, workspace, err)
	}

	out := cmd.OutOrStdout()
	if opts.jsonOut {
		return printJSON(out, res)
	}

	addr := res.Address
	if addr == "" {
		addr = address(name, workspace)
	}
	addr = sanitizeTerminal(addr)

	verb := "registered"
	if !res.Created {
		verb = "refreshed registration for"
	}
	if _, err := fmt.Fprintf(out, "%s %s\n", verb, addr); err != nil {
		return err
	}
	return keyValues(out, [][2]string{
		{"harness", dash(res.Harness)},
	})
}

// registerError turns a 409 (name held by a live agent) into an actionable message.
func registerError(name, workspace string, err error) error {
	if code, ok := daemonCode(err); ok && code == protocol.CodeConflict {
		var pe *protocol.Error
		_ = errors.As(err, &pe)
		return fail(exitConflict, fmt.Errorf(
			"cannot register %s: %s\n"+
				"       the name is held by a live agent — pick a different --as name, "+
				"or run `tether who` to see who holds it",
			address(name, workspace), sanitizeTerminal(pe.Message)))
	}
	return err
}
