package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/internal/daemon"
	"github.com/praneethravuri/tether/internal/protocol"
)

// daemonBanner is the text printed once before the daemon blocks, kept as a
// pure function so its wording is testable without starting a real daemon.
func daemonBanner(sock, db string) string {
	return fmt.Sprintf(
		"tether: running the daemon in the foreground (Ctrl-C to stop)\n"+
			"tether: socket %s · db %s\n"+
			"tether: for the fleet view, run `tether ls`\n",
		sock, db)
}

// runDaemon is root's RunE when tether is invoked with no subcommand.
func runDaemon(cmd *cobra.Command) error {
	sock, err := protocol.SocketPath()
	if err != nil {
		return err
	}
	db, err := protocol.DBPath()
	if err != nil {
		return err
	}

	if _, err := fmt.Fprint(cmd.OutOrStdout(), daemonBanner(sock, db)); err != nil {
		return err
	}

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("tether: ")

	return daemonRunErr(daemon.Run())
}

// daemonRunErr maps daemon.Run's error to the CLI's exit code contract: a
// daemon already holding the socket is a conflict (drift item 9), not a
// general failure.
func daemonRunErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, daemon.ErrAlreadyRunning) {
		return fail(exitConflict, err)
	}
	return err
}
