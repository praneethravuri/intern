package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/praneethravuri/helios/pkg/protocol"
	"github.com/spf13/cobra"
)

// mcpCmd starts an MCP-over-stdio server exposing the same 4 mailbox ops as the
// CLI, dialing the same heliosd TCP mailbox listener. Optional in v1 -- wire this
// in only for a harness that can't shell out to the CLI directly (see the v1
// build spec artifact and [[Messaging Bus Pivot]] in the vault for why the CLI is
// the default, not this).
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start an MCP server exposing register/send/inbox/who as tools (optional; the CLI is the default)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := server.NewMCPServer("helios", "0.1.0")

		s.AddTool(mcp.NewTool("helios_register",
			mcp.WithDescription("Register this agent with the Helios mailbox"),
			mcp.WithString("name", mcp.Required(), mcp.Description("agent name")),
		), mcpHandleRegister)

		s.AddTool(mcp.NewTool("helios_send",
			mcp.WithDescription("Send a message to one agent, or \"*\" for every registered agent"),
			mcp.WithString("from", mcp.Required(), mcp.Description("your agent name")),
			mcp.WithString("to", mcp.Required(), mcp.Description("target agent name, or \"*\" for everyone")),
			mcp.WithString("body", mcp.Required(), mcp.Description("message body")),
		), mcpHandleSend)

		s.AddTool(mcp.NewTool("helios_inbox",
			mcp.WithDescription("Read and clear this agent's pending messages"),
			mcp.WithString("as", mcp.Required(), mcp.Description("your agent name")),
		), mcpHandleInbox)

		s.AddTool(mcp.NewTool("helios_who",
			mcp.WithDescription("List agents seen in the last 30 minutes"),
		), mcpHandleWho)

		return server.ServeStdio(s)
	},
}

func mcpHandleRegister(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if _, err := callMailbox(protocol.MailboxRequest{Op: protocol.MailboxOpRegister, From: name}); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("registered %q", name)), nil
}

func mcpHandleSend(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	from, err := req.RequireString("from")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to, err := req.RequireString("to")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, err := req.RequireString("body")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if _, err := callMailbox(protocol.MailboxRequest{Op: protocol.MailboxOpSend, From: from, To: to, Body: body}); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("sent"), nil
}

func mcpHandleInbox(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	as, err := req.RequireString("as")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := callMailbox(protocol.MailboxRequest{Op: protocol.MailboxOpInbox, From: as})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out, _ := json.Marshal(resp.Inbox)
	return mcp.NewToolResultText(string(out)), nil
}

func mcpHandleWho(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := callMailbox(protocol.MailboxRequest{Op: protocol.MailboxOpWho})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out, _ := json.Marshal(resp.Agents)
	return mcp.NewToolResultText(string(out)), nil
}
