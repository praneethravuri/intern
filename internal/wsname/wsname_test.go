package wsname

import (
	"os"
	"path/filepath"
	"testing"
)

// mkdirAll creates dir (and parents) or fails the test.
func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
}

// gitDir makes dir a normal-clone style repository root: .git is a directory.
func gitDir(t *testing.T, dir string) {
	t.Helper()
	mkdirAll(t, dir)
	mkdirAll(t, filepath.Join(dir, ".git"))
}

// gitFile makes dir a worktree/submodule style repository root: .git is a
// regular file containing a gitdir pointer.
func gitFile(t *testing.T, dir, pointsTo string) string {
	t.Helper()
	mkdirAll(t, dir)
	contents := []byte("gitdir: " + pointsTo + "\n")
	if err := os.WriteFile(filepath.Join(dir, ".git"), contents, 0o600); err != nil {
		t.Fatalf("WriteFile(%q/.git): %v", dir, err)
	}
	return dir
}

// clearEnv makes sure the override is not inherited from the test host.
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvVar, "")
}

// requireNoGitAncestor skips the test if dir lives under a real git repo on this host.
func requireNoGitAncestor(t *testing.T, dir string) {
	t.Helper()

	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	}
	if root, found := findGitRoot(resolved); found {
		t.Skipf("temp dir %q lives inside git repository %q on this host; "+
			"cannot exercise the no-git-anywhere case", resolved, root)
	}
}

// fixture builds a tree exercising every resolution shape (no-git, normal
// clone, worktree file, nested repo).
type fixture struct {
	root string
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	root := t.TempDir()
	f := fixture{root: root}

	mkdirAll(t, filepath.Join(root, "plain", "deep", "deeper"))

	gitDir(t, filepath.Join(root, "storefront"))
	mkdirAll(t, filepath.Join(root, "storefront", "frontend", "src", "ui"))

	wt := filepath.Join(root, "wt-checkout")
	gitFile(t, wt, filepath.Join(root, "storefront", ".git", "worktrees", "wt"))
	mkdirAll(t, filepath.Join(wt, "pkg", "inner"))

	gitDir(t, filepath.Join(root, "repo-a"))
	gitDir(t, filepath.Join(root, "repo-a", "vendor", "repo-b"))
	mkdirAll(t, filepath.Join(root, "repo-a", "vendor", "repo-b", "lib"))

	return f
}

func (f fixture) path(parts ...string) string {
	return filepath.Join(append([]string{f.root}, parts...)...)
}

func TestResolveGitRootDetection(t *testing.T) {
	clearEnv(t)
	f := newFixture(t)

	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{
			name: "directory that is itself a git root",
			cwd:  f.path("storefront"),
			want: "storefront",
		},
		{
			name: "one level below the git root",
			cwd:  f.path("storefront", "frontend"),
			want: "storefront",
		},
		{
			name: "several levels below the git root",
			cwd:  f.path("storefront", "frontend", "src", "ui"),
			want: "storefront",
		},
		{
			name: "worktree style dot-git file at root",
			cwd:  f.path("wt-checkout"),
			want: "wt-checkout",
		},
		{
			name: "worktree style dot-git file from a subdirectory",
			cwd:  f.path("wt-checkout", "pkg", "inner"),
			want: "wt-checkout",
		},
		{
			name: "outer repo",
			cwd:  f.path("repo-a"),
			want: "repo-a",
		},
		{
			name: "between outer repo and nested repo",
			cwd:  f.path("repo-a", "vendor"),
			want: "repo-a",
		},
		{
			name: "nested repo root: nearest wins",
			cwd:  f.path("repo-a", "vendor", "repo-b"),
			want: "repo-b",
		},
		{
			name: "inside nested repo: nearest wins",
			cwd:  f.path("repo-a", "vendor", "repo-b", "lib"),
			want: "repo-b",
		},
		{
			name: "trailing separator is irrelevant",
			cwd:  f.path("storefront", "frontend") + string(filepath.Separator),
			want: "storefront",
		},
		{
			name: "unclean path with dot-dot segments",
			cwd:  filepath.Join(f.path("storefront", "frontend"), "..", "frontend", "src"),
			want: "storefront",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.cwd)
			if err != nil {
				t.Fatalf("Resolve(%q) returned error: %v", tc.cwd, err)
			}
			if got != tc.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tc.cwd, got, tc.want)
			}
		})
	}
}

