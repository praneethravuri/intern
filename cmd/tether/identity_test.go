package main

import (
	"testing"
)

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		name      string
		addr      string
		defaultWS string
		wantName  string
		wantWS    string
		wantErr   bool
	}{
		{
			name:      "full address",
			addr:      "a@b",
			defaultWS: "other",
			wantName:  "a",
			wantWS:    "b",
		},
		{
			name:      "bare name uses the default workspace",
			addr:      "a",
			defaultWS: "storefront",
			wantName:  "a",
			wantWS:    "storefront",
		},
		{
			name:    "bare name without a default is an error",
			addr:    "a",
			wantErr: true,
		},
		{
			name:      "missing workspace",
			addr:      "a@",
			defaultWS: "storefront",
			wantErr:   true,
		},
		{
			name:      "missing name",
			addr:      "@b",
			defaultWS: "storefront",
			wantErr:   true,
		},
		{
			name:      "empty address",
			addr:      "",
			defaultWS: "storefront",
			wantErr:   true,
		},
		{
			name:      "whitespace only address",
			addr:      "   ",
			defaultWS: "storefront",
			wantErr:   true,
		},
		{
			name:      "the last at sign wins",
			addr:      "a@b@c",
			defaultWS: "storefront",
			wantName:  "a@b",
			wantWS:    "c",
		},
		{
			name:      "only an at sign",
			addr:      "@",
			defaultWS: "storefront",
			wantErr:   true,
		},
		{
			name:      "surrounding whitespace is trimmed",
			addr:      "  a@b  ",
			defaultWS: "storefront",
			wantName:  "a",
			wantWS:    "b",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, ws, err := resolveTarget(tc.addr, tc.defaultWS)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveTarget(%q, %q) = (%q, %q), want an error",
						tc.addr, tc.defaultWS, name, ws)
				}
				if got := exitCodeFor(err); got != exitGeneral {
					t.Fatalf("exit code = %d, want %d", got, exitGeneral)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTarget(%q, %q) returned error: %v", tc.addr, tc.defaultWS, err)
			}
			if name != tc.wantName || ws != tc.wantWS {
				t.Fatalf("resolveTarget(%q, %q) = (%q, %q), want (%q, %q)",
					tc.addr, tc.defaultWS, name, ws, tc.wantName, tc.wantWS)
			}
		})
	}
}

// harnessEnvVars is every variable detectHarness looks at. Tests clear all of
// them so the developer's own harness cannot leak into the result.
var harnessEnvVars = []string{
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDECODE",
	"GEMINI_SESSION_ID",
	"CODEX_HOME",
	"CODEX_SESSION_ID",
	"COPILOT_HOME",
	"COPILOT_SESSION_ID",
	"OPENCODE_SESSION_ID",
	"OPENCODE_BIN_PATH",
	"AMP_THREAD_ID",
}

func clearHarnessEnv(t *testing.T) {
	t.Helper()
	for _, key := range harnessEnvVars {
		t.Setenv(key, "")
	}
}

func TestDetectHarness(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantHarness string
		wantSession string
	}{
		{
			name:        "nothing set",
			wantHarness: harnessUnknown,
		},
		{
			name:        "claude code with a session id",
			env:         map[string]string{"CLAUDE_CODE_SESSION_ID": "sess-123"},
			wantHarness: harnessClaudeCode,
			wantSession: "sess-123",
		},
		{
			name:        "claude code without a session id",
			env:         map[string]string{"CLAUDECODE": "1"},
			wantHarness: harnessClaudeCode,
		},
		{
			name: "the session id wins over the bare marker",
			env: map[string]string{
				"CLAUDECODE":             "1",
				"CLAUDE_CODE_SESSION_ID": "sess-abc",
			},
			wantHarness: harnessClaudeCode,
			wantSession: "sess-abc",
		},
		{
			name:        "gemini cli",
			env:         map[string]string{"GEMINI_SESSION_ID": "g-1"},
			wantHarness: harnessGeminiCLI,
			wantSession: "g-1",
		},
		{
			name:        "codex",
			env:         map[string]string{"CODEX_HOME": "/home/me/.codex"},
			wantHarness: harnessCodex,
		},
		{
			name: "codex with a session id",
			env: map[string]string{
				"CODEX_HOME":       "/home/me/.codex",
				"CODEX_SESSION_ID": "c-9",
			},
			wantHarness: harnessCodex,
			wantSession: "c-9",
		},
		{
			name:        "copilot cli",
			env:         map[string]string{"COPILOT_HOME": "/home/me/.copilot"},
			wantHarness: harnessCopilotCLI,
		},
		{
			name:        "opencode via its session id",
			env:         map[string]string{"OPENCODE_SESSION_ID": "o-2"},
			wantHarness: harnessOpenCode,
			wantSession: "o-2",
		},
		{
			name:        "opencode via any other opencode variable",
			env:         map[string]string{"OPENCODE_BIN_PATH": "/usr/local/bin/opencode"},
			wantHarness: harnessOpenCode,
		},
		{
			name:        "an empty variable counts as unset",
			env:         map[string]string{"CLAUDE_CODE_SESSION_ID": "", "CODEX_HOME": ""},
			wantHarness: harnessUnknown,
		},
		{
			name:        "amp, whose thread id is both the marker and the session id",
			env:         map[string]string{"AMP_THREAD_ID": "thread-42"},
			wantHarness: harnessAmp,
			wantSession: "thread-42",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearHarnessEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			harness, session := detectHarness()
			if harness != tc.wantHarness {
				t.Fatalf("harness = %q, want %q", harness, tc.wantHarness)
			}
			if session != tc.wantSession {
				t.Fatalf("session id = %q, want %q", session, tc.wantSession)
			}
		})
	}
}

