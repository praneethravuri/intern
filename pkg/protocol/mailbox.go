package protocol

import (
	"os"
	"time"
)

// MailboxAddr is the default TCP loopback address for the mailbox RPC surface.
// Unlike SocketPath (a Unix domain socket, unavailable on Windows), TCP loopback
// works identically on any OS -- the mailbox is meant to be reachable from any
// harness regardless of platform. Override with TETHER_MAILBOX_ADDR.
const MailboxAddr = "127.0.0.1:47530"

// MailboxAddrFromEnv returns TETHER_MAILBOX_ADDR if set, else MailboxAddr.
// Shared by tetherd (listener) and tether (client) so both sides agree on
// where the mailbox lives.
func MailboxAddrFromEnv() string {
	if addr := os.Getenv("TETHER_MAILBOX_ADDR"); addr != "" {
		return addr
	}
	return MailboxAddr
}

const (
	MailboxOpRegister = "register"
	MailboxOpSend     = "send"
	MailboxOpInbox    = "inbox"
	MailboxOpWho      = "who"
)

// MailboxMessage is one message sitting in an agent's inbox.
type MailboxMessage struct {
	From string    `json:"from"`
	Body string    `json:"body"`
	At   time.Time `json:"at"`
}

// MailboxRequest is one newline-delimited JSON request to the mailbox listener.
type MailboxRequest struct {
	Op   string `json:"op"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Body string `json:"body,omitempty"`
}

// MailboxResponse is the reply to a MailboxRequest.
type MailboxResponse struct {
	OK     bool             `json:"ok"`
	Error  string           `json:"error,omitempty"`
	Agents []string         `json:"agents,omitempty"` // MailboxOpWho
	Inbox  []MailboxMessage `json:"inbox,omitempty"`  // MailboxOpInbox
}
