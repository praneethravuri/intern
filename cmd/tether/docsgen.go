package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// docsCommandOrder is docs/reference.md's row order: the workflow a new
// agent follows, not cobra's alphabetical default. A new top-level command
// belongs here too, or TestDocsCommandCoverage fails.
var docsCommandOrder = [][]string{
	{},
	{"start"},
	{"register"},
	{"send"},
	{"inbox"},
	{"wait"},
	{"ls"},
	{"top"},
	{"explain"},
	{"claim"},
	{"release"},
	{"claims"},
	{"doctor"},
	{"demo"},
	{"hooks", "install"},
	{"version"},
}

// docsOmittedHooksChildren are hooks subcommands intentionally left out of
// the public docs: run-stop/run-session-start are Claude Code's own hook
// entry points, never typed by a person, and status is covered in prose
// next to install.
var docsOmittedHooksChildren = map[string]bool{
	"status":            true,
	"run-stop":          true,
	"run-session-start": true,
}

// rootBareDescription documents what running "tether" with no subcommand
// does. The root command's own Short is its --help one-liner for the whole
// binary, not a description of the bare-invocation behavior, so it can't be
// reused here the way every other row reuses its command's Short.
const rootBareDescription = "Shows a quick glance at your own inbox: how many messages are pending. Auto-starts the daemon and registers implicitly, like every other command, once the workspace has somebody in it."

// resolveDocCmd walks path from root, returning the command it names.
func resolveDocCmd(root *cobra.Command, path []string) (*cobra.Command, error) {
	cmd := root
	for _, name := range path {
		next, err := findChild(cmd, name)
		if err != nil {
			return nil, err
		}
		cmd = next
	}
	return cmd, nil
}

func findChild(cmd *cobra.Command, name string) (*cobra.Command, error) {
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("gendocs: %q has no child command %q", cmd.Name(), name)
}

// commandUseLine renders "tether <path...> <args>" the way the docs display
// it: the leaf's own args hint, no cobra-added "[flags]" suffix.
func commandUseLine(cmd *cobra.Command) string {
	if !cmd.HasParent() {
		return cmd.Use
	}
	return commandUseLine(cmd.Parent()) + " " + cmd.Use
}

func referenceTableRows(root *cobra.Command) ([][]string, error) {
	rows := make([][]string, 0, len(docsCommandOrder))
	for _, path := range docsCommandOrder {
		cmd, err := resolveDocCmd(root, path)
		if err != nil {
			return nil, err
		}
		desc := cmd.Short
		if len(path) == 0 {
			desc = rootBareDescription
		}
		rows = append(rows, []string{"`" + commandUseLine(cmd) + "`", desc})
	}
	return rows, nil
}

// flagValueHints supplies a readable placeholder for flags whose pflag type
// ("string") is too generic to render on its own. Anything absent here falls
// back to a type-derived hint in flagValueHint. Flag names, existence, and
// descriptions still come entirely from the live command tree -- only this
// cosmetic placeholder is hand-picked.
var flagValueHints = map[string]string{
	"claim/holder":        "<text>",
	"release/if-claim-id": "<id>",
	"register/doing":      "<text>",
	"send/kind":           "note|handoff|question|answer",
	"send/reply-to":       "<id>",
	"send/body-file":      "<path|->",
}

func flagValueHint(label string, f *pflag.Flag) string {
	if h, ok := flagValueHints[label+"/"+f.Name]; ok {
		return h
	}
	switch f.Value.Type() {
	case "bool":
		return ""
	case "duration":
		return "<duration>"
	case "int":
		return "<n>"
	default:
		return "<value>"
	}
}

// universalFlagNames are documented once in reference.md's own prose, not
// repeated per row in the Flags table.
var universalFlagNames = map[string]bool{"as": true, "workspace": true, "json": true}

func flagsTableRows(root *cobra.Command) ([][]string, error) {
	var rows [][]string
	for _, path := range docsCommandOrder {
		if len(path) == 0 {
			continue
		}
		cmd, err := resolveDocCmd(root, path)
		if err != nil {
			return nil, err
		}
		label := strings.Join(path, " ")
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if universalFlagNames[f.Name] {
				return
			}
			flag := "--" + f.Name
			if hint := flagValueHint(label, f); hint != "" {
				flag += " " + hint
			}
			rows = append(rows, []string{"`" + label + "`", "`" + flag + "`", f.Usage})
		})
	}
	return rows, nil
}

func renderTable(header []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	sep := make([]string, len(header))
	for i := range sep {
		sep[i] = "---"
	}
	b.WriteString("| " + strings.Join(sep, " | ") + " |\n")
	for _, r := range rows {
		b.WriteString("| " + strings.Join(r, " | ") + " |\n")
	}
	return b.String()
}