func TestResolveSelf(t *testing.T) {
	t.Run("flags win over the environment", func(t *testing.T) {
		setIdentity(t, "from-env", "env-ws")

		name, ws, err := resolveSelf("from-flag", "flag-ws")
		if err != nil {
			t.Fatalf("resolveSelf: %v", err)
		}
		if name != "from-flag" || ws != "flag-ws" {
			t.Fatalf("resolveSelf = (%q, %q), want (from-flag, flag-ws)", name, ws)
		}
	})

	t.Run("falls back to the environment", func(t *testing.T) {
		setIdentity(t, "frontend", "storefront")

		name, ws, err := resolveSelf("", "")
		if err != nil {
			t.Fatalf("resolveSelf: %v", err)
		}
		if name != "frontend" || ws != "storefront" {
			t.Fatalf("resolveSelf = (%q, %q), want (frontend, storefront)", name, ws)
		}
	})

	t.Run("no name anywhere derives one instead of erroring", func(t *testing.T) {
		// This is the behaviour implicit registration replaces: resolveSelf
		// used to hard-error here, requiring an explicit `tether register`
		// or --as before any other command could run at all.
		t.Setenv(envName, "")
		t.Setenv(envSessionOverride, "shell-abc")
		clearHarnessEnv(t)
		t.Setenv("TETHER_WORKSPACE", "storefront")

		name, ws, err := resolveSelf("", "")
		if err != nil {
			t.Fatalf("resolveSelf with no name: %v", err)
		}
		if ws != "storefront" {
			t.Fatalf("workspace = %q, want storefront", ws)
		}
		if name == "" {
			t.Fatal("resolveSelf derived an empty name")
		}
		if err := validateName(name); err != nil {
			t.Fatalf("derived name %q is not a valid name: %v", name, err)
		}
	})

	t.Run("an address passed to --as is rejected", func(t *testing.T) {
		setIdentity(t, "", "storefront")

		_, _, err := resolveSelf("frontend@storefront", "")
		if err == nil {
			t.Fatal("resolveSelf accepted an address as a name")
		}
		requireContains(t, err.Error(), "--workspace", "error")
	})
}

// TestValidateNameRejectsControlBytes is H1: a name is echoed back verbatim
// by every other agent's `who`/`status`/`inbox`, so it must be refused at
// the source rather than merely sanitised downstream.
func TestValidateNameRejectsControlBytes(t *testing.T) {
	hostile := []string{
		"ali\x1bce", // ESC, the start of every ANSI escape sequence
		"ali\rce",   // carriage return
		"ali\x07ce", // BEL
		"ali\x7fce", // DEL
		"ali\x00ce", // NUL
		"\x1b]0;pwned\x07evil",
	}
	for _, name := range hostile {
		if err := validateName(name); err == nil {
			t.Fatalf("validateName(%q) = nil, want a control-byte rejection", name)
		}
	}
}

func TestValidateNameRejectsEmptyAndWhitespace(t *testing.T) {
	for _, name := range []string{"", "alice bob", "alice\tbob", "alice\nbob"} {
		if err := validateName(name); err == nil {
			t.Fatalf("validateName(%q) = nil, want an error", name)
		}
	}
}

