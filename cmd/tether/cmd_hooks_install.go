package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/internal/hooks/claudecode"
)

const hooksInstallLong = `Write tether's Stop and SessionStart hooks into Claude Code's settings.json.

Merges into the existing "hooks" key rather than replacing it, so any other
tool's hook entries and every other top-level setting survive untouched.
Safe to run again: a second run repairs a stale binary path (if this binary
moved) instead of duplicating the entry, and only touches the file when
something actually changed.

Targets the project-level .claude/settings.json in the current directory by
default; --user targets ~/.claude/settings.json instead. Only run this
command explicitly -- nothing else in tether ever writes to it.`

const hooksStatusLong = `Report whether tether's Claude Code hooks are installed and current.

Targets the same project-level .claude/settings.json as "hooks install" by
default; --user checks ~/.claude/settings.json instead.`

// hooksTargetOptions are the flags install and status share: which
// settings.json to touch, and whether to emit JSON.
type hooksTargetOptions struct {
	user    bool
	jsonOut bool
}

func (o *hooksTargetOptions) addFlags(cmd *cobra.Command, targetHelp string) {
	cmd.Flags().BoolVar(&o.user, "user", false, targetHelp)
	cmd.Flags().BoolVar(&o.jsonOut, "json", false, "emit the raw result as JSON")
}

func newHooksInstallCmd() *cobra.Command {
	var opts hooksTargetOptions

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the Claude Code hooks into settings.json",
		Long:  hooksInstallLong,
		Example: "  tether hooks install\n" +
			"  tether hooks install --user\n" +
			"  tether hooks install --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHooksInstall(cmd, &opts)
		},
	}
	opts.addFlags(cmd, "target ~/.claude/settings.json instead of the project-level file")
	return quiet(cmd)
}

func newHooksStatusCmd() *cobra.Command {
	var opts hooksTargetOptions

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report whether the Claude Code hooks are installed",
		Long:  hooksStatusLong,
		Example: "  tether hooks status\n" +
			"  tether hooks status --user --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHooksStatus(cmd, &opts)
		},
	}
	opts.addFlags(cmd, "check ~/.claude/settings.json instead of the project-level file")
	return quiet(cmd)
}

func runHooksInstall(cmd *cobra.Command, opts *hooksTargetOptions) error {
	path, err := claudeSettingsPath(opts.user)
	if err != nil {
		return err
	}
	exe, err := tetherExecutable()
	if err != nil {
		return err
	}

	res, err := claudecode.Install(path, exe)
	if err != nil {
		return failf(exitGeneral, "cannot install the Claude Code hooks: %v", err)
	}

	out := cmd.OutOrStdout()
	if opts.jsonOut {
		return printJSON(out, res)
	}
	if res.Changed {
		_, err = fmt.Fprintf(out, "installed the Stop and SessionStart hooks into %s\n", res.Path)
	} else {
		_, err = fmt.Fprintf(out, "%s already has tether's hooks up to date\n", res.Path)
	}
	return err
}

func runHooksStatus(cmd *cobra.Command, opts *hooksTargetOptions) error {
	path, err := claudeSettingsPath(opts.user)
	if err != nil {
		return err
	}
	exe, err := tetherExecutable()
	if err != nil {
		return err
	}

	st, err := claudecode.Inspect(path, exe)
	if err != nil {
		return failf(exitGeneral, "cannot read %s: %v", path, err)
	}

	out := cmd.OutOrStdout()
	if opts.jsonOut {
		return printJSON(out, st)
	}

	if err := keyValues(out, [][2]string{
		{"settings", st.Path},
		{"stop hook", hookStateLabel(st.StopInstalled, st.StopCurrent)},
		{"session-start hook", hookStateLabel(st.SessionStartInstalled, st.SessionStartCurrent)},
	}); err != nil {
		return err
	}
	// Current implies Installed (see Inspect), so checking Current alone
	// covers both "missing" and "installed but stale".
	if !st.StopCurrent || !st.SessionStartCurrent {
		return next(out, "tether hooks install")
	}
	return nil
}

func hookStateLabel(installed, current bool) string {
	switch {
	case !installed:
		return "not installed"
	case !current:
		return "installed, stale path"
	default:
		return "installed"
	}
}

// claudeSettingsPath resolves where install/status read and write: the
// project-level file in the current directory, or --user's ~/.claude one.
func claudeSettingsPath(user bool) (string, error) {
	if user {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", failf(exitGeneral, "cannot find home dir: %v", err)
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", failf(exitGeneral, "cannot determine the current directory: %v", err)
	}
	return filepath.Join(cwd, ".claude", "settings.json"), nil
}

// tetherExecutable resolves the absolute path to this running binary, which
// is what the installed hook command must invoke.
func tetherExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", failf(exitGeneral, "cannot determine this binary's own path: %v", err)
	}
	return exe, nil
}
