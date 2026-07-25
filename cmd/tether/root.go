package main

import (
	"os"

	"github.com/praneethravuri/tether/pkg/logger"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// log is initialized once in main() before Execute() and used by every
// subcommand's RunE -- same single logger the old flat-switch CLI used.
var log *zap.SugaredLogger

var rootCmd = &cobra.Command{
	Use:   "tether",
	Short: "Run and message coding-agent sessions from any terminal",
}

func init() {
	rootCmd.AddCommand(runCmd, listCmd, broadcastCmd, uiCmd)
	rootCmd.AddCommand(registerCmd, sendCmd, inboxCmd, whoCmd)
}

func main() {
	var cleanup func()
	log, cleanup = logger.InitLogger()
	defer cleanup()

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
