package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/praneethravuri/tether/pkg/protocol"
)

func callDaemon(method string) (*protocol.Response, error) {
	sockPath, err := protocol.SocketPath()
	if err != nil {
		return nil, fmt.Errorf("could not resolve socket path: %w", err)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, errors.New("0 agents. No daemon running")
	}

	defer func() { _ = conn.Close() }()

	req := protocol.Request{
		ID:     "cli-1",
		Method: method,
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	var res protocol.Response
	if err := json.NewDecoder(conn).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if res.Error != nil {
		return nil, fmt.Errorf("daemon error (code %d): %s", res.Error.Code, res.Error.Message)
	}

	return &res, nil
}
