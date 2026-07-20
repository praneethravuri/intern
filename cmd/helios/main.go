package main

import (
	"fmt"
	"github.com/praneethravuri/helios/pkg/logger"
	"golang.org/x/term"
	"io"
	"net"
	"os"
	"strings"
)

const socketPath = "/tmp/helios.sock"

func main() {
	if len(os.Args) < 3 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	arg := os.Args[2]

	log, cleanup := logger.InitLogger()
	defer cleanup()

	log.Info("Connecting to heliosd...")
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		log.Fatalw("Failed to connect to daemon", "error", err)
	}
	defer conn.Close()

	switch command {
	case "run":
		sessionID := arg
		log.Infof("Starting interactive session: %s", sessionID)

		// spawn handshake command to the daemon
		handshake := fmt.Sprintf("SPAWN %s\n", sessionID)
		_, err := conn.Write([]byte(handshake))
		if err != nil {
			log.Fatalw("Failed to send spawn handshake", "error", err)
		}

		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			log.Fatalw("Failed to set raw mode", "error", err)
		}

		defer func() {
			_ = term.Restore(int(os.Stdin.Fd()), oldState)
		}()

		go func() {
			_, _ = io.Copy(os.Stdout, conn)
		}()

		_, _ = io.Copy(conn, os.Stdin)

	case "broadcast":
		message := arg
		if !strings.HasSuffix(message, "\n") {
			message = message + "\n"
		}
		log.Infof("Broadcasting command: %q", message)

		handshake := fmt.Sprintf("BROADCAST %s", message)
		_, err = conn.Write([]byte(handshake))
		if err != nil {
			log.Fatalw("Failed to send broadcast handshake", "error", err)
		}

		log.Info("Broadcast command sent successfully")

	default:
		printUsage()
		os.Exit(1)
	}

}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  helios run <session-id>        - Starts an interactive terminal session")
	fmt.Println("  helios broadcast \"<message>\"   - Broadcasts a command to all active terminal sessions")
}
