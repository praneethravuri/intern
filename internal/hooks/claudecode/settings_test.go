package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func settingsPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".claude", "settings.json")
}

// asMap decodes raw JSON into a generic map for assertions that don't care
// about field ordering.
func asMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw)
	}
	return m
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

func TestInstallCreatesSettingsFileWhenAbsent(t *testing.T) {
	path := settingsPath(t)

	res, err := Install(path, "/usr/local/bin/intern")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Changed {
		t.Fatal("Changed = false, want true on first install")
	}

	st, err := Inspect(path, "/usr/local/bin/intern")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !st.StopInstalled || !st.StopCurrent {
		t.Fatalf("Stop hook status = %+v, want installed and current", st)
	}
	if !st.SessionStartInstalled || !st.SessionStartCurrent {
		t.Fatalf("SessionStart hook status = %+v, want installed and current", st)
	}
}

func TestInstallWritesTheDocumentedHookShape(t *testing.T) {
	path := settingsPath(t)
	if _, err := Install(path, "/usr/local/bin/intern"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	m := asMap(t, readFile(t, path))
	hooks := m["hooks"].(map[string]any)

	stop := hooks["Stop"].([]any)[0].(map[string]any)
	stopHook := stop["hooks"].([]any)[0].(map[string]any)
	if stopHook["command"] != "/usr/local/bin/intern hooks run-stop" {
		t.Fatalf("Stop command = %v, want the run-stop invocation", stopHook["command"])
	}
	if stopHook["type"] != "command" {
		t.Fatalf("Stop type = %v, want %q", stopHook["type"], "command")
	}
	if async, ok := stopHook["async"].(bool); !ok || !async {
		t.Fatalf("Stop async = %v, want true", stopHook["async"])
	}
	if rewake, ok := stopHook["asyncRewake"].(bool); !ok || !rewake {
		t.Fatalf("Stop asyncRewake = %v, want true", stopHook["asyncRewake"])
	}

	start := hooks["SessionStart"].([]any)[0].(map[string]any)
	startHook := start["hooks"].([]any)[0].(map[string]any)
	if startHook["command"] != "/usr/local/bin/intern hooks run-session-start" {
		t.Fatalf("SessionStart command = %v, want the run-session-start invocation", startHook["command"])
	}
	if _, has := startHook["async"]; has {
		t.Fatalf("SessionStart hook set async, want it left at the default (unset)")
	}
}

// TestInstallMergesIntoExistingUnrelatedContent is the load-bearing merge
// test: a settings.json with another tool's hooks and unrelated top-level
// keys must survive install with nothing lost, plus intern's own entries
// added.
func TestInstallMergesIntoExistingUnrelatedContent(t *testing.T) {
	path := settingsPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	existing := `{
  "permissions": {
    "allow": ["Bash(git *)"]
  },
  "env": {
    "SOME_VAR": "1"
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "/usr/bin/lint-guard"}
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "/opt/othertool/notify-stop"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing settings: %v", err)
	}

	res, err := Install(path, "/usr/local/bin/intern")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Changed {
		t.Fatal("Changed = false, want true")
	}

	m := asMap(t, readFile(t, path))

	// Unrelated top-level keys survive untouched.
	perms := m["permissions"].(map[string]any)
	allow := perms["allow"].([]any)
	if len(allow) != 1 || allow[0] != "Bash(git *)" {
		t.Fatalf("permissions.allow = %v, want it untouched", allow)
	}
	env := m["env"].(map[string]any)
	if env["SOME_VAR"] != "1" {
		t.Fatalf("env.SOME_VAR = %v, want untouched", env["SOME_VAR"])
	}

	hooks := m["hooks"].(map[string]any)

	// The other tool's PreToolUse hook is untouched, key and all.
	preToolUse := hooks["PreToolUse"].([]any)
	if len(preToolUse) != 1 {
		t.Fatalf("PreToolUse groups = %d, want 1 (untouched)", len(preToolUse))
	}
	preHook := preToolUse[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if preHook["command"] != "/usr/bin/lint-guard" {
		t.Fatalf("PreToolUse command = %v, want the pre-existing lint-guard untouched", preHook["command"])
	}

	// The other tool's Stop hook entry survives alongside intern's new one.
	stopGroups := hooks["Stop"].([]any)
	var sawOtherTool, sawIntern bool
	for _, g := range stopGroups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			cmd := h.(map[string]any)["command"]
			if cmd == "/opt/othertool/notify-stop" {
				sawOtherTool = true
			}
			if cmd == "/usr/local/bin/intern hooks run-stop" {
				sawIntern = true
			}
		}
	}
	if !sawOtherTool {
		t.Fatal("the other tool's Stop hook entry was lost")
	}
	if !sawIntern {
		t.Fatal("intern's own Stop hook entry was not added")
	}

	// SessionStart, absent before, now exists with only intern's entry.
	startGroups := hooks["SessionStart"].([]any)
	if len(startGroups) != 1 {
		t.Fatalf("SessionStart groups = %d, want 1", len(startGroups))
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	path := settingsPath(t)

	if _, err := Install(path, "/usr/local/bin/intern"); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	res, err := Install(path, "/usr/local/bin/intern")
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if res.Changed {
		t.Fatal("Changed = true on a second, identical install, want false (no-op)")
	}

	m := asMap(t, readFile(t, path))
	hooks := m["hooks"].(map[string]any)
	if n := len(hooks["Stop"].([]any)); n != 1 {
		t.Fatalf("Stop groups after two installs = %d, want 1 (no duplicate)", n)
	}
	if n := len(hooks["SessionStart"].([]any)); n != 1 {
		t.Fatalf("SessionStart groups after two installs = %d, want 1 (no duplicate)", n)
	}
}

// TestInstallRepairsAStalePath proves a moved binary gets its existing hook
// entry rewritten in place rather than leaving a second, dead entry behind.
func TestInstallRepairsAStalePath(t *testing.T) {
	path := settingsPath(t)

	if _, err := Install(path, "/old/path/intern"); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	res, err := Install(path, "/new/path/intern")
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if !res.Changed {
		t.Fatal("Changed = false, want true: the path moved")
	}

	m := asMap(t, readFile(t, path))
	hooks := m["hooks"].(map[string]any)

	stopGroups := hooks["Stop"].([]any)
	var commands []string
	for _, g := range stopGroups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			commands = append(commands, h.(map[string]any)["command"].(string))
		}
	}
	if len(commands) != 1 {
		t.Fatalf("Stop hook commands = %v, want exactly 1 (the stale one repaired, not duplicated)", commands)
	}
	if commands[0] != "/new/path/intern hooks run-stop" {
		t.Fatalf("Stop command = %q, want the repaired path", commands[0])
	}
}

func TestInspectOnMissingFileReportsNotInstalled(t *testing.T) {
	path := settingsPath(t)

	st, err := Inspect(path, "/usr/local/bin/intern")
	if err != nil {
		t.Fatalf("Inspect on a missing file: %v", err)
	}
	if st.StopInstalled || st.SessionStartInstalled {
		t.Fatalf("status = %+v, want nothing installed", st)
	}
}

func TestInstallQuotesACommandPathContainingASpace(t *testing.T) {
	path := settingsPath(t)
	if _, err := Install(path, "/Applications/My Tools/intern"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	m := asMap(t, readFile(t, path))
	hooks := m["hooks"].(map[string]any)
	stopHook := hooks["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	cmd := stopHook["command"].(string)
	if cmd != `"/Applications/My Tools/intern" hooks run-stop` {
		t.Fatalf("command = %q, want the path quoted", cmd)
	}
}
