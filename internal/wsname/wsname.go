// Package wsname resolves a working directory to a tether workspace name,
// derived from the git root so agents in the same repo agree.
package wsname

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvVar overrides workspace-name detection when set to a non-empty value.
const EnvVar = "TETHER_WORKSPACE"

// gitEntry is a directory in a normal clone but a regular file in a
// worktree/submodule, so only its existence is checked.
const gitEntry = ".git"

// Resolve returns the workspace name for cwd: TETHER_WORKSPACE if set,
// otherwise the basename of the nearest ancestor containing .git, falling
// back to cwd's own basename.
func Resolve(cwd string) (string, error) {
	if ws := os.Getenv(EnvVar); ws != "" {
		return ws, nil
	}

	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("wsname: absolute path for %q: %w", cwd, err)
	}

	// Best effort: symlink resolution failure falls back to the plain path.
	dir := abs
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		dir = resolved
	}

	if root, ok := findGitRoot(dir); ok {
		return filepath.Base(root), nil
	}

	return filepath.Base(dir), nil
}

// findGitRoot walks up from dir looking for a .git entry, returning the first
// directory that has one.
func findGitRoot(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, gitEntry)); err == nil {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root (or a volume root on Windows).
			return "", false
		}
		dir = parent
	}
}
