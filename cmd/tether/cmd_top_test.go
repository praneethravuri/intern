package main

import (
	"context"
	"testing"
	"time"

	"github.com/praneethravuri/tether/internal/protocol"
)

func TestTopRendersTheSameTableAsLs(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(agents()))

	cmd := newTopCmd()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)

	r := mustRun(t, cmd, "", "--interval", "10ms")

	requireContains(t, r.stdout, "NAME", "stdout")
	requireContains(t, r.stdout, "frontend@storefront", "stdout")
	requireContains(t, r.stdout, "2 agents", "stdout")
}

// TestTopRefreshesOnEveryTick proves each tick issues a fresh MethodLs call
// rather than rendering one cached snapshot forever.
func TestTopRefreshesOnEveryTick(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(agents()))

	cmd := newTopCmd()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)

	mustRun(t, cmd, "", "--interval", "20ms")

	if n := d.countMethod(protocol.MethodLs); n < 3 {
		t.Fatalf("daemon received %d ls calls, want at least 3 over 150ms at a 20ms interval", n)
	}
}

func TestTopUsesTheWorkspaceFlag(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.LsResult{}))

	cmd := newTopCmd()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)

	mustRun(t, cmd, "", "--interval", "10ms", "--workspace", "warehouse")

	params := decodeParams[protocol.LsParams](t, d.requests()[0])
	if params.Workspace != "warehouse" {
		t.Fatalf("workspace = %q, want warehouse", params.Workspace)
	}
}

func TestTopAllSkipsWorkspaceResolution(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	d := newFakeDaemon(t, okHandler(protocol.LsResult{}))

	cmd := newTopCmd()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)

	mustRun(t, cmd, "", "--interval", "10ms", "--all")

	params := decodeParams[protocol.LsParams](t, d.requests()[0])
	if params.Workspace != "" {
		t.Fatalf("workspace = %q, want empty for --all", params.Workspace)
	}
}

func TestTopRejectsNonPositiveInterval(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	newFakeDaemon(t, okHandler(protocol.LsResult{}))

	r := run(t, newTopCmd(), "", "--interval", "0s")
	if r.err == nil {
		t.Fatal("top accepted a zero interval")
	}
	requireContains(t, r.err.Error(), "--interval", "error")
}

func TestTopWithoutADaemonExitsThree(t *testing.T) {
	setIdentity(t, "frontend", "storefront")
	noDaemon(t)

	r := run(t, newTopCmd(), "", "--interval", "10ms")
	if got := r.exitCode(); got != exitNoDaemon {
		t.Fatalf("exit code = %d, want %d", got, exitNoDaemon)
	}
}
