package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/praneethravuri/intern/internal/sanitize"
)

// quiet marks a command as reporting its own failures -- no "Error: ..." or
// usage dump -- and makes a bad flag list this command's valid ones.
func quiet(cmd *cobra.Command) *cobra.Command {
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetFlagErrorFunc(flagErrorFunc)
	return cmd
}

// flagErrorFunc appends this command's valid flags to a flag-parsing error.
func flagErrorFunc(cmd *cobra.Command, err error) error {
	return fmt.Errorf("%w\nvalid flags for %s: %s", err, cmd.Name(), flagNames(cmd))
}

// flagNames lists cmd's registered flags as "--name", in registration order.
func flagNames(cmd *cobra.Command) string {
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		names = append(names, "--"+f.Name)
	})
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// identityFlags are the flags shared by every command that acts as, or on
// behalf of, a particular agent.
type identityFlags struct {
	name      string
	workspace string
}

// addIdentity registers --as and --workspace on cmd.
func (f *identityFlags) addIdentity(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.name, "as", "",
		"agent name to act as (defaults to whatever this session already registered, or a minted name)")
	cmd.Flags().StringVar(&f.workspace, "workspace", "",
		"workspace to use (defaults to the git root of the current directory)")
}

// addWorkspace registers only --workspace, for commands that do not act as a
// specific agent.
func (f *identityFlags) addWorkspace(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.workspace, "workspace", "",
		"workspace to use (defaults to the git root of the current directory)")
}

// printJSON writes v as indented JSON followed by a newline. This is the only
// place JSON output is produced, so the shape stays consistent.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return failf(exitGeneral, "cannot encode the result as JSON: %v", err)
	}
	return nil
}

// relTime renders an RFC3339 timestamp as a short relative age such as
// "40s ago". A timestamp that cannot be parsed is returned unchanged rather
// than swallowed, so odd data is visible instead of invisible.
func relTime(ts string) string {
	if ts == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return humanSince(time.Since(t))
}

// humanSince renders a duration as a one-unit relative age.
func humanSince(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// relExpiry renders an RFC3339 expiry timestamp as a short relative
// countdown, e.g. "in 15m", or "expired" once past.

// plural renders "1 message" / "2 messages".

// dash sanitises s and replaces an empty field with "-" so table columns never collapse.

// sanitizeTerminal replaces C0 control bytes and DEL (except newline/tab)
// with U+FFFD so a store-derived string can't smuggle terminal escapes.
// Never applied on the --json path, which must stay byte-exact.
func sanitizeTerminal(s string) string {
	return sanitize.Replace(s, '�', true)
}

// untrustedContentNotice is printed once above a human-rendered inbox, since
// message bodies are data from other agents, not instructions.

// indent prefixes every line of s with pad, preserving the text byte for byte
// apart from the added prefix.

// aggregate prints a summary line joined with " · ", e.g. "3 agents · 1 quiet".
// Empty parts are dropped.

// next prints the "Next:" suggestion line every human-readable command ends
// with, pointing at the most likely follow-up command.

// empty prints "0 <noun>." followed by a Next: suggestion -- the shared
// shape for every listing command's empty case.

// truncate shortens s to at most maxRunes runes and reports whether it did.
// Runes, not bytes: cutting mid-rune would corrupt multi-byte UTF-8 and
// could even land inside a terminal escape sequence.

// keyValues prints aligned "key: value" lines under a heading block.
