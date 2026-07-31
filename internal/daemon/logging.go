package daemon

import (
	"io"
	"log"
	"log/slog"
	"os"
)

// logMaxBytes caps the daemon log; past this it's rotated in place, keeping
// only the most recent bytes -- the tail is what a bug report actually needs.
const logMaxBytes = 1 << 20 // 1 MiB

// openLog opens path for a structured JSON log, rotating in place first if
// it has already grown past logMaxBytes. Never removes or recreates the
// path: a concurrent process opening the same path mid-rotation would race
// into an unlinked inode and silently lose its writes. The returned file is
// opened O_APPEND, so every write -- from this process or another with the
// same path open -- lands atomically at the current end of file.
func openLog(path string) (*os.File, error) {
	if err := rotateIfLarge(path); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// rotateIfLarge truncates path to its last logMaxBytes if it has grown past
// that. It opens its own non-append descriptor to do the pwrite the
// truncate-then-rewrite needs (Go forbids WriteAt on an O_APPEND file); a
// log line written by another writer in the narrow window between the
// truncate and the rewrite can be lost -- an accepted ceiling for a
// diagnostic file rotated at most every few minutes, not worth a flock.
func rotateIfLarge(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() <= logMaxBytes {
		return nil
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	tail := make([]byte, logMaxBytes)
	if _, err := f.ReadAt(tail, info.Size()-logMaxBytes); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.WriteAt(tail, 0)
	return err
}

// newJSONLogger wraps w in a *log.Logger backed by a JSON slog handler, so
// every existing Printf-style call site becomes a structured log line with
// no call-site changes.
func newJSONLogger(w io.Writer) *log.Logger {
	return slog.NewLogLogger(slog.NewJSONHandler(w, nil), slog.LevelInfo)
}
