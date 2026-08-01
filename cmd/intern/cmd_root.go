package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/intern/internal/protocol"
)

// runRoot is root's RunE for a bare `intern` invocation: a live inbox
// glance, not a usage dump. It registers you implicitly, like every other
// command, only once the workspace already has somebody in it -- an empty
// workspace is reported as-is rather than minting a name nobody asked for.
func runRoot(cmd *cobra.Command) error {
	workspace, err := resolveWorkspace("")
	if err != nil {
		return err
	}

	var ls protocol.LsResult
	if err := call(protocol.MethodLs, protocol.LsParams{Workspace: workspace}, &ls); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(ls.Agents) == 0 {
		_, err := fmt.Fprintf(out,
			"no agent registered in %s yet — register with `intern register --as <name>`\n", workspace)
		return err
	}

	name, err := ensureRegistered("", workspace)
	if err != nil {
		return err
	}
	_, session := currentSession()

	var inbox protocol.InboxResult
	if err := call(protocol.MethodInbox, protocol.InboxParams{
		Name: name, Workspace: workspace, Session: session, Peek: true,
	}, &inbox); err != nil {
		return err
	}

	addr := address(name, workspace)
	if inbox.Pending == 0 {
		if _, err := fmt.Fprintf(out, "0 unread for %s\n", addr); err != nil {
			return err
		}
		return next(out, "intern wait --timeout 5m")
	}
	if _, err := fmt.Fprintf(out, "%d unread for %s\n", inbox.Pending, addr); err != nil {
		return err
	}
	return next(out, "intern inbox")
}
