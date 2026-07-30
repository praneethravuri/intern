package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/praneethravuri/tether/pkg/protocol"
)

func TestDoctorWithAHealthyDaemon(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-123")

	d := newFakeDaemon(t, okHandler(agents()))

	r := mustRun(t, newDoctorCmd(), "")

	reqs := d.requests()
	if len(reqs) != 1 {
		t.Fatalf("daemon received %d requests, want 1 (ls): %+v", len(reqs), reqs)
	}
	params := decodeParams[protocol.WhoParams](t, reqs[0])
	if params.Workspace != "storefront" {
		t.Fatalf("workspace = %q, want storefront", params.Workspace)
	}

	out := r.stdout
	requireContains(t, out, "reachable", "stdout")
	requireContains(t, out, os.Getenv("TETHER_SOCK"), "stdout")
	requireContains(t, out, "storefront", "stdout")
	requireContains(t, out, "claude-code", "stdout")
	requireContains(t, out, "sess-123", "stdout")
	requireContains(t, out, "frontend@storefront", "stdout")
	requireContains(t, out, "legacy@storefront", "stdout")

	if got := r.exitCode(); got != exitOK {
		t.Fatalf("exit code = %d, want 0", got)
	}
}

// No warning when every agent can actually be woken.
func TestDoctorIsQuietWhenEveryAgentCanBeWoken(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)
	t.Setenv("CLAUDECODE", "1")

	res := agents()
	res.Agents = res.Agents[:1]
	newFakeDaemon(t, okHandler(res))

	r := mustRun(t, newDoctorCmd(), "")
	requireNotContains(t, r.stdout, "WARNING", "stdout")
	requireNotContains(t, r.stderr, "WARNING", "stderr")
}

// An unrecognised harness is itself worth a warning: anything registered from
// here will be unwakeable. The warning is a secondary observation, not the
// report doctor was asked for, so it lands on stderr, not stdout.
func TestDoctorWarnsAboutAnUnknownHarness(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)
	newFakeDaemon(t, okHandler(protocol.WhoResult{}))

	r := mustRun(t, newDoctorCmd(), "")
	requireContains(t, r.stderr, "not recognised", "stderr")
	requireNotContains(t, r.stdout, "not recognised", "stdout")
	requireContains(t, r.stdout, "no agents in storefront", "stdout")
}

func TestDoctorWithoutADaemonDegradesAndExitsThree(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)
	noDaemon(t)

	r := run(t, newDoctorCmd(), "")

	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}

	// It still says everything it can work out locally.
	out := r.stdout
	requireContains(t, out, "NOT reachable", "stdout")
	requireContains(t, out, os.Getenv("TETHER_SOCK"), "stdout")
	requireContains(t, out, "storefront", "stdout")
	requireContains(t, out, "no daemon running", "stdout")
	requireContains(t, out, "start it with `tether`", "stdout")

	// The diagnosis is the output, so there is nothing extra to print.
	if msg := errorMessage(r.err); msg != "" {
		t.Fatalf("doctor would also print %q to stderr; the report is enough", msg)
	}
}

// A socket file with nobody behind it (e.g. a crashed daemon) must not be
// diagnosed via os.Stat on the socket path -- doctor dials instead and
// reports the same "not reachable" answer it would for a socket path that
// was never created at all.
func TestDoctorSpotsAStaleSocket(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)

	dir := t.TempDir()
	sock := dir + "/sock"
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("TETHER_SOCK", sock)

	r := run(t, newDoctorCmd(), "")
	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
	requireContains(t, r.stdout, "no daemon running", "stdout")
}

// TestDoctorReportHasNoSocketExistsField nails down the --json wire contract
// for external consumers not compiled against this repo: doctorReport has no
// socket_exists field, since only an os.Stat doctor deliberately avoids could
// have populated one.
func TestDoctorReportHasNoSocketExistsField(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)
	newFakeDaemon(t, okHandler(agents()))

	r := mustRun(t, newDoctorCmd(), "", "--json")
	requireNotContains(t, r.stdout, "socket_exists", "--json output")
}

// TestDoctorReportsDatabaseHealth proves doctor's database line covers what
// "is the DB getting too big" actually needs: path and size -- answered with
// a real, on-disk database via TETHER_DB, not the fake daemon's canned agent
// list.
func TestDoctorReportsDatabaseHealth(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)

	dbPath := filepath.Join(t.TempDir(), "tether.db")
	if err := os.WriteFile(dbPath, []byte("not a real db, just needs a size"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("TETHER_DB", dbPath)

	newFakeDaemon(t, okHandler(agents()))

	r := mustRun(t, newDoctorCmd(), "", "--json")

	var got doctorReport
	unmarshalJSON(t, r.stdout, &got)

	if got.DBPath != dbPath {
		t.Fatalf("db_path = %q, want %q", got.DBPath, dbPath)
	}
	if got.DBSizeBytes <= 0 {
		t.Fatalf("db_size_bytes = %d, want > 0", got.DBSizeBytes)
	}
	if got.DaemonLogPath == "" {
		t.Fatal("daemon_log_path is empty")
	}

	human := mustRun(t, newDoctorCmd(), "").stdout
	requireContains(t, human, dbPath, "human output")
	requireContains(t, human, got.DaemonLogPath, "human output")
}

func TestDoctorJSON(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-123")

	newFakeDaemon(t, okHandler(agents()))

	r := mustRun(t, newDoctorCmd(), "", "--json")

	var got doctorReport
	unmarshalJSON(t, r.stdout, &got)

	if !got.DaemonRunning {
		t.Fatal("daemon_running = false, want true")
	}
	if got.Workspace != "storefront" {
		t.Fatalf("workspace = %q, want storefront", got.Workspace)
	}
	if got.Harness != harnessClaudeCode || got.SessionID != "sess-123" {
		t.Fatalf("harness = %q session = %q", got.Harness, got.SessionID)
	}
	if len(got.Agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(got.Agents))
	}
	// A recognised harness and no resolution failures: nothing to warn about.
	if len(got.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", got.Warnings)
	}
}

func TestDoctorJSONWithoutADaemon(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)
	noDaemon(t)

	r := run(t, newDoctorCmd(), "", "--json")

	var got doctorReport
	unmarshalJSON(t, r.stdout, &got)

	if got.DaemonRunning {
		t.Fatal("daemon_running = true, want false")
	}
	if got.Agents == nil {
		t.Fatal("agents = null, want an empty array")
	}
	requireContains(t, got.Error, "no daemon running", "error field")
	if code := r.exitCode(); code != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", code, exitNoDaemon)
	}
}

// A daemon that answers with nonsense is a daemon that is up, and doctor has
// to say both things without crashing.
func TestDoctorSurvivesAMalformedResponse(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	clearHarnessEnv(t)
	newRawDaemon(t, []byte("nope"))

	r := run(t, newDoctorCmd(), "")
	if r.err != nil {
		t.Fatalf("doctor failed instead of reporting: %v", r.err)
	}
	requireContains(t, r.stdout, "ERROR", "stdout")
	requireContains(t, r.stdout, "malformed response", "stdout")
}
