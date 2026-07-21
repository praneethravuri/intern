package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/praneethravuri/helios/pkg/logger"
	"github.com/praneethravuri/helios/pkg/protocol"
	"go.uber.org/zap"
)

// SessionManager manages active PTY sessions in a thread-safe way.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]io.Writer
}

// NewSessionManager creates and initializes a new SessionManager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]io.Writer),
	}
}

// Register adds a PTY writer to our active sessions map, returning an error if the id is already active.
// ponytail: %1000 default id is collision-prone; explicit `helios run <id> claude` is the escape hatch
func (sm *SessionManager) Register(id string, writer io.Writer) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if _, exists := sm.sessions[id]; exists {
		return fmt.Errorf("session %q already active", id)
	}
	sm.sessions[id] = writer
	return nil
}

// Deregister removes a PTY writer from our active sessions map.
func (sm *SessionManager) Deregister(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, id)
}

// Broadcast sends a message to sessions matching target, returning matched and delivered counts.
// ponytail: snapshot under lock, write unlocked -- no PTY write stalls the registry
func (sm *SessionManager) Broadcast(msg, target string, log *zap.SugaredLogger) (delivered, matched int) {
	sm.mu.Lock()
	// matchedSession is a snapshot pair, distinct from the package-level session type
	// (that one owns a PTY/process lifecycle; this one is just an id+writer match).
	type matchedSession struct {
		id     string
		writer io.Writer
	}
	var targets []matchedSession
	if target == protocol.BroadcastAll {
		targets = make([]matchedSession, 0, len(sm.sessions))
		for id, writer := range sm.sessions {
			targets = append(targets, matchedSession{id, writer})
		}
	} else if writer, ok := sm.sessions[target]; ok {
		targets = []matchedSession{{target, writer}}
	}
	sm.mu.Unlock()

	matched = len(targets)
	log.Infof("Broadcasting to %d matched session(s): %q", matched, msg)

	const writeTimeout = 2 * time.Second

	for _, t := range targets {
		log.Infof("Sending command to session: %s", t.id)
		result := make(chan error, 1)
		go func(w io.Writer) {
			_, err := w.Write([]byte(msg))
			result <- err
		}(t.writer)

		select {
		case err := <-result:
			if err != nil {
				log.Errorw("Failed to write to session", "sessionID", t.id, "error", err)
				continue
			}
			delivered++
		case <-time.After(writeTimeout):
			// ponytail: the write goroutine leaks until the underlying PTY write finally
			// unblocks or the session closes -- PTY writes can't be cancelled from here.
			// Upgrade to context-cancellable writes if wedged sessions become common.
			log.Errorw("Timed out writing to session", "sessionID", t.id, "timeout", writeTimeout)
		}
	}
	return delivered, matched
}

// List returns a slice of all active session IDs.
func (sm *SessionManager) List() []string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ids := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		ids = append(ids, id)
	}
	return ids
}

func main() {
	log, cleanup := logger.InitLogger()
	defer cleanup()

	log.Info("Starting heliosd...")

	// Clean up old socket files
	if err := os.RemoveAll(protocol.SocketPath); err != nil {
		log.Fatalw("Failed to remove old socket file", "error", err)
	}

	listener, err := net.Listen("unix", protocol.SocketPath)
	if err != nil {
		log.Fatalw("Failed to start Unix socket listener", "error", err)
	}
	defer listener.Close()

	log.Infof("Listening on unix socket: %s", protocol.SocketPath)

	// Initialize our session manager
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

	// 1. Read the handshake line from the client
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.Errorw("Failed to read handshake from connection", "error", err)
		return
	}

	command := strings.TrimSpace(line)
	log.Infof("Received handshake: %q", command)

	// 2. Parse and handle the command
	if sessionIDAndCmd, found := strings.CutPrefix(command, protocol.VerbSpawn+" "); found {
		if sessionIDAndCmd == "" {
			log.Warn("Received empty SPAWN command")
			return
		}

		// Split the session ID and the command (e.g. "session-a claude" -> ["session-a", "claude"])
		parts := strings.SplitN(sessionIDAndCmd, " ", 2)
		sessionID := parts[0]
		cmdName := "claude" // Default command fallback
		if len(parts) > 1 {
			cmdName = parts[1]
		}

		log.Infof("Spawning PTY sessions: %s (command: %s)", sessionID, cmdName)
		runSession(conn, sessionID, cmdName, sm, log)

	} else if strings.HasPrefix(command, protocol.VerbBroadcast+" ") {
		target, msg, ok := protocol.ParseBroadcast(command)
		if !ok {
			log.Warnf("Malformed BROADCAST command: %q", command)
			_, _ = conn.Write([]byte(fmt.Sprintf("\nError: malformed BROADCAST command: %q\n", command)))
			return
		}
		if !strings.HasSuffix(msg, "\n") {
			msg = msg + "\n"
		}

		delivered, matched := sm.Broadcast(msg, target, log)
		log.Infow("Broadcast completed", "target", target, "delivered", delivered, "matched", matched)

		var response string
		switch {
		case matched == 0 && target != protocol.BroadcastAll:
			response = fmt.Sprintf("Session %q not found.\n", target)
		case matched == 0:
			response = "No active sessions.\n"
		default:
			response = fmt.Sprintf("Delivered to %d of %d session(s).\n", delivered, matched)
		}
		_, _ = conn.Write([]byte(response))

	} else if command == protocol.VerbList {
		ids := sm.List()

		var response string
		if len(ids) == 0 {
			response = "No active sessions found.\n"
		} else {
			response = strings.Join(ids, "\n") + "\n"
		}

		_, err = conn.Write([]byte(response))
		if err != nil {
			log.Errorw("Failed to write session list to client", "error", err)
		}
		return // Terminate the connection immediately

	} else {
		log.Warnf("Unknown handshake command: %q", command)
		_, _ = conn.Write([]byte(fmt.Sprintf("Error: unknown command: %q\n", command)))
	}
}

