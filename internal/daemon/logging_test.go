package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestOpenLog_AppendsWhenSmall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	if err := os.WriteFile(path, []byte("existing\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	f, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog: %v", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString("more\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "existing\nmore\n" {
		t.Fatalf("log content = %q, want the old content preserved with the new appended", got)
	}
}

// TestOpenLog_RotatesInPlaceKeepingTheTail is 6.18: an oversized log is
// truncated in place, keeping the most recent bytes -- not deleted outright
// and recreated, which is both a lost-tail bug and a race with any other
// process that has the same path open.
func TestOpenLog_RotatesInPlaceKeepingTheTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	old := strings.Repeat("a", logMaxBytes/2)
	tail := "THE PART WORTH KEEPING\n"
	if err := os.WriteFile(path, []byte(old+strings.Repeat("b", logMaxBytes)+tail), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	f, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog: %v", err)
	}
	defer func() { _ = f.Close() }()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if int64(len(got)) > logMaxBytes {
		t.Fatalf("rotated log is %d bytes, want at most %d", len(got), logMaxBytes)
	}
	if !strings.HasSuffix(string(got), tail) {
		t.Fatalf("rotated log lost its tail: last 40 bytes = %q", string(got)[max(0, len(got)-40):])
	}
	if strings.Contains(string(got), old) {
		t.Fatal("rotated log kept the oldest content instead of the most recent")
	}

	// The fd is still valid and appending continues to work after rotation.
	if _, err := f.WriteString("fresh\n"); err != nil {
		t.Fatalf("WriteString after rotation: %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after append: %v", err)
	}
	if !strings.HasSuffix(string(got), "fresh\n") {
		t.Fatalf("append after rotation did not land: %q", string(got)[max(0, len(got)-40):])
	}
}

func TestOpenLog_NeverRemovesThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	oversized := make([]byte, logMaxBytes+100)
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	inoBefore := inode(t, path)
	f, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog: %v", err)
	}
	defer func() { _ = f.Close() }()

	if inode(t, path) != inoBefore {
		t.Fatal("rotation replaced the file at path instead of truncating it in place")
	}
}

// TestNewJSONLogger_ProducesValidJSONLines proves the Printf-to-slog bridge
// actually emits structured JSON, not plain text, with no call-site changes
// needed anywhere Printf is already used.
func TestNewJSONLogger_ProducesValidJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	f, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog: %v", err)
	}
	defer func() { _ = f.Close() }()

	logger := newJSONLogger(f)
	logger.Printf("listening on %s", "/tmp/sock")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	line := strings.TrimSpace(string(raw))

	var parsed struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("log line is not valid JSON: %q: %v", line, err)
	}
	if parsed.Msg != "listening on /tmp/sock" {
		t.Fatalf("msg = %q, want %q", parsed.Msg, "listening on /tmp/sock")
	}
}

// inode returns path's inode number, so a test can prove rotation truncated
// the existing file rather than replacing it with a new one at the same path.
func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("Sys() did not return *syscall.Stat_t on this platform")
	}
	return stat.Ino
}
