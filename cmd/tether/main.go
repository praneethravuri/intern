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
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "tether",
		Short:        "Let coding-agent harnesses talk to each other",
		SilenceUsage: true,
		RunE:         runLs,
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

	root.AddCommand(&cobra.Command{
		Use:   "register",
		Short: "Register this agent with the daemon",
		RunE:  runRegister,
	})

	root.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "List all active agents",
		RunE:  runLs,
	})

	return root
}

func runRegister(cmd *cobra.Command, _ []string) error {
	res, err := callDaemon("register")
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.OutOrStderr(), "Successfully registered: %v\n", res.Result)
	return err
}

func runLs(cmd *cobra.Command, _ []string) error {
	res, err := callDaemon("list")
	if err != nil {
		_, writeErr := fmt.Fprintln(cmd.OutOrStderr(), err.Error())
		return writeErr
	}

	agents, ok := res.Result.(map[string]any)
	if !ok || len(agents) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "0 agents.")
		return err
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%d agents online\n\n", len(agents)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-15s %-15s\n", "NAME", "HARNESS"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "-------------------------------"); err != nil {
		return err
	}

	for name, metaAny := range agents {
		meta, ok := metaAny.(map[string]any)
		if !ok {
			continue
		}
		harness, _ := meta["Harness"].(string)
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-15s %-15s\n", name, harness); err != nil {
			return err
		}
	}

	return nil
}