func TestResolveAgentsInOneRepoAgree(t *testing.T) {
	clearEnv(t)
	f := newFixture(t)

	dirs := []string{
		f.path("storefront"),
		f.path("storefront", "frontend"),
		f.path("storefront", "frontend", "src"),
		f.path("storefront", "frontend", "src", "ui"),
	}

	var first string
	for i, dir := range dirs {
		got, err := Resolve(dir)
		if err != nil {
			t.Fatalf("Resolve(%q) returned error: %v", dir, err)
		}
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("Resolve(%q) = %q, but Resolve(%q) = %q; agents in one repo must agree",
				dir, got, dirs[0], first)
		}
	}

	if first != "storefront" {
		t.Fatalf("agents agreed on %q, want %q", first, "storefront")
	}
}

func TestResolveNoGitAnywhere(t *testing.T) {
	clearEnv(t)
	f := newFixture(t)
	requireNoGitAncestor(t, f.root)

	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{name: "leaf directory", cwd: f.path("plain", "deep", "deeper"), want: "deeper"},
		{name: "mid directory", cwd: f.path("plain", "deep"), want: "deep"},
		{name: "top directory", cwd: f.path("plain"), want: "plain"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.cwd)
			if err != nil {
				t.Fatalf("Resolve(%q) returned error: %v", tc.cwd, err)
			}
			if got != tc.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tc.cwd, got, tc.want)
			}
		})
	}
}

func TestResolveEnvOverride(t *testing.T) {
	f := newFixture(t)

	const override = "my-workspace"
	t.Setenv(EnvVar, override)

	cwds := []string{
		"",
		".",
		f.root,
		f.path("storefront"),
		f.path("storefront", "frontend", "src", "ui"),
		f.path("repo-a", "vendor", "repo-b"),
		f.path("plain", "deep", "deeper"),
		filepath.Join(f.root, "does", "not", "exist"),
	}

	for _, cwd := range cwds {
		got, err := Resolve(cwd)
		if err != nil {
			t.Fatalf("Resolve(%q) returned error: %v", cwd, err)
		}
		if got != override {
			t.Fatalf("Resolve(%q) = %q, want override %q", cwd, got, override)
		}
	}
}

func TestResolveEnvOverrideVerbatim(t *testing.T) {
	f := newFixture(t)

	values := []string{
		"team alpha",
		"/looks/like/a/path",
		"  padded  ",
		"UPPER-case_Mixed.123",
	}

	for _, v := range values {
		t.Run(v, func(t *testing.T) {
			t.Setenv(EnvVar, v)
			got, err := Resolve(f.path("storefront", "frontend"))
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			if got != v {
				t.Fatalf("Resolve = %q, want %q verbatim", got, v)
			}
		})
	}
}

func TestResolveEnvEmptyFallsThrough(t *testing.T) {
	f := newFixture(t)
	t.Setenv(EnvVar, "")

	dir := f.path("storefront", "frontend")
	got, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve(%q) returned error: %v", dir, err)
	}
	if got != "storefront" {
		t.Fatalf("Resolve(%q) = %q, want %q", dir, got, "storefront")
	}
}

