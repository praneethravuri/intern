package main

import (
	"io"
	"net"
	"os"

	"github.com/praneethravuri/helios/pkg/logger"
	"go.uber.org/zap"
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

	log.Info("Handling client request...")

	if _, err := io.Copy(os.Stdout, conn); err != nil {
		log.Errorw("Error copying data from client", "error", err)
	}

	log.Info("Client disconnected")
}
