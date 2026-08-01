package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const hooksLong = `Install or run the hooks that let a harness deliver mail without polling.

"tether hooks install" writes a Stop and a SessionStart hook into Claude
Code's settings.json, so a session blocks on mail in the hook's own process
tree -- outside any tool-call timeout -- and Claude Code wakes it itself when
mail arrives.

Claude Code only, for now: every other harness keeps using ` + "`tether wait`" + `
(in a shell loop, or with --timeout) as its polling-free idle.`

// newHooksCmd is the parent of the install/status pair and the two hidden
// run-* subcommands Claude Code's own hook config invokes.
func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install or run the Claude Code mail-delivery hooks",
		Long:  hooksLong,
	}
	cmd.AddCommand(
		newHooksInstallCmd(),
		newHooksStatusCmd(),
		newHooksRunStopCmd(),
		newHooksRunSessionStartCmd(),
	)
	return quiet(cmd)
}

// hooksStateDir is where hook-runner-only state lives -- single-flight
// locks, budget counters -- never git-tracked, unlike the settings.json
// install writes into.
func hooksStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", failf(exitGeneral, "cannot find home dir: %v", err)
	}
	return filepath.Join(home, ".tether", "hooks"), nil
}
