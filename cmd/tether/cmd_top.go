package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/internal/protocol"
)

// defaultTopInterval matches a comfortable "glance and go" refresh rate --
// fast enough to feel live, slow enough not to spam the daemon.
const defaultTopInterval = 2 * time.Second

const topLong = `Watch the fleet view, refreshing like ` + "`top`" + ` or ` + "`watch tether ls`" + `.

Renders exactly what ` + "`tether ls`" + ` renders, on a timer, until you press Ctrl-C.
Presence is screenshottable this way; messages are not shown here -- read
those with ` + "`tether inbox`" + `.`

type topOptions struct {
	identityFlags
	all      bool
	interval time.Duration
}

func newTopCmd() *cobra.Command {
	var opts topOptions

	cmd := &cobra.Command{
		Use:   "top",
		Short: "Watch the fleet view, refreshing on a timer",
		Long:  topLong,
		Example: "  tether top\n" +
			"  tether top --interval 5s\n" +
			"  tether top --all",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTop(cmd, &opts)
		},
	}

	opts.addWorkspace(cmd)
	cmd.Flags().BoolVar(&opts.all, "all", false, "watch agents in every workspace, not just this one")
	cmd.Flags().DurationVar(&opts.interval, "interval", defaultTopInterval, "refresh interval, as a Go duration")

	return quiet(cmd)
}

func runTop(cmd *cobra.Command, opts *topOptions) error {
	if opts.interval <= 0 {
		return failf(exitGeneral, "--interval must be greater than zero, got %s", opts.interval)
	}

	workspace, err := fleetWorkspace(opts.workspace, opts.all)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	out := cmd.OutOrStdout()
	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()

	for {
		if err := renderTopFrame(out, workspace, opts.interval); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// renderTopFrame fetches a fresh fleet snapshot and redraws the screen with
// it, reusing ls's own table renderer so the two commands never drift apart.
func renderTopFrame(out io.Writer, workspace string, interval time.Duration) error {
	var res protocol.LsResult
	if err := call(protocol.MethodLs, protocol.LsParams{Workspace: workspace}, &res); err != nil {
		return err
	}

	// "\x1b[H\x1b[2J": home the cursor, then clear the screen -- the same
	// escape pair `clear` emits, so it works in any ANSI terminal.
	if _, err := fmt.Fprint(out, "\x1b[H\x1b[2J"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "tether top -- every %s, Ctrl-C to quit -- %s\n\n",
		interval, time.Now().Format(time.Kitchen)); err != nil {
		return err
	}

	if len(res.Agents) == 0 {
		return empty(out, "agents", "tether register --as <name>")
	}
	if err := aggregate(out, fleetSummaryParts(res.Agents)...); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	return writeAgentTable(out, res.Agents)
}
