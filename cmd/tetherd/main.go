package main

import (
	"encoding/json"
	"fmt"
	"github.com/praneethravuri/tether/pkg/protocol"
	"io"
	"log"
	"net"
	"time"
)

func main() {
	sockPath, err := protocol.SocketPath()
	if err != nil {
		log.Fatalf("Failed to resolve socket path: %v", err)
	}

	listener, err := protocol.Listen(sockPath)

	if err != nil {
		log.Fatalf("Daemon failed to bind socket: %v", err)
	}

	defer func() { _ = listener.Close() }()

	log.Printf("tetherd listening on %s", sockPath)

	reg := NewRegistry()

	// infinite accept loop
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		go handleConnection(conn, reg)
	}
}

func handleConnection(conn net.Conn, reg *Registry) {
	defer func() { _ = conn.Close() }()

	pid, err := getPeerPID(conn)
	if err != nil {
		log.Printf("Failed to get peer PID: %v", err)
		return
	}

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req protocol.Request
		if err := decoder.Decode(&req); err != nil {
			if err != io.EOF {
				log.Printf("JSON decode error from PID %d: %v", pid, err)
			}
			break
		}

		res := handleCommand(req, pid, reg)

		if err := encoder.Encode(res); err != nil {
			log.Printf("Failed to write response to PID %d: %v", pid, err)
			break
		}
	}
}

// TODO: DetectHarness() instead of unknown
// TODO: agent should not name itself
func handleCommand(req protocol.Request, pid int, reg *Registry) protocol.Response {
	res := protocol.Response{ID: req.ID}

	switch req.Method {
	case "register":
		meta := AgentMeta{
			Harness:   "unknown",
			PID:       pid,
			StartTime: time.Now(),
		}

		if err := reg.Register(fmt.Sprintf("agent-%d", pid), meta); err != nil {
			res.Error = &protocol.Error{Code: 1, Message: err.Error()}
		} else {
			res.Result = "registered"
		}
	case "list":
		res.Result = reg.List()
	default:
		res.Error = &protocol.Error{Code: 2, Message: "unknown method: " + req.Method}
	}

	return res
}
