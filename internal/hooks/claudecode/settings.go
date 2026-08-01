package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code hook event names this package installs into.
const (
	eventStop         = "Stop"
	eventSessionStart = "SessionStart"

	subcommandStop         = "hooks run-stop"
	subcommandSessionStart = "hooks run-session-start"
)

// hookEntry is one Claude Code hook command, as documented in the hooks
// reference. Async/AsyncRewake let a Stop hook block for mail outside the
// model's own tool-call timeout and wake the session when it exits.
type hookEntry struct {
	Type        string `json:"type"`
	Command     string `json:"command"`
	Async       bool   `json:"async,omitempty"`
	AsyncRewake bool   `json:"asyncRewake,omitempty"`
}

// hookGroup is one matcher group within an event's hook array. Stop and
// SessionStart are not tool-scoped, so Matcher is always empty here.
type hookGroup struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

// InstallResult reports what Install did.
type InstallResult struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
}

// Install idempotently merges intern's Stop and SessionStart hook entries
// into the Claude Code settings file at path, creating it (and its parent
// directory) if absent. Every unrelated top-level key and every other hook
// entry, including other tools' entries for the same events, is preserved.
// The file is only touched when something actually changed.
func Install(path, internExe string) (InstallResult, error) {
	doc, err := loadSettings(path)
	if err != nil {
		return InstallResult{}, err
	}

	stopChanged, err := doc.mergeHook(eventStop, subcommandStop, hookEntry{
		Type: "command", Command: commandFor(internExe, subcommandStop),
		Async: true, AsyncRewake: true,
	})
	if err != nil {
		return InstallResult{}, err
	}

	startChanged, err := doc.mergeHook(eventSessionStart, subcommandSessionStart, hookEntry{
		Type: "command", Command: commandFor(internExe, subcommandSessionStart),
	})
	if err != nil {
		return InstallResult{}, err
	}

	if !stopChanged && !startChanged {
		return InstallResult{Path: path, Changed: false}, nil
	}
	if err := doc.save(path); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Path: path, Changed: true}, nil
}

// Status reports whether intern's hooks are present in a settings file and
// whether they point at internExe's current path.
type Status struct {
	Path                  string `json:"path"`
	StopInstalled         bool   `json:"stop_installed"`
	StopCurrent           bool   `json:"stop_current"`
	SessionStartInstalled bool   `json:"session_start_installed"`
	SessionStartCurrent   bool   `json:"session_start_current"`
}

// Inspect reads path without modifying it and reports Status.
func Inspect(path, internExe string) (Status, error) {
	doc, err := loadSettings(path)
	if err != nil {
		return Status{}, err
	}
	hooks, err := doc.hooks()
	if err != nil {
		return Status{}, err
	}

	stopInstalled, stopCurrent := inspectEvent(hooks, eventStop, subcommandStop, commandFor(internExe, subcommandStop))
	startInstalled, startCurrent := inspectEvent(hooks, eventSessionStart, subcommandSessionStart, commandFor(internExe, subcommandSessionStart))

	return Status{
		Path:                  path,
		StopInstalled:         stopInstalled,
		StopCurrent:           stopCurrent,
		SessionStartInstalled: startInstalled,
		SessionStartCurrent:   startCurrent,
	}, nil
}

// settingsDoc holds a settings.json parsed one level deep, so every
// top-level key this package doesn't touch round-trips byte-for-byte in
// value even though key order is not preserved (Go maps have none).
type settingsDoc map[string]json.RawMessage

func loadSettings(path string) (settingsDoc, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is a settings location the caller chose, not untrusted input
	if errors.Is(err, fs.ErrNotExist) {
		return settingsDoc{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return settingsDoc{}, nil
	}

	var doc settingsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc == nil {
		doc = settingsDoc{}
	}
	return doc, nil
}

// hooks decodes the top-level "hooks" key one level deeper, event name to
// its raw array, without touching events this package doesn't know about.
func (doc settingsDoc) hooks() (map[string]json.RawMessage, error) {
	hooks := map[string]json.RawMessage{}
	if raw, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("parse hooks: %w", err)
		}
	}
	return hooks, nil
}

// mergeHook installs want into event, identified across existing groups by
// subcommand (see isOurCommand), and reports whether the document changed.
func (doc settingsDoc) mergeHook(event, subcommand string, want hookEntry) (bool, error) {
	hooks, err := doc.hooks()
	if err != nil {
		return false, err
	}

	var groups []hookGroup
	if raw, ok := hooks[event]; ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return false, fmt.Errorf("parse hooks.%s: %w", event, err)
		}
	}

	groups, changed := mergeGroup(groups, subcommand, want)
	if !changed {
		return false, nil
	}

	groupsRaw, err := json.Marshal(groups)
	if err != nil {
		return false, err
	}
	hooks[event] = groupsRaw

	hooksRaw, err := json.Marshal(hooks)
	if err != nil {
		return false, err
	}
	doc["hooks"] = hooksRaw
	return true, nil
}

// mergeGroup finds subcommand among groups' hook entries and repairs it in
// place if it drifted from want, or appends a new dedicated group if it is
// missing. It never rewrites any other entry, so unrelated hooks sharing a
// group are left exactly as they were.
func mergeGroup(groups []hookGroup, subcommand string, want hookEntry) ([]hookGroup, bool) {
	if gi, hi, found := locate(groups, subcommand); found {
		if groups[gi].Hooks[hi] == want {
			return groups, false
		}
		groups[gi].Hooks[hi] = want
		return groups, true
	}
	return append(groups, hookGroup{Hooks: []hookEntry{want}}), true
}

func inspectEvent(hooks map[string]json.RawMessage, event, subcommand, wantCommand string) (installed, current bool) {
	raw, ok := hooks[event]
	if !ok {
		return false, false
	}
	var groups []hookGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		return false, false
	}
	gi, hi, found := locate(groups, subcommand)
	if !found {
		return false, false
	}
	return true, groups[gi].Hooks[hi].Command == wantCommand
}

// locate finds subcommand's hook entry among groups, if present -- the
// shared search behind both merging and inspecting.
func locate(groups []hookGroup, subcommand string) (gi, hi int, found bool) {
	for gi := range groups {
		for hi := range groups[gi].Hooks {
			if isOurCommand(groups[gi].Hooks[hi].Command, subcommand) {
				return gi, hi, true
			}
		}
	}
	return 0, 0, false
}

// isOurCommand reports whether cmd is intern's own hook invocation for
// subcommand, regardless of where the intern binary itself lives -- the
// binary path prefix can change (see the repair case), but the subcommand
// suffix it invokes cannot.
func isOurCommand(cmd, subcommand string) bool {
	cmd = strings.TrimSpace(cmd)
	return cmd == subcommand || strings.HasSuffix(cmd, " "+subcommand)
}

// commandFor renders the shell command Claude Code should run, quoting the
// binary path only if it needs it.
func commandFor(internExe, subcommand string) string {
	if strings.ContainsAny(internExe, " \t\"'$`\\") {
		internExe = fmt.Sprintf("%q", internExe)
	}
	return internExe + " " + subcommand
}

// save writes doc to path, creating its parent directory if needed, via a
// temp file plus rename so a crash mid-write can never corrupt the file a
// human or Claude Code reads next.
func (doc settingsDoc) save(path string) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	raw = append(raw, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o644); err != nil { //nolint:gosec // settings.json is a git-tracked config file, not secret
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}
	return nil
}
