package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// commandsByName returns every subcommand this binary registers, keyed by
// its cobra Use name, plus "" for the bare root command itself.
func commandsByName(t *testing.T) map[string]*cobra.Command {
	t.Helper()
	root := newRootCmd()
	m := map[string]*cobra.Command{"": root}
	for _, c := range root.Commands() {
		m[c.Name()] = c
	}
	return m
}

func readReadme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	return string(b)
}

// flagRow matches one Flags-table row: "| `command` | `--flag ...` | ...".
var flagRow = regexp.MustCompile("^\\| `([a-z]+)` \\| `--([a-zA-Z-]+)")

// TestReadmeFlagsTableMatchesRegisteredFlags kills spec drift 3: every
// per-command flag the README's Flags table documents must actually be
// registered on that cobra command, so a renamed or removed flag is caught
// here instead of by a user hitting "unknown flag".
func TestReadmeFlagsTableMatchesRegisteredFlags(t *testing.T) {
	cmds := commandsByName(t)

	matched := 0
	for _, line := range strings.Split(readReadme(t), "\n") {
		m := flagRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		cmdName, flagName := m[1], m[2]
		matched++

		cmd, ok := cmds[cmdName]
		if !ok {
			t.Errorf("README documents --%s on %q, which is not a real command", flagName, cmdName)
			continue
		}
		if cmd.Flags().Lookup(flagName) == nil {
			t.Errorf("README documents --%s on %q, but that command has no such flag", flagName, cmdName)
		}
	}
	if matched < 8 {
		t.Fatalf("only matched %d Flags-table rows; the regex or the table format drifted", matched)
	}
}

// TestUniversalFlagsMatchReadmeClaim locks in which commands accept
// --as/--workspace/--json, matching the CLI Reference sentence in README.md.
// Update both together if a command's flag set changes.
func TestUniversalFlagsMatchReadmeClaim(t *testing.T) {
	identityBearing := []string{"register", "send", "inbox", "wait", "explain"}
	workspaceOnly := []string{"ls", "doctor"}
	// top refreshes continuously, so a one-shot --json snapshot doesn't fit.
	workspaceOnlyNoJSON := []string{"top"}

	wantAs := map[string]bool{}
	wantWorkspace := map[string]bool{}
	wantJSON := map[string]bool{}
	for _, name := range identityBearing {
		wantAs[name] = true
		wantWorkspace[name] = true
		wantJSON[name] = true
	}
	for _, name := range workspaceOnly {
		wantWorkspace[name] = true
		wantJSON[name] = true
	}
	for _, name := range workspaceOnlyNoJSON {
		wantWorkspace[name] = true
	}

	cmds := commandsByName(t)
	for name, cmd := range cmds {
		if name == "" {
			continue // bare tether (inbox glance): no flags at all
		}
		if got := cmd.Flags().Lookup("as") != nil; got != wantAs[name] {
			t.Errorf("%s: --as registered = %v, want %v", name, got, wantAs[name])
		}
		if got := cmd.Flags().Lookup("workspace") != nil; got != wantWorkspace[name] {
			t.Errorf("%s: --workspace registered = %v, want %v", name, got, wantWorkspace[name])
		}
		if got := cmd.Flags().Lookup("json") != nil; got != wantJSON[name] {
			t.Errorf("%s: --json registered = %v, want %v", name, got, wantJSON[name])
		}
	}
}
