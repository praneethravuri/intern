// Command tetherd is the tether daemon: a local message bus for coding agents.
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/praneethravuri/tether/internal/store"
	"github.com/praneethravuri/tether/pkg/protocol"
)

// version is overridden at build time the same way as cmd/tether's, via
// -ldflags "-X main.version=...". Logged at startup since tetherd has no
// subcommands to print it on request.
var version = "dev"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("tetherd: ")

	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run() error {
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
	// socket, so two racing tetherd processes could both pass the liveness
	// check below before either binds.
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		return err
	}
	release, err := acquireLock(sockPath + ".lock")
	if err != nil {
		return err
	}
	defer release()

	// Second, independent check: catches a live tetherd not holding this lock.
	if daemonIsLive(sockPath) {
		return &startupError{"socket " + sockPath + " is already served by a running tetherd"}
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

	log.Printf("tetherd %s", version)
	log.Printf("listening on %s", sockPath)
	log.Printf("database %s", dbPath)

	err = NewServer(st, DefaultConfig()).Serve(ctx, ln)
	log.Printf("tetherd stopped")
	return err
}

// daemonIsLive reports whether something is currently accepting on path. A
// leftover socket file from a crashed daemon refuses connections and is treated
// as free.
func daemonIsLive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// startupError keeps main's failure messages free of Go error decoration.
type startupError struct{ msg string }

func (e *startupError) Error() string { return e.msg }
