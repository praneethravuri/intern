package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// Guards help text against advertising features the store no longer has:
// notifier/tier columns are dropped in migrateV1ToV2, and nothing reads
// $TETHER_NAME.
func TestHelpDoesNotAdvertiseRemovedFeatures(t *testing.T) {
	cases := []struct {
		name     string
		newCmd   func() *cobra.Command
		unwanted string
	}{
		// doctor's Short only surfaces in the parent listing, not its own --help.
		{"doctor wake tier", newRootCmd, "wake tier"},
		{"explain TETHER_NAME", newExplainCmd, "TETHER_NAME"},
		{"wait universal tier", newWaitCmd, "universal"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := mustRun(t, c.newCmd(), "", "--help")
			requireNotContains(t, r.stdout, c.unwanted, "help output")
		})
	}
}
