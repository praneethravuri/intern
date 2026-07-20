package main

import (
	"github.com/creack/pty"
	"github.com/praneethravuri/helios/pkg/logger"
	"go.uber.org/zap"
	"io"
	"net"
	"os"
	"os/exec"
)

const socketPath = "/tmp/helios.sock"

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

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Warnw("Failed to accept client connection", "error", err)
			continue
		}

		log.Info("New client connected")

		go handleConnection(conn, log)
	}
}

func handleConnection(conn net.Conn, log *zap.SugaredLogger) {
	defer conn.Close()

	// prepare the process command (spawn a zsh shell on mac)
	cmd := exec.Command("/bin/zsh")

	// spawn the shell inside PTY. it acts as both a reader and writer for the shell's input/output
	ptyMaster, err := pty.Start(cmd)

	if err != nil {
		log.Errorw("Failed to start shell in PTY", "error", err)
		return
	}

	defer ptyMaster.Close()

	// ensure the spawned process is killed if the client drops the connection
	defer func() {
		_ = cmd.Process.Kill()
	}()

	// two way streaming. pip socket client input -> pty shell input
	go func() {
		_, _ = io.Copy(ptyMaster, conn)
	}()

	// copy PTY shell output -> socket client output
	_, _ = io.Copy(conn, ptyMaster)

	log.Info("Shell session ended")
}
