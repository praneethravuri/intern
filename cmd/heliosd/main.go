package main

import (
	"bufio"
	"github.com/creack/pty"
	"github.com/praneethravuri/helios/pkg/logger"
	"go.uber.org/zap"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const socketPath = "/tmp/helios.sock"

// SessionManager manages active PTY sessions in a thread safe way
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]io.Writer
}

// NewSessionManager creates and initializes a new SessionManager
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]io.Writer),
	}
}

// Register adds a PTY writer to our active sessions map
func (sm *SessionManager) Register(id string, writer io.Writer) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[id] = writer
}

// Deregister removes a PTY writer from our active sessions map
func (sm *SessionManager) Deregister(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, id)
}

// Broadcast sends a message to all registered active PTY sessions
func (sm *SessionManager) Broadcast(msg string, log *zap.SugaredLogger) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Infof("Broadcasting to %d active sessions: %q", len(sm.sessions), msg)

	for id, writer := range sm.sessions {
		log.Infof("Sending command to session: %s", id)
		_, err := writer.Write([]byte(msg))
		if err != nil {
			log.Errorw("Failed to write to session", "sessionID", id, "error", err)
		}
	}
}

func main() {
	log, cleanup := logger.InitLogger()
	defer cleanup()

	log.Info("Starting heliosd...")

	// cleanup the old socket file if it exists
	if err := os.RemoveAll(socketPath); err != nil {
		log.Fatalw("Failed to remove old socket file", "error", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalw("Failed to start Unix socket listener", "error", err)
	}

	defer listener.Close()

	log.Infof("Listening on unix socket: %s", socketPath)

	// Initialize the session manager
	sm := NewSessionManager()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Warnw("Failed to accept client connection", "error", err)
			continue
		}

		log.Info("New client connected")

		go handleConnection(conn, sm, log)
	}
}

func handleConnection(conn net.Conn, sm *SessionManager, log *zap.SugaredLogger) {
	defer conn.Close()

	// read the handshake line from the client
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.Errorw("Failed to read handshake from connection", "error", err)
		return
	}

	command := strings.TrimSpace(line)
	log.Infof("Received handshake: %q", command)

	// parse the command
	if strings.HasPrefix(command, "SPAWN ") {
		sessionID := strings.TrimPrefix(command, "SPAWN ")
		if sessionID == "" {
			log.Warn("Received empty SPAWN session ID")
			return
		}

		log.Infof("Spawing PTY shell sessions: %s", sessionID)
		runSession(conn, sessionID, sm, log)
	} else if strings.HasPrefix(command, "BROADCAST ") {
		broadcastMsg := strings.TrimPrefix(command, "BROADCAST ")
		if !strings.HasSuffix(broadcastMsg, "\n") {
			broadcastMsg = broadcastMsg + "\n"
		}

		sm.Broadcast(broadcastMsg, log)
		log.Info("Broadcast completed successfully")
	} else {
		log.Warnf("Unknown handshake command: %q", command)
	}
}

func runSession(conn net.Conn, sessionID string, sm *SessionManager, log *zap.SugaredLogger) {
	cmd := exec.Command("/bin/zsh")

	ptyMaster, err := pty.Start(cmd)
	if err != nil {
		log.Errorw("Failed to start shell in PTY", "sessionID", sessionID, "error", err)
		return
	}

	defer ptyMaster.Close()

	sm.Register(sessionID, ptyMaster)
	defer sm.Deregister(sessionID)

	defer func() {
		_ = cmd.Process.Kill()
	}()

	log.Infof("Session %s registered and active", sessionID)

	go func() {
		_, _ = io.Copy(ptyMaster, conn)
	}()

	_, _ = io.Copy(conn, ptyMaster)

	log.Infof("Session %s ended", sessionID)
}