func TestResolveRelativeMatchesAbsolute(t *testing.T) {
	clearEnv(t)
	f := newFixture(t)

	deep := f.path("storefront", "frontend", "src", "ui")

	want, err := Resolve(deep)
	if err != nil {
		t.Fatalf("Resolve(%q) returned error: %v", deep, err)
	}
	if want != "storefront" {
		t.Fatalf("Resolve(%q) = %q, want %q", deep, want, "storefront")
	}

	t.Chdir(deep)

	for _, cwd := range []string{"", ".", "./", "../ui", filepath.Join("..", "..", "src", "ui")} {
		got, err := Resolve(cwd)
		if err != nil {
			t.Fatalf("Resolve(%q) returned error: %v", cwd, err)
		}
		if got != want {
			t.Fatalf("Resolve(%q) = %q, want %q (same as absolute form)", cwd, got, want)
		}
	}
}

func TestResolveRelativeFromRepoRoot(t *testing.T) {
	clearEnv(t)
	f := newFixture(t)

	t.Chdir(f.path("storefront"))

	for _, cwd := range []string{".", "frontend", "frontend/src", "frontend/src/ui"} {
		got, err := Resolve(filepath.FromSlash(cwd))
		if err != nil {
			t.Fatalf("Resolve(%q) returned error: %v", cwd, err)
		}
		if got != "storefront" {
			t.Fatalf("Resolve(%q) = %q, want %q", cwd, got, "storefront")
		}
	}
}

func TestResolveFollowsSymlinks(t *testing.T) {
	clearEnv(t)
	f := newFixture(t)

	realPath := f.path("storefront", "frontend", "src")
	link := f.path("link-to-src")
	if err := os.Symlink(realPath, link); err != nil {
		t.Skipf("cannot create symlinks on this host: %v", err)
	}

	gotReal, err := Resolve(realPath)
	if err != nil {
		t.Fatalf("Resolve(%q) returned error: %v", realPath, err)
	}
	gotLink, err := Resolve(link)
	if err != nil {
		t.Fatalf("Resolve(%q) returned error: %v", link, err)
	}

	if gotLink != gotReal {
		t.Fatalf("Resolve via symlink %q = %q, but via real path %q = %q; must agree",
			link, gotLink, realPath, gotReal)
	}
	if gotLink != "storefront" {
		t.Fatalf("Resolve(%q) = %q, want %q", link, gotLink, "storefront")
	}

	repoLink := f.path("link-to-repo")
	if err := os.Symlink(f.path("storefront"), repoLink); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	got, err := Resolve(repoLink)
	if err != nil {
		t.Fatalf("Resolve(%q) returned error: %v", repoLink, err)
	}
	if got != "storefront" {
		t.Fatalf("Resolve(%q) = %q, want %q", repoLink, got, "storefront")
	}
}

func TestResolveNonExistentPath(t *testing.T) {
	clearEnv(t)
	f := newFixture(t)

	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{
			name: "missing dir inside a repo still finds the repo",
			cwd:  f.path("storefront", "frontend", "no-such-dir"),
			want: "storefront",
		},
		{
			name: "missing dir outside any repo falls back to basename",
			cwd:  f.path("plain", "no-such-dir"),
			want: "no-such-dir",
		},
	}

	requireNoGitAncestor(t, f.root)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.cwd)
			if err != nil {
				t.Fatalf("Resolve(%q) returned error: %v", tc.cwd, err)
			}
			if got != tc.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tc.cwd, got, tc.want)
			}
		})
	}
}

func TestResolveFilesystemRoot(t *testing.T) {
	clearEnv(t)

	got, err := Resolve(string(filepath.Separator))
	if err != nil {
		t.Fatalf("Resolve(%q) returned error: %v", string(filepath.Separator), err)
	}
	if got == "" {
		t.Fatal("Resolve(root) returned an empty workspace name")
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	clearEnv(t)
	f := newFixture(t)

	dir := f.path("repo-a", "vendor", "repo-b", "lib")

	first, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve(%q) returned error: %v", dir, err)
	}
	for i := 0; i < 10; i++ {
		got, err := Resolve(dir)
		if err != nil {
			t.Fatalf("Resolve(%q) returned error: %v", dir, err)
		}
		if got != first {
			t.Fatalf("Resolve(%q) = %q on call %d, want stable %q", dir, got, i, first)
		}
	}
}