// session owns a spawned child's PTY and process, with one idempotent teardown so the
// disconnect path and the normal-return path don't each duplicate kill/close.
type session struct {
	ptyMaster *os.File
	cmd       *exec.Cmd
	once      sync.Once
}

// Close kills the child process and closes its PTY master. Safe to call more than once
// (e.g. once from the disconnect goroutine, once via defer) -- only the first call acts.
//
// ponytail: closing ptyMaster alone unblocks the pending Read on platforms where the
// master fd is poller-integrated (e.g. Linux), but not on Darwin, where creack/pty opens
// it in blocking mode outside the runtime poller (verified via repro). Killing the child
// releases the pty slave, which reliably unblocks the master Read (EOF) on every platform.
func (s *session) Close() {
	s.once.Do(func() {
		_ = s.cmd.Process.Kill()
		_ = s.ptyMaster.Close()
	})
}

func runSession(conn net.Conn, sessionID string, cmdName string, sm *SessionManager, log *zap.SugaredLogger) {
	// Resolve the executable command path
	cmd, err := resolveCommand(cmdName, log)
	if err != nil {
		// Write the error back to the client socket so it prints in their terminal
		errorMessage := fmt.Sprintf("\nError: %v\n", err)
		_, _ = conn.Write([]byte(errorMessage))
		log.Errorw("Failed to resolve command", "sessionID", sessionID, "error", err)
		return
	}

	// Spawn the resolved command in the PTY
	ptyMaster, err := pty.Start(cmd)
	if err != nil {
		errorMessage := fmt.Sprintf("\nError: failed to start process: %v\n", err)
		_, _ = conn.Write([]byte(errorMessage))
		log.Errorw("Failed to start shell in PTY", "sessionID", sessionID, "error", err)
		return
	}
	sess := &session{ptyMaster: ptyMaster, cmd: cmd}
	defer sess.Close()

	if err := sm.Register(sessionID, ptyMaster); err != nil {
		errorMessage := fmt.Sprintf("\nError: %v\n", err)
		_, _ = conn.Write([]byte(errorMessage))
		log.Errorw("Failed to register session", "sessionID", sessionID, "error", err)
		sess.Close()
		return
	}
	defer sm.Deregister(sessionID)

	log.Infof("Session %s registered and active", sessionID)

	// Two-way stream between PTY and socket
	go func() {
		_, _ = io.Copy(ptyMaster, conn)
		// client disconnected; unblock the pty->client read below so cleanup runs.
		sess.Close()
	}()

	_, _ = io.Copy(conn, ptyMaster)

	log.Infof("Session %s ended", sessionID)
}

// resolveCommand looks up the path of the command or falls back to npx for claude
func resolveCommand(cmdName string, log *zap.SugaredLogger) (*exec.Cmd, error) {
	if cmdName == "claude" {
		// 1. Try to find local 'claude' command on PATH
		if path, err := exec.LookPath("claude"); err == nil {
			log.Infof("Found 'claude' binary at: %s", path)
			return exec.Command(path), nil
		}
		// 2. Fall back to npx if available
		if path, err := exec.LookPath("npx"); err == nil {
			log.Info("'claude' binary not found on PATH. Falling back to 'npx @anthropic-ai/claude-code'")
			return exec.Command(path, "@anthropic-ai/claude-code"), nil
		}
		// Neither is available
		return nil, fmt.Errorf("neither 'claude' nor 'npx' (Node Package Runner) was found on the system PATH")
	}

	// For other commands (e.g. zsh, bash), look them up on PATH
	path, err := exec.LookPath(cmdName)
	if err != nil {
		return nil, fmt.Errorf("command %q not found on the system PATH: %w", cmdName, err)
	}

	return exec.Command(path), nil
}
