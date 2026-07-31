package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/internal/protocol"
)

const registerLong = `Register this agent with the daemon so other agents can address it.

The name is claimed inside a workspace, which is the basename of the git root
of the current directory unless --workspace or $TETHER_WORKSPACE says
otherwise. Other agents then reach you at name@workspace.

Every other command registers implicitly before its real request, so running
this explicitly is optional -- it exists to let you pick a name and see it
confirmed up front. With no name at all, the daemon mints one and reports it.

Running this again with a different name renames you in place, and moves
your pending mail along with it -- your old name stops working immediately.`

type registerOptions struct {
	identityFlags
	doing string
}

func newRegisterCmd() *cobra.Command {
	var opts registerOptions

	cmd := &cobra.Command{
		Use:   "register [name]",
		Short: "Register this agent so others can reach it",
		Long:  registerLong,
		Example: "  tether register frontend\n" +
			"  tether register backend --workspace storefront\n" +
			"  tether register --doing \"compiling tests, ~5min\"\n" +
			"  tether register --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegister(cmd, args, &opts)
		},
	}

	opts.addIdentity(cmd)
	opts.addJSON(cmd)
	cmd.Flags().StringVar(&opts.doing, "doing", "",
		"what you're doing right now, shown by tether explain (e.g. \"compiling tests, ~5min\")")

	return quiet(cmd)
}

func runRegister(cmd *cobra.Command, args []string, opts *registerOptions) error {
	nameFlag := opts.name
	if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
		nameFlag = args[0]
	}

	name, workspace, err := resolveSelf(nameFlag, opts.workspace)
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
		Doing:     opts.doing,
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
		addr = address(res.Name, workspace)
	}
	addr = sanitizeTerminal(addr)

	verb := "registered"
	switch {
	case res.Renamed:
		verb = "renamed to"
	case !res.Created:
		verb = "refreshed registration for"
	}
	if _, err := fmt.Fprintf(out, "%s %s\n", verb, addr); err != nil {
		return err
	}
	return keyValues(out, [][2]string{
		{"harness", dash(res.Harness)},
	})
}

// registerError turns a 409 (name held by a live agent) into an actionable
// message. The daemon's own message already carries a free alternative when
// one was found (see withNameSuggestion in internal/daemon).
func registerError(name, workspace string, err error) error {
	if code, ok := daemonCode(err); ok && code == protocol.CodeConflict {
		var pe *protocol.Error
		_ = errors.As(err, &pe)
		return fail(exitConflict, fmt.Errorf(
			"cannot register %s: %s\n"+
				"       the name is held by a live agent — pick a different name, "+
				"or run `tether ls` to see who holds it",
			address(name, workspace), sanitizeTerminal(pe.Message)))
	}
	return err
}
