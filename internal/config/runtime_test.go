package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoKey_StableAcrossCWDAndSlashForms(t *testing.T) {
	dir := t.TempDir()
	// Create a real directory so Abs/EvalSymlinks behave.
	repo := filepath.Join(dir, "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	k1 := RepoKey(repo)
	k2 := RepoKey(repo + string(filepath.Separator))
	k3 := RepoKey(filepath.Join(dir, "myrepo", ".", "..", "myrepo"))
	if k1 == "" || k1 != k2 || k1 != k3 {
		t.Fatalf("keys differ across path forms: %q %q %q", k1, k2, k3)
	}
	if !strings.HasPrefix(k1, "myrepo-") {
		t.Errorf("key %q should start with basename", k1)
	}
	// Two distinct repos → distinct keys.
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if RepoKey(other) == k1 {
		t.Fatalf("distinct repos share key %q", k1)
	}
}

func TestRepoKey_SymlinkResolvesSame(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "real")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, want := RepoKey(link), RepoKey(repo); got != want {
		t.Fatalf("symlink key %q != real key %q", got, want)
	}
}

func TestRepoKey_WorktreesShareKey(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(main, "init")
	// Some git versions need an initial commit before worktree add.
	if err := os.WriteFile(filepath.Join(main, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(main, "add", "f")
	run(main, "commit", "-m", "init")
	wt := filepath.Join(dir, "wt")
	run(main, "worktree", "add", wt, "HEAD")

	kMain := RepoKey(main)
	kWT := RepoKey(wt)
	if kMain != kWT {
		t.Fatalf("worktree key %q != main key %q", kWT, kMain)
	}
}

func TestResolveRuntimeDir_FreshGoesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	cfg := Config{}
	res := cfg.ResolveRuntimeDir(repo)
	if res.Legacy {
		t.Fatal("fresh repo should not be legacy")
	}
	wantPrefix := filepath.Join(home, RepoKey(repo))
	if res.Dir != wantPrefix {
		t.Fatalf("runtime dir = %q, want %q", res.Dir, wantPrefix)
	}
	if cfg.ResolveRuntimeDB(repo) != filepath.Join(wantPrefix, DefaultDBName) {
		t.Fatalf("runtime db path wrong: %s", cfg.ResolveRuntimeDB(repo))
	}
	if note := cfg.LegacyRuntimeNote(repo); note != "" {
		t.Errorf("fresh should have no deprecation note, got %q", note)
	}
}

func TestResolveRuntimeDir_LegacyDetected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	dataDir := filepath.Join(repo, DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyDB := filepath.Join(dataDir, DefaultDBName)
	if err := os.WriteFile(legacyDB, []byte("sqlite-placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{}
	res := cfg.ResolveRuntimeDir(repo)
	if !res.Legacy {
		t.Fatal("expected legacy=true when only in-repo db exists")
	}
	if res.Dir != dataDir {
		t.Fatalf("legacy dir = %q, want %q", res.Dir, dataDir)
	}
	if note := cfg.LegacyRuntimeNote(repo); note == "" || !strings.Contains(note, "runtime migrate") {
		t.Fatalf("expected migrate note, got %q", note)
	}
}

func TestResolveRuntimeDir_HomeWinsOverLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	dataDir := filepath.Join(repo, DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, DefaultDBName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	homeDir := filepath.Join(home, RepoKey(repo))
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, DefaultDBName), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{}
	res := cfg.ResolveRuntimeDir(repo)
	if res.Legacy || res.Dir != homeDir {
		t.Fatalf("home should win: %+v", res)
	}
}

func TestResolveRuntimeDir_ExplicitDBWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	custom := filepath.Join(repo, "custom", "mine.db")
	cfg := Config{DB: custom}
	res := cfg.ResolveRuntimeDir(repo)
	if res.Legacy {
		t.Fatal("explicit db is not legacy")
	}
	if res.Dir != filepath.Dir(custom) {
		t.Fatalf("dir = %q, want parent of explicit db", res.Dir)
	}
	// ResolveDB still honors the explicit path for the filename.
	if got := cfg.ResolveDB(repo); got != custom && got != filepath.Clean(custom) {
		// resolveUnder may abs-join; accept either cleaned form under repo.
		if !strings.HasSuffix(got, filepath.Join("custom", "mine.db")) {
			t.Fatalf("ResolveDB = %q, want explicit", got)
		}
	}
}

func TestResolveDB_DefaultsToHomeKeyed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	cfg := Config{}
	got := cfg.ResolveDB(repo)
	want := filepath.Join(home, RepoKey(repo), DefaultDBName)
	if got != want {
		t.Fatalf("ResolveDB = %q, want %q", got, want)
	}
}

func TestResolveRuntimeDir_KeepsPathKeyAfterGitInit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	// First open as non-git → path-key DB.
	pathKey := pathRepoKey(repo)
	pathDir := filepath.Join(home, pathKey)
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pathDir, DefaultDBName), []byte("path-db"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Now the tree becomes a git repo (RepoKey would flip without continuity).
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-m", "init")

	gitKey := RepoKey(repo)
	if gitKey == pathKey {
		t.Skip("git and path keys coincide in this environment")
	}
	res := Config{}.ResolveRuntimeDir(repo)
	if res.Dir != pathDir {
		t.Fatalf("after git init, want to keep path-key dir %q, got %q (git key would be %s)",
			pathDir, res.Dir, gitKey)
	}
}
