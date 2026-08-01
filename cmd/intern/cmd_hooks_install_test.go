package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/praneethravuri/intern/internal/hooks/claudecode"
)

// chdir switches the process cwd for the duration of one test and restores
// it after -- install's default target is resolved relative to cwd, the
// same way Claude Code resolves its own project-level settings.json.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestHooksInstallDefaultsToProjectSettings(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	r := mustRun(t, newHooksInstallCmd(), "")

	requireContains(t, r.stdout, "installed the Stop and SessionStart hooks", "stdout")
	want := filepath.Join(dir, ".claude", "settings.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected %s to exist: %v", want, err)
	}
}

func TestHooksInstallUserFlagTargetsHomeClaudeSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	mustRun(t, newHooksInstallCmd(), "", "--user")

	want := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected %s to exist: %v", want, err)
	}
}

func TestHooksInstallSecondRunReportsNoChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	mustRun(t, newHooksInstallCmd(), "", "--user")
	r := mustRun(t, newHooksInstallCmd(), "", "--user")

	requireContains(t, r.stdout, "already has intern's hooks up to date", "stdout")
}

func TestHooksInstallJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	r := mustRun(t, newHooksInstallCmd(), "", "--user", "--json")

	var res claudecode.InstallResult
	unmarshalJSON(t, r.stdout, &res)
	if !res.Changed {
		t.Fatalf("Changed = false, want true on first install")
	}
	if res.Path != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("Path = %q, want the user settings file", res.Path)
	}
}

func TestHooksStatusBeforeAndAfterInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	before := mustRun(t, newHooksStatusCmd(), "", "--user")
	requireContains(t, before.stdout, "not installed", "status before install")
	requireContains(t, before.stdout, "intern hooks install", "status before install")

	mustRun(t, newHooksInstallCmd(), "", "--user")

	after := mustRun(t, newHooksStatusCmd(), "", "--user")
	requireNotContains(t, after.stdout, "not installed", "status after install")
	requireContains(t, after.stdout, "installed", "status after install")
}

func TestHooksStatusJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustRun(t, newHooksInstallCmd(), "", "--user")

	r := mustRun(t, newHooksStatusCmd(), "", "--user", "--json")

	var st claudecode.Status
	unmarshalJSON(t, r.stdout, &st)
	if !st.StopInstalled || !st.StopCurrent || !st.SessionStartInstalled || !st.SessionStartCurrent {
		t.Fatalf("status = %+v, want everything installed and current", st)
	}
}