const referenceIntro = `` +
	`Generated from the live command definitions -- see ` + "`cmd/tether/docsgen.go`" + `.
Run ` + "`go test ./cmd/tether -run TestGeneratedDocsMatchCheckedIn -update`" + ` after
changing a command's flags or help text to regenerate it.

` + "`--json`" + ` is accepted by every command except ` + "`version`" + `, ` + "`top`" + `, and bare ` + "`tether`" + `.
` + "`--as <name>`" + ` and ` + "`--workspace <name>`" + ` are accepted by ` + "`register`" + `, ` + "`send`" + `,
` + "`inbox`" + `, ` + "`wait`" + `, and ` + "`explain`" + `; ` + "`ls`" + `, ` + "`top`" + `, ` + "`doctor`" + `, ` + "`claim`" + `,
` + "`release`" + `, and ` + "`claims`" + ` accept ` + "`--workspace`" + ` but not ` + "`--as`" + ` (a claim is owned by
a process, not a registered agent name); ` + "`version`" + `, ` + "`demo`" + `, and bare
` + "`tether`" + ` accept neither. Every identity-bearing command auto-starts the
daemon if it isn't running, and also registers implicitly if you never called
` + "`register`" + ` yourself -- usually minting a name from your harness,
` + "`<harness>-<hex4>`" + ` -- except ` + "`doctor`" + `, which only diagnoses and never
auto-starts.`

const stateNotes = `` +
	"`ls`/`explain`" + ` compute a state fresh on every call, never stored, in priority
order: ` + "`gone`" + ` (pid no longer alive) -> ` + "`blocked`" + ` (parked in a live ` + "`wait`" + `) ->
` + "`working`" + ` (ran a command in the last 60s) -> ` + "`quiet`" + ` (ran one, just not
recently) -> ` + "`unknown`" + ` (registered, nothing observed yet). ` + "`register --doing \"compiling tests, ~5min\"`" + `
sets a note that ` + "`explain`" + ` shows in place of the generic evidence string, for
anything that runs long enough to otherwise read as quiet.`

const claimNotes = `A claim answers "who owns this file right now": three independent facts,
never inferred from one another -- which live process holds it (self-heals
like presence, via the same pid+start-time check), a durable lease id
(128-bit random, freshly minted on every acquisition, required to release),
and a free-text holder label (diagnostic only, shown by ` + "`tether claims`" + `,
never checked by ` + "`release`" + `). A claim held by a process that has since died
is reclaimed immediately by the next ` + "`claim`" + `, without waiting for its TTL
(15m, a daemon-side default) to elapse.`

const flagsClosing = "`--as`, `--workspace`, and `--json` apply as described above and are omitted " +
	`from this table.

Message kinds (` + "`note`, `handoff`, `question`, `answer`" + `) are advisory: the receiver
decides what to do with each, but a shared vocabulary lets an agent triage
its inbox without reading every body.

On a name conflict, ` + "`register`" + ` suggests a free alternative (` + "`frontend`" + ` taken ->
` + "`frontend-2`" + `).`

const exitCodesBody = `Exit codes are part of the contract -- a script or an agent branches on
these, so they never change meaning:

| Code | Name | Meaning |
| --- | --- | --- |
| ` + "`0`" + ` | success | the command did what it was asked to do |
| ` + "`1`" + ` | general error | any failure without a more specific code |
| ` + "`3`" + ` | no daemon | the daemon could not be reached, including after an auto-start attempt |
| ` + "`4`" + ` | timeout / not found | ` + "`tether wait`" + ` returned with no mail, or ` + "`tether send`" + ` addressed an agent that does not exist ("nobody was there" either way) |
| ` + "`5`" + ` | conflict | the request collided with existing state -- a name already held by a live agent, a key already claimed by a live process, or a ` + "`release --if-claim-id`" + ` that doesn't match the claim's current lease |
`

const configBody = `| Variable | Effect |
| --- | --- |
| ` + "`TETHER_SOCK`" + ` | Socket path. Otherwise ` + "`$XDG_RUNTIME_DIR/tether/sock`" + `, otherwise ` + "`~/.tether/sock`" + `. |
| ` + "`TETHER_DB`" + ` | Database path. Otherwise ` + "`~/.tether/tether.db`" + `. |
| ` + "`TETHER_WORKSPACE`" + ` | Overrides workspace detection entirely (otherwise the basename of the git root of the current directory). |
| ` + "`TETHER_SESSION_ID`" + ` | Overrides the session id used to authenticate "acting as ` + "`--as X`" + `" claims. Only needed if your harness is not one ` + "`tether`" + ` recognises; most callers never set this -- an unrecognised harness still gets a stable, per-shell synthetic session id automatically. |

Outside a git repo entirely, workspace detection falls back to the current
directory's own basename rather than failing.

The socket is created mode 0600 inside a 0700 directory, so only your user
can reach the bus. See [SECURITY.md](../SECURITY.md) for the full threat
model.
`

// buildReferenceDoc renders docs/reference.md from the live command tree.
func buildReferenceDoc(root *cobra.Command) (string, error) {
	refRows, err := referenceTableRows(root)
	if err != nil {
		return "", err
	}
	flagRows, err := flagsTableRows(root)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# CLI Reference\n\n")
	b.WriteString(referenceIntro + "\n\n")
	b.WriteString(renderTable([]string{"Command", "What it does"}, refRows))
	b.WriteString("\n" + stateNotes + "\n\n")
	b.WriteString(claimNotes + "\n\n")
	b.WriteString("## Flags\n\n")
	b.WriteString(renderTable([]string{"Command", "Flag", "Description"}, flagRows))
	b.WriteString("\n" + flagsClosing + "\n\n")
	b.WriteString("## Exit codes\n\n" + exitCodesBody + "\n")
	b.WriteString("## Configuration\n\n" + configBody)
	return b.String(), nil
}
