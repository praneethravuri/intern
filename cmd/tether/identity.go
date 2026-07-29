package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"

	"github.com/praneethravuri/tether/internal/proc"
	"github.com/praneethravuri/tether/internal/wsname"
)

// envName is the environment variable a harness sets once so every later
// tether invocation in that session knows who it is without --as.
const envName = "TETHER_NAME"

// Harness identifiers reported at registration time. They are stable strings:
// the daemon maps them to a notifier and a wake tier.
const (
	harnessClaudeCode = "claude-code"
	harnessGeminiCLI  = "gemini-cli"
	harnessCodex      = "codex"
	harnessCopilotCLI = "copilot-cli"
	harnessOpenCode   = "opencode"
	harnessAmp        = "amp"
	harnessUnknown    = "unknown"
)

// resolveWorkspace returns the workspace to operate in: the flag when given,
// otherwise the workspace of the current directory.
func resolveWorkspace(workspaceFlag string) (string, error) {
	if ws := strings.TrimSpace(workspaceFlag); ws != "" {
		return ws, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", failf(exitGeneral, "cannot determine the current directory: %v", err)
	}

	ws, err := wsname.Resolve(cwd)
	if err != nil {
		return "", failf(exitGeneral, "cannot determine the workspace for %s: %v", cwd, err)
	}
	if ws == "" {
		return "", failf(exitGeneral,
			"the workspace for %s resolved to an empty name — set %s", cwd, wsname.EnvVar)
	}
	return ws, nil
}

// resolveSelf works out which agent is running this command: name from the
// flag, else $TETHER_NAME, else derived from harness+session.
func resolveSelf(nameFlag, workspaceFlag string) (name, workspace string, err error) {
	workspace, err = resolveWorkspace(workspaceFlag)
	if err != nil {
		return "", "", err
	}

	name = strings.TrimSpace(nameFlag)
	if name == "" {
		name = strings.TrimSpace(os.Getenv(envName))
	}
	if name == "" {
		name = derivedName()
	}
	if err := validateName(name); err != nil {
		return "", "", err
	}

	return name, workspace, nil
}

// currentSession returns the harness and session id for this invocation.
// Every command authenticating as itself must call this rather than
// recompute the session id, or the daemon sees a false mismatch.
func currentSession() (harness, session string) {
	harness, session = detectHarness()
	if session == "" {
		session = syntheticSessionID()
	}
	return harness, session
}

// derivedName synthesises "<harness>-<hex4>" from a hash of the session id,
// stable per shell, when the caller supplied no --as or $TETHER_NAME.
func derivedName() string {
	harness, session := currentSession()
	h := fnv.New32a()
	_, _ = h.Write([]byte(session))
	return fmt.Sprintf("%s-%04x", harness, h.Sum32()&0xffff)
}

// validateName rejects names that would be unroutable or could smuggle a
// terminal escape into another agent's screen via who/status/inbox output.
func validateName(name string) error {
	switch {
	case name == "":
		return failf(exitGeneral, "the agent name is empty")
	case strings.Contains(name, "@"):
		return failf(exitGeneral,
			"the agent name %q contains '@' — pass a bare name and use --workspace "+
				"for the workspace", name)
	case strings.ContainsAny(name, " \t\n"):
		return failf(exitGeneral, "the agent name %q contains whitespace", name)
	case hasControlByte(name):
		return failf(exitGeneral, "the agent name %q contains a control character", name)
	default:
		return nil
	}
}

// hasControlByte reports whether s contains a C0 control code or DEL.
func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// resolveTarget splits an address of the form name@workspace, on the last
// "@". A bare name resolves against defaultWS.
func resolveTarget(addr, defaultWS string) (name, workspace string, err error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", "", failf(exitGeneral,
			"empty address: expected name@workspace, or a bare name for this workspace")
	}

	at := strings.LastIndex(addr, "@")
	if at < 0 {
		if defaultWS == "" {
			return "", "", failf(exitGeneral,
				"cannot resolve the bare name %q: no workspace is known, "+
					"write it as %s@<workspace>", addr, addr)
		}
		return addr, defaultWS, nil
	}

	name = addr[:at]
	workspace = addr[at+1:]
	if name == "" {
		return "", "", failf(exitGeneral,
			"address %q has no agent name before the '@' (expected name@workspace)", addr)
	}
	if workspace == "" {
		return "", "", failf(exitGeneral,
			"address %q has no workspace after the '@' (expected name@workspace)", addr)
	}

	return name, workspace, nil
}

// address renders a name and workspace as the canonical name@workspace form.
func address(name, workspace string) string {
	return fmt.Sprintf("%s@%s", name, workspace)
}

// detectHarness sniffs the environment for which coding-agent harness is
// running this command, and its session id where one is exposed.
func detectHarness() (harness, sessionID string) {
	if sid := env("CLAUDE_CODE_SESSION_ID"); sid != "" {
		return harnessClaudeCode, sid
	}
	if env("CLAUDECODE") != "" {
		return harnessClaudeCode, ""
	}
	if sid := env("GEMINI_SESSION_ID"); sid != "" {
		return harnessGeminiCLI, sid
	}
	if env("CODEX_HOME") != "" {
		return harnessCodex, env("CODEX_SESSION_ID")
	}
	if env("COPILOT_HOME") != "" {
		return harnessCopilotCLI, env("COPILOT_SESSION_ID")
	}
	if sid := env("OPENCODE_SESSION_ID"); sid != "" {
		return harnessOpenCode, sid
	}
	if hasEnvPrefix("OPENCODE_") {
		return harnessOpenCode, ""
	}
	if sid := env("AMP_THREAD_ID"); sid != "" { // both the marker and the session id
		return harnessAmp, sid
	}
	return harnessUnknown, ""
}

// env reads an environment variable with surrounding whitespace removed, which
// matters because harnesses export these from shell snippets.
func env(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// envSessionOverride lets an unrecognised harness, or a test, supply its own
// session id directly, bypassing the derivation below.
const envSessionOverride = "TETHER_SESSION_ID"

// syntheticSessionID gives an unrecognised harness (including a plain shell)
// a stable-per-shell id instead of empty, since an empty session_id never
// matches on re-register and would make a repeat `tether register` fail as
// a false name conflict. Derived from the parent process's pid+start time,
// which is stable per shell and distinct across shells even under pid reuse.
//
// $TETHER_SESSION_ID wins only within this fallback: a harness detectHarness
// already recognises (one that reports its own session id) is not affected,
// since this function is never called for it.
func syntheticSessionID() string {
	if sid := env(envSessionOverride); sid != "" {
		return sid
	}

	ppid := os.Getppid()
	start, err := proc.StartTime(ppid)
	if err != nil {
		return "" // cannot identify the parent; honest empty rather than fabricated
	}
	return fmt.Sprintf("shell-%d-%d", ppid, start)
}

// hasEnvPrefix reports whether any environment variable whose name starts with
// prefix is set to a non-empty value.
func hasEnvPrefix(prefix string) bool {
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if strings.HasPrefix(kv[:eq], prefix) && strings.TrimSpace(kv[eq+1:]) != "" {
			return true
		}
	}
	return false
}
