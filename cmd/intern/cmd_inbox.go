package main

import (
	"github.com/spf13/cobra"

	"github.com/praneethravuri/intern/internal/protocol"
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
		Example: "  intern inbox\n" +
			"  intern inbox --as frontend --limit 10\n" +
			"  intern inbox --peek\n" +
			"  intern inbox --replay --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInbox(cmd, &opts)
		},
	}

	opts.addIdentity(cmd)
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
	if res.Messages == nil {
		res.Messages = []protocol.MessageView{}
	}
	return printJSON(out, res)
}

// writeMessage renders one message: a scannable header, then the body
// indented underneath. Every field is sanitised for the terminal; --json
// still gets the body byte for byte via res.Messages, never through here.
