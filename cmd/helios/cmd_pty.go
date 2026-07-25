package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/praneethravuri/helios/pkg/protocol"
	"github.com/praneethravuri/helios/pkg/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// dialDaemon connects to heliosd's Unix socket -- unchanged from before the
// cobra migration; this is the PTY side, which stays macOS/Linux-only (PTYs and
// Unix sockets aren't available on Windows). The mailbox commands in
// cmd_mailbox.go use a separate TCP connection instead, so they work anywhere.
func dialDaemon() (net.Conn, error) {
	log.Info("Connecting to heliosd...")
	return net.Dial("unix", protocol.SocketPath)
}

var runCmd = &cobra.Command{
	Use:   "run <command> | run <session-id> <command>",
	Short: "Run a command in a managed PTY session",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := dialDaemon()
		if err != nil {
			return fmt.Errorf("failed to connect to daemon: %w", err)
		}
		defer conn.Close()

		var sessionID, commandToRun string
		if len(args) == 1 {
			commandToRun = args[0]
			sessionID = fmt.Sprintf("%s-%d", commandToRun, time.Now().Unix()%1000)
		} else {
			sessionID, commandToRun = args[0], args[1]
		}

		if !validSessionID(sessionID) {
			return fmt.Errorf("invalid session id %q: must be non-empty with no whitespace", sessionID)
		}

		log.Infof("Starting interactive session: %s (spawning: %s)", sessionID, commandToRun)

		handshake := fmt.Sprintf("SPAWN %s %s\n", sessionID, commandToRun)
		if _, err := conn.Write([]byte(handshake)); err != nil {
			return fmt.Errorf("failed to send spawn handshake: %w", err)
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "helios: session %q (use: helios broadcast %q \"<msg>\")\n", sessionID, sessionID)

		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("failed to set raw mode: %w", err)
		}
		defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

		runInteractive(conn, os.Stdin, os.Stdout)
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active terminal sessions",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := dialDaemon()
		if err != nil {
			return fmt.Errorf("failed to connect to daemon: %w", err)
		}
		defer conn.Close()

		if _, err := conn.Write([]byte("LIST\n")); err != nil {
			return fmt.Errorf("failed to send list handshake: %w", err)
		}
		_, err = io.Copy(cmd.OutOrStdout(), conn)
		return err
	},
}

var broadcastCmd = &cobra.Command{
	Use:   "broadcast \"<message>\" | broadcast <session-id> \"<message>\"",
	Short: "Send a message to one session, or every active session",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		target, message, ok := parseBroadcast(args)
		if !ok {
			return cmd.Usage()
		}

		conn, err := dialDaemon()
		if err != nil {
			return fmt.Errorf("failed to connect to daemon: %w", err)
		}
		defer conn.Close()

		log.Infof("Broadcasting command to %q: %q", target, message)
		handshake, err := protocol.FormatBroadcast(target, message)
		if err != nil {
			return fmt.Errorf("invalid broadcast: %w", err)
		}
		if _, err := conn.Write([]byte(handshake)); err != nil {
			return fmt.Errorf("failed to send broadcast handshake: %w", err)
		}
		_, err = io.Copy(cmd.OutOrStdout(), conn)
		return err
	},
}

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Open the interactive TUI (session list + broadcast composer)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		m := ui.New()
		p := tea.NewProgram(m)
		_, err := p.Run()
		return err
	},
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
