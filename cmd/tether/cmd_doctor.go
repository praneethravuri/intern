package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/praneethravuri/tether/internal/protocol"
)

const doctorLong = `Check that tether is actually working, and say so plainly when it is not.

doctor reports the socket it would talk to, whether the daemon is answering,
which workspace this directory resolves to, which harness it detects, and
every agent registered here.

Nothing currently pushes a notification when mail arrives, so every agent
sees a message only if it polls with ` + "`tether inbox`" + ` or blocks on
` + "`tether wait`" + `.

Exits 3 when no daemon is reachable.`

// doctorReport is the --json shape of the doctor command.
type doctorReport struct {
	DaemonRunning bool                 `json:"daemon_running"`
	Socket        string               `json:"socket"`
	Workspace     string               `json:"workspace"`
	Cwd           string               `json:"cwd"`
	Harness       string               `json:"harness"`
	SessionID     string               `json:"session_id,omitempty"`
	DBPath        string               `json:"db_path,omitempty"`
	DBSizeBytes   int64                `json:"db_size_bytes,omitempty"`
	Agents        []protocol.AgentView `json:"agents"`
	DaemonLogPath string               `json:"daemon_log_path,omitempty"`
	Warnings      []string             `json:"warnings"`
	Error         string               `json:"error,omitempty"`
}

func newDoctorCmd() *cobra.Command {
	var opts identityFlags

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the daemon, this workspace, and every agent here",
		Long:  doctorLong,
		Example: "  tether doctor\n" +
			"  tether doctor --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd, &opts)
		},
	}

	opts.addWorkspace(cmd)
	opts.addJSON(cmd)

	return quiet(cmd)
}

func runDoctor(cmd *cobra.Command, opts *identityFlags) error {
	report := collectDoctorReport(opts.workspace)

	out := cmd.OutOrStdout()
	if opts.jsonOut {
		if err := printJSON(out, report); err != nil {
			return err
		}
	} else if err := writeDoctorReport(cmd, report); err != nil {
		return err
	}

	if !report.DaemonRunning {
		return silentExit(exitNoDaemon)
	}
	return nil
}

// collectDoctorReport never fails; anything undeterminable is recorded in the report.
func collectDoctorReport(workspaceFlag string) doctorReport {
	report := doctorReport{
		Agents:   []protocol.AgentView{},
		Warnings: []string{},
	}

	if sock, err := protocol.SocketPath(); err == nil {
		report.Socket = sock
	} else {
		report.Socket = "unknown"
		report.Error = fmt.Sprintf("cannot work out where the tether socket lives: %v", err)
	}

	if cwd, err := os.Getwd(); err == nil {
		report.Cwd = cwd
	}

	if ws, err := resolveWorkspace(workspaceFlag); err == nil {
		report.Workspace = ws
	} else {
		report.Workspace = "unknown"
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("WARNING: cannot resolve the workspace for this directory: %v", err))
	}

	report.Harness, report.SessionID = detectHarness()
	if report.Harness == harnessUnknown {
		report.Warnings = append(report.Warnings,
			"WARNING: this harness was not recognised, so agents registered from here "+
				"show harness \"unknown\".")
	}

	if dbPath, err := protocol.DBPath(); err == nil {
		report.DBPath = dbPath
		if info, err := os.Stat(dbPath); err == nil {
			report.DBSizeBytes = info.Size()
		}
	}
	if logPath, err := protocol.LogPath(); err == nil {
		report.DaemonLogPath = logPath
	}

	var who protocol.WhoResult
	err := doCall(protocol.MethodLs, protocol.WhoParams{Workspace: report.Workspace}, &who,
		defaultCallTimeout, false)
	switch err {
	case nil:
		report.DaemonRunning = true
		if who.Agents != nil {
			report.Agents = who.Agents
		}
	default:
		var ee *exitError
		if errors.As(err, &ee) && ee.code == exitNoDaemon {
			report.DaemonRunning = false
		} else {
			report.DaemonRunning = true // socket answered but didn't like the question
		}
		report.Error = err.Error()
	}

	return report
}

// humanBytes renders a byte count as e.g. "1.2 MB" -- decimal (1000-based)
// units, matching how disk usage is normally reported.
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

// writeDoctorReport renders the report to stdout; warnings go to stderr.
func writeDoctorReport(cmd *cobra.Command, r doctorReport) error {
	out := cmd.OutOrStdout()

	daemon := "reachable"
	if !r.DaemonRunning {
		daemon = "NOT reachable"
	}

	harness := sanitizeTerminal(r.Harness)
	if r.SessionID != "" {
		harness += " (session " + sanitizeTerminal(r.SessionID) + ")"
	}

	pairs := [][2]string{
		{"daemon", daemon},
		{"socket", dash(r.Socket)},
		{"workspace", dash(r.Workspace)},
		{"cwd", dash(r.Cwd)},
		{"harness", dash(harness)},
	}
	if r.DBPath != "" {
		pairs = append(pairs, [2]string{"database", fmt.Sprintf("%s (%s)", r.DBPath, humanBytes(r.DBSizeBytes))})
	}
	if r.DaemonLogPath != "" {
		pairs = append(pairs, [2]string{"daemon log", r.DaemonLogPath})
	}
	if _, err := fmt.Fprintln(out, "tether doctor"); err != nil {
		return err
	}
	if err := keyValues(out, pairs); err != nil {
		return err
	}

	if !r.DaemonRunning {
		_, err := fmt.Fprintln(out, "\nno daemon running — start it with `tether`")
		return err
	}

	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if len(r.Agents) == 0 {
		if _, err := fmt.Fprintf(out,
			"no agents in %s — register one with `tether register --as <name>`\n",
			r.Workspace); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(out, "%s in %s\n\n",
			plural(len(r.Agents), "agent", "agents"), r.Workspace); err != nil {
			return err
		}
		if err := writeAgentTable(out, r.Agents); err != nil {
			return err
		}
	}

	errOut := cmd.ErrOrStderr()
	for _, w := range r.Warnings {
		if _, err := fmt.Fprintln(errOut, w); err != nil {
			return err
		}
	}

	if r.Error != "" {
		if _, err := fmt.Fprintf(out, "\nERROR: %s\n", sanitizeTerminal(r.Error)); err != nil {
			return err
		}
	}

	return nil
}
