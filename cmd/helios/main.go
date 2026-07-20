package main

import (
	"github.com/praneethravuri/helios/pkg/logger"
	"golang.org/x/term"
	"io"
	"net"
	"os"
)

const socketPath = "/tmp/helios.sock"

func main() {
	log, cleanup := logger.InitLogger()
	defer cleanup()

	log.Info("Connecting to heliosd...")

	// connect to unix socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		log.Fatalw("Failed to connect to daemon", "error", err)
	}

	defer conn.Close()

	log.Info("Connected! Switching terminal to raw mode...")

	// switch standard input into raw mode. makes sure keystrokes are sent instantly and special keybinds are not caught locally
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalw("Failed to set raw mode", "error", err)
	}

	// restore the terminal state to normal when the program exits
	defer func() {
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}()

	// pipe socket incoming bytes to our local screen
	go func() {
		_, _ = io.Copy(os.Stdout, conn)
	}()

	// pipe keyboard inputs to the socket connection
	_, _ = io.Copy(conn, os.Stdin)
}
