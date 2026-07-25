package main

import (
	"encoding/json"
	"fmt"

	"github.com/praneethravuri/tether/pkg/protocol"
	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:   "register <name>",
	Short: "Register this agent with the Tether mailbox",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := callMailbox(protocol.MailboxRequest{Op: protocol.MailboxOpRegister, From: args[0]})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "registered %q\n", args[0])
		return nil
	},
}

var sendCmd = &cobra.Command{
	Use:   "send <from> <to> <message>",
	Short: "Send a message to one agent, or \"*\" for every registered agent",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, body := args[0], args[1], args[2]
		if _, err := callMailbox(protocol.MailboxRequest{Op: protocol.MailboxOpSend, From: from, To: to, Body: body}); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "sent")
		return nil
	},
}

var inboxCmd = &cobra.Command{
	Use:   "inbox <name>",
	Short: "Read and clear this agent's pending messages",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := callMailbox(protocol.MailboxRequest{Op: protocol.MailboxOpInbox, From: args[0]})
		if err != nil {
			return err
		}
		if len(resp.Inbox) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no messages")
			return nil
		}
		for _, m := range resp.Inbox {
			fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s\n", m.At.Format("15:04:05"), m.From, m.Body)
		}
		return nil
	},
}

var whoCmd = &cobra.Command{
	Use:   "who",
	Short: "List agents seen in the last 30 minutes",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := callMailbox(protocol.MailboxRequest{Op: protocol.MailboxOpWho})
		if err != nil {
			return err
		}
		if len(resp.Agents) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no active agents")
			return nil
		}
		out, _ := json.MarshalIndent(resp.Agents, "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(out))
		return nil
	},
}
