package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/internal/protocol"
)

const inboxLong = `Show the messages waiting for this agent.

By default this drains the inbox: messages are shown and acknowledged in the
same call, so there is no separate step to get wrong. Use --peek to look
without clearing anything, or --replay to see messages an earlier drain
already delivered.

Use --json when another program has to read this.`

// defaultBodyDisplayMax caps a message body shown in the human-readable
// table/list view. --json is never affected: it always carries the full
// body, straight from res.Messages.
const defaultBodyDisplayMax = 2000

type inboxOptions struct {
	identityFlags
	limit  int
	peek   bool
	replay bool
	full   bool
}

func newInboxCmd() *cobra.Command {
	var opts inboxOptions

	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Show messages waiting for this agent",
		Long:  inboxLong,
		Example: "  tether inbox\n" +
			"  tether inbox --as frontend --limit 10\n" +
			"  tether inbox --peek\n" +
			"  tether inbox --replay --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInbox(cmd, &opts)
		},
	}

	opts.addIdentity(cmd)
	opts.addJSON(cmd)
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "maximum messages to return (default 50, max 500)")
	cmd.Flags().BoolVar(&opts.peek, "peek", false, "show messages without clearing them")
	cmd.Flags().BoolVar(&opts.replay, "replay", false, "show messages an earlier drain already delivered")
	cmd.Flags().BoolVar(&opts.full, "full", false,
		"show every message body in full, without the human-view truncation (--json is always full)")

	return quiet(cmd)
}

func runInbox(cmd *cobra.Command, opts *inboxOptions) error {
	name, workspace, err := resolveSelf(opts.name, opts.workspace)
	if err != nil {
		return err
	}
	if opts.limit < 0 {
		return failf(exitGeneral, "--limit cannot be negative")
	}
	if opts.peek && opts.replay {
		return failf(exitGeneral, "--peek and --replay cannot be used together")
	}

	name, err = ensureRegistered(name, workspace)
	if err != nil {
		return err
	}
	_, session := currentSession()

	params := protocol.InboxParams{
		Name:      name,
		Workspace: workspace,
		Session:   session,
		Limit:     opts.limit,
		Peek:      opts.peek,
		Replay:    opts.replay,
	}

	var res protocol.InboxResult
	if err := call(protocol.MethodInbox, params, &res); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.jsonOut {
		if res.Messages == nil {
			res.Messages = []protocol.MessageView{}
		}
		return printJSON(out, res)
	}

	if res.Dropped > 0 { // a warning, not part of the machine-parseable channel: stderr, not stdout
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(),
			"%d messages were dropped to keep this inbox under its limit — read more often\n",
			res.Dropped); err != nil {
			return err
		}
	}

	if len(res.Messages) == 0 {
		return empty(out, "messages", "tether wait --timeout 5m")
	}

	if _, err := fmt.Fprintln(out, untrustedContentNotice); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	for _, m := range res.Messages {
		if err := writeMessage(cmd, m, opts.full); err != nil {
			return err
		}
	}

	switch {
	case opts.peek:
		_, err = fmt.Fprintf(out, "%s (peek — not cleared).\n", plural(len(res.Messages), "message", "messages"))
	case opts.replay:
		_, err = fmt.Fprintf(out, "%s (history).\n", plural(len(res.Messages), "message", "messages"))
	default:
		_, err = fmt.Fprintf(out, "%s, inbox cleared.\n", plural(len(res.Messages), "message", "messages"))
	}
	return err
}

// writeMessage renders one message: a scannable header, then the body
// indented underneath. Every field is sanitised for the terminal; --json
// still gets the body byte for byte via res.Messages, never through here.
func writeMessage(cmd *cobra.Command, m protocol.MessageView, full bool) error {
	out := cmd.OutOrStdout()

	header := fmt.Sprintf("[%s] %s · %s · %s",
		sanitizeTerminal(m.ID), dash(m.From), dash(m.Kind), relTime(m.CreatedAt))
	if m.ReplyTo != "" {
		header += " · reply to " + sanitizeTerminal(m.ReplyTo)
	}
	if _, err := fmt.Fprintln(out, header); err != nil {
		return err
	}

	body := strings.TrimRight(m.Body, "\n")
	if body == "" {
		body = "(empty body)"
	}
	body = sanitizeTerminal(body)
	if !full {
		if shortened, cut := truncate(body, defaultBodyDisplayMax); cut {
			body = fmt.Sprintf("%s\n  ... (%d bytes total, --full for all)", shortened, len(m.Body))
		}
	}
	if _, err := fmt.Fprintln(out, indent(body, "  ")); err != nil {
		return err
	}

	_, err := fmt.Fprintln(out)
	return err
}
