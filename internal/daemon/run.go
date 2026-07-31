// Package daemon runs the tether daemon: the socket listener, SQLite-backed
// message bus, and background sweep that cmd/tether serves in the foreground
// when invoked with no arguments.
package daemon

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/praneethravuri/tether/internal/protocol"
	"github.com/praneethravuri/tether/internal/store"
)

// ErrAlreadyRunning marks every "a daemon already holds this socket" failure
// (lock held or live), so a caller can map it to a conflict rather than a
// general error regardless of which of the two checks caught it.
var ErrAlreadyRunning = errors.New("a daemon is already running")

// Run resolves the socket and database paths, guards against a second
// daemon, and serves until interrupted or terminated.
func Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sockPath, err := protocol.SocketPath()
	if err != nil {
		return err
	}
	dbPath, err := protocol.DBPath()
	if err != nil {
		return err
	}

	// Closes a race daemonIsLive alone can't: Listen unlinks any existing
	// socket, so two racing daemons could both pass the liveness check below
	// before either binds.
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		return err
	}
	release, err := acquireLock(sockPath + ".lock")
	if err != nil {
		return err
	}
	defer release()

	// Second, independent check: catches a live daemon not holding this lock.
	if daemonIsLive(sockPath) {
		return &startupError{"socket " + sockPath + " is already served by a running daemon"}
	}

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	st.Logger = log.Default()
	defer func() {
		if err := st.Close(); err != nil {
			log.Printf("closing store: %v", err)
		}
	}()

	ln, err := protocol.Listen(sockPath)
	if err != nil {
		return err
	}

	log.Printf("listening on %s", sockPath)
	log.Printf("database %s", dbPath)

	err = NewServer(st, DefaultConfig()).Serve(ctx, ln)
	log.Printf("stopped")
	return err
}

// daemonIsLive reports whether something is currently accepting on path. A
// leftover socket file from a crashed daemon refuses connections and is
// treated as free.
func daemonIsLive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// startupError keeps Run's failure messages free of Go error decoration.
type startupError struct{ msg string }

func (e *startupError) Error() string { return e.msg }
func (e *startupError) Unwrap() error { return ErrAlreadyRunning }
