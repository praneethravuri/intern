package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/praneethravuri/helios/pkg/logger"
	"github.com/praneethravuri/helios/pkg/protocol"
	"github.com/praneethravuri/helios/pkg/ui"
	"golang.org/x/term"
)

func main() {
	// 1. Ensure at least the subcommand is provided (e.g. helios list)
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	log, cleanup := logger.InitLogger()
	defer cleanup()

	// 3. Handle subcommands
	switch command {
	case "ui":
		m := ui.New()
		p := tea.NewProgram(m)
		if _, err := p.Run(); err != nil {
			log.Fatalw("TUI failed", "error", err)
		}
		return
	}

	// 2. Connect to the background daemon (for non-ui commands)
	log.Info("Connecting to heliosd...")
	conn, err := net.Dial("unix", protocol.SocketPath)
	if err != nil {
		log.Fatalw("Failed to connect to daemon", "error", err)
	}
	defer conn.Close()

	// 3. Handle other subcommands
	switch command {
	case "list":
		log.Info("Requesting active session list...")
		// Send the LIST handshake
		_, err := conn.Write([]byte("LIST\n"))
		if err != nil {
			log.Fatalw("Failed to send list handshake", "error", err)
		}

		// Read the list response and output it directly to the console
		_, err = io.Copy(os.Stdout, conn)
		if err != nil {
			log.Errorw("Failed to read list response", "error", err)
		}

	case "run":
		// Ensure we have at least one argument after 'run' (e.g. helios run claude)
		if len(os.Args) < 3 {
			printUsage()
			os.Exit(1)
		}

		var sessionID string
		var commandToRun string

		// If only 1 argument is passed after 'run' (e.g., helios run claude)
		if len(os.Args) == 3 {
			commandToRun = os.Args[2]
			// Auto-generate session ID using timestamp (e.g., claude-492)
			sessionID = fmt.Sprintf("%s-%d", commandToRun, time.Now().Unix()%1000)
		} else {
			// If 2 arguments are passed after 'run' (e.g., helios run my-session zsh)
			sessionID = os.Args[2]
			commandToRun = os.Args[3]
		}

		if !validSessionID(sessionID) {
			fmt.Fprintf(os.Stderr, "helios: invalid session id %q: must be non-empty with no whitespace\n", sessionID)
			os.Exit(1)
		}

		log.Infof("Starting interactive session: %s (spawning: %s)", sessionID, commandToRun)

		// Send SPAWN handshake command to the daemon: "SPAWN <sessionID> <command>\n"
		handshake := fmt.Sprintf("SPAWN %s %s\n", sessionID, commandToRun)
		_, err = conn.Write([]byte(handshake))
		if err != nil {
			log.Fatalw("Failed to send spawn handshake", "error", err)
		}

		// Announce the session id on stderr (stdout is reserved for relayed PTY output)
		fmt.Fprintf(os.Stderr, "helios: session %q (use: helios broadcast %q \"<msg>\")\n", sessionID, sessionID)

		// Switch your local terminal to raw mode
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			log.Fatalw("Failed to set raw mode", "error", err)
		}
		defer func() {
			_ = term.Restore(int(os.Stdin.Fd()), oldState)
		}()

		// Two-way interactive pipe; returns once the daemon ends the session.
		runInteractive(conn, os.Stdin, os.Stdout)

	case "broadcast":
		target, message, ok := parseBroadcast(os.Args[2:])
		if !ok {
			printUsage()
			os.Exit(1)
		}

		log.Infof("Broadcasting command to %q: %q", target, message)

		// Send BROADCAST handshake command to the daemon
		handshake, err := protocol.FormatBroadcast(target, message)
		if err != nil {
			log.Fatalw("Invalid broadcast", "error", err)
		}
		_, err = conn.Write([]byte(handshake))
		if err != nil {
			log.Fatalw("Failed to send broadcast handshake", "error", err)
		}

		// Read the daemon's reply and print it directly to the console
		_, err = io.Copy(os.Stdout, conn)
		if err != nil {
			log.Errorw("Failed to read broadcast response", "error", err)
		}

	default:
		printUsage()
		os.Exit(1)
	}
}

// runInteractive pipes stdin to conn and conn to stdout concurrently, returning
// as soon as the daemon closes its side (the conn -> stdout copy hits EOF) --
// rather than staying blocked on a stdin read the user may never satisfy.
func runInteractive(conn net.Conn, stdin io.Reader, stdout io.Writer) {
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(stdout, conn)
		close(done)
	}()
	go func() {
		_, _ = io.Copy(conn, stdin)
	}()
	<-done
}

// validSessionID reports whether id is safe to embed in the space-delimited
// SPAWN/BROADCAST wire framing (see protocol.FormatBroadcast).
func validSessionID(id string) bool {
	return id != "" && !strings.ContainsAny(id, " \t\n")
}

// parseBroadcast extracts target and message from broadcast arguments (all-sessions if 1 arg, single session if 2).
func parseBroadcast(args []string) (target, msg string, ok bool) {
	switch len(args) {
	case 1:
		return protocol.BroadcastAll, args[0], true
	case 2:
		return args[0], args[1], true
	default:
		return "", "", false
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  helios run <command>                        - Runs a command with an auto-generated session ID (e.g. helios run claude)")
	fmt.Println("  helios run <session-id> <command>           - Runs a command with a custom session ID (e.g. helios run session-1 zsh)")
	fmt.Println("  helios list                                 - Lists all active terminal sessions")
	fmt.Println("  helios broadcast \"<message>\"                - Broadcasts a command to all active terminal sessions")
	fmt.Println("  helios broadcast <session-id> \"<message>\"   - Sends a command to a single session")
	fmt.Println("  helios ui                                   - Opens the terminal UI")
}
