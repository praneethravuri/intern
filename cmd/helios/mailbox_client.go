package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"

	"github.com/praneethravuri/helios/pkg/protocol"
)

// callMailbox sends one request and returns the daemon's response.
func callMailbox(req protocol.MailboxRequest) (protocol.MailboxResponse, error) {
	conn, err := net.Dial("tcp", protocol.MailboxAddrFromEnv())
	if err != nil {
		return protocol.MailboxResponse{}, fmt.Errorf("connect to heliosd mailbox: %w", err)
	}
	defer conn.Close()

	line, err := json.Marshal(req)
	if err != nil {
		return protocol.MailboxResponse{}, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return protocol.MailboxResponse{}, fmt.Errorf("send request: %w", err)
	}

	var resp protocol.MailboxResponse
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return protocol.MailboxResponse{}, fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}
