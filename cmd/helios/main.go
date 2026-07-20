package main

import (
	"github.com/praneethravuri/helios/pkg/logger"
	"net"
)

const socketPath = "/tmp/helios.sock"

func main() {
	log, cleanup := logger.InitLogger()
	defer cleanup()

	log.Info("Connecting to heliosd...")

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		log.Fatalw("Failed to connect to daemon", "error", err)
	}

	defer conn.Close()

	log.Info("Connected! Sending message...")

	message := "Hello from helios client!\n"

	_, err = conn.Write([]byte(message))

	if err != nil {
		log.Errorw("Failed to write message to connection", "error", err)
		return
	}

	log.Info("Message sent successfully. Exiting")
}
