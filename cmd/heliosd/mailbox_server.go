package main

import (
	"bufio"
	"encoding/json"
	"net"

	"github.com/praneethravuri/helios/pkg/protocol"
	"go.uber.org/zap"
)

// serveMailbox listens on TCP loopback -- not the Unix socket the PTY side uses --
// so the mailbox works from any OS, not just macOS/Linux. One request per
// connection: read one line of JSON, dispatch, write one line of JSON, close.
func serveMailbox(mb *Mailbox, log *zap.SugaredLogger) {
	addr := protocol.MailboxAddrFromEnv()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalw("Failed to start mailbox listener", "addr", addr, "error", err)
	}
	log.Infof("Mailbox listening on tcp: %s", addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Warnw("Failed to accept mailbox connection", "error", err)
			continue
		}
		go handleMailboxConn(conn, mb, log)
	}
}

func handleMailboxConn(conn net.Conn, mb *Mailbox, log *zap.SugaredLogger) {
	defer conn.Close()

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		log.Warnw("Failed to read mailbox request", "error", err)
		return
	}

	var req protocol.MailboxRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		writeMailboxResponse(conn, protocol.MailboxResponse{OK: false, Error: "malformed request: " + err.Error()})
		return
	}

	resp := dispatchMailboxRequest(mb, req)
	writeMailboxResponse(conn, resp)
}

func dispatchMailboxRequest(mb *Mailbox, req protocol.MailboxRequest) protocol.MailboxResponse {
	switch req.Op {
	case protocol.MailboxOpRegister:
		mb.Register(req.From)
		return protocol.MailboxResponse{OK: true}

	case protocol.MailboxOpSend:
		if err := mb.Send(req.From, req.To, req.Body); err != nil {
			return protocol.MailboxResponse{OK: false, Error: err.Error()}
		}
		return protocol.MailboxResponse{OK: true}

	case protocol.MailboxOpInbox:
		msgs, err := mb.Inbox(req.From)
		if err != nil {
			return protocol.MailboxResponse{OK: false, Error: err.Error()}
		}
		return protocol.MailboxResponse{OK: true, Inbox: msgs}

	case protocol.MailboxOpWho:
		return protocol.MailboxResponse{OK: true, Agents: mb.Who()}

	default:
		return protocol.MailboxResponse{OK: false, Error: "unknown op: " + req.Op}
	}
}

func writeMailboxResponse(conn net.Conn, resp protocol.MailboxResponse) {
	enc := json.NewEncoder(conn)
	_ = enc.Encode(resp)
}
