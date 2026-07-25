// Command tether is the CLI for cross-harness agent messaging.
//
// Coding-agent harnesses -- Claude Code, Codex, Gemini CLI, Cline and the rest --
// each ship their own session registry and mailbox, and none of them cross. tether
// is one registry and one inbox for all of them, driven from the shell.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags)" ./cmd/tether
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// cobra has already printed the error.
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "tether",
		Short:        "Let coding-agent harnesses talk to each other",
		SilenceUsage: true,
		// AXI principle 8: a bare invocation shows live data, not help text.
		// Until there is a daemon to ask, say so plainly rather than dumping usage.
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "0 agents. No daemon running.")
			return err
		},
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		},
	})

	return root
}