func TestValidateNameAcceptsOrdinaryNames(t *testing.T) {
	for _, name := range []string{"frontend", "backend-2", "a.b_c", "café"} {
		if err := validateName(name); err != nil {
			t.Fatalf("validateName(%q) = %v, want nil", name, err)
		}
	}
}

func TestHasControlByte(t *testing.T) {
	for _, s := range []string{"", "plain", "unicode: café", "with space"} {
		if hasControlByte(s) {
			t.Errorf("hasControlByte(%q) = true, want false", s)
		}
	}
	for _, s := range []string{"\x1b", "\r", "\n", "\x00", "\x7f", "a\x1bb"} {
		if !hasControlByte(s) {
			t.Errorf("hasControlByte(%q) = false, want true", s)
		}
	}
}

// -- M2: harness-less re-registration --------------------------------------

// TestSyntheticSessionIDHonoursOverride is M2: $TETHER_SESSION_ID must win
// over the derived value whenever it is set, so a harness (or a test) that
// already knows its own stable identity can supply it directly.
func TestSyntheticSessionIDHonoursOverride(t *testing.T) {
	t.Setenv(envSessionOverride, "fixed-session-id")
	if got := syntheticSessionID(); got != "fixed-session-id" {
		t.Fatalf("syntheticSessionID() = %q, want the override", got)
	}
}

// TestSyntheticSessionIDIsStableForThisProcess is M2's core guarantee:
// repeated calls from what is effectively "the same shell" (the same test
// process, calling twice) must return the same value, or a harness-less
// `tether register --as X` run twice in a row would still look like two
// different sessions and still fail with a conflict.
func TestSyntheticSessionIDIsStableForThisProcess(t *testing.T) {
	t.Setenv(envSessionOverride, "")

	a := syntheticSessionID()
	b := syntheticSessionID()
	if a != b {
		t.Fatalf("syntheticSessionID() is not stable across calls: %q != %q", a, b)
	}
	// It degrades to "" on a platform proc.StartTime cannot support, which
	// is itself stable (always ""), so this is deliberately not asserting
	// a != "".
}

// -- M3: implicit registration's derived name --------------------------------

// TestDerivedNameIsStableForTheSameSession is the core guarantee behind
// implicit registration: the same session id must always derive the same
// name, or two invocations from the same shell would derive two different
// names and never see each other's registration as idempotent.
func TestDerivedNameIsStableForTheSameSession(t *testing.T) {
	clearHarnessEnv(t)
	t.Setenv(envSessionOverride, "session-a")

	a := derivedName()
	b := derivedName()
	if a != b {
		t.Fatalf("derivedName() is not stable across calls: %q != %q", a, b)
	}
}

// TestDerivedNameDiffersForADifferentSession is the flip side: a different
// session id must derive a different name, or two unrelated shells would
// collide on the same identity.
func TestDerivedNameDiffersForADifferentSession(t *testing.T) {
	clearHarnessEnv(t)

	t.Setenv(envSessionOverride, "session-a")
	a := derivedName()

	t.Setenv(envSessionOverride, "session-b")
	b := derivedName()

	if a == b {
		t.Fatalf("derivedName() collided for two different sessions: both = %q", a)
	}
}

// TestAsOverridesTheDerivedNameEvenWithASessionPresent proves --as always
// wins: a caller that explicitly names itself must never be overridden by
// the fallback that only exists for the caller that supplied nothing.
func TestAsOverridesTheDerivedNameEvenWithASessionPresent(t *testing.T) {
	clearHarnessEnv(t)
	t.Setenv(envName, "")
	t.Setenv(envSessionOverride, "session-a")
	t.Setenv("TETHER_WORKSPACE", "storefront")

	name, _, err := resolveSelf("explicit-name", "")
	if err != nil {
		t.Fatalf("resolveSelf: %v", err)
	}
	if name != "explicit-name" {
		t.Fatalf("name = %q, want the --as value to win", name)
	}
}

func TestResolveWorkspaceUsesTheEnvironmentOverride(t *testing.T) {
	t.Setenv("TETHER_WORKSPACE", "storefront")

	ws, err := resolveWorkspace("")
	if err != nil {
		t.Fatalf("resolveWorkspace: %v", err)
	}
	if ws != "storefront" {
		t.Fatalf("resolveWorkspace = %q, want storefront", ws)
	}
}
