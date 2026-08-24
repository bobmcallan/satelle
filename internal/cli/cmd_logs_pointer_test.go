package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

func TestRunInitPlantsLogsPointer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink pointer")
	}
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	link := filepath.Join(repo, ".satelle", "logs")
	st, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("want symlink")
	}
	cfg, _, err := config.Load(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	rt := cfg.ResolveLogsDir(repo)
	if err := os.MkdirAll(filepath.Join(rt, "dispatch"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte("planted-through-pointer\n")
	if err := os.WriteFile(filepath.Join(rt, "dispatch", "x.log"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(link, "dispatch", "x.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("read-through = %q", got)
	}
}

func TestPointerIsSymlinkOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink pointer")
	}
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(repo, ".satelle", "logs"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatal(".satelle/logs must be a symlink, not a directory")
	}
}

func TestInitPointerIsGitIgnored_TrackedSatelle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatal(err)
	}
	gi, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if !strings.Contains(string(gi), ".satelle/logs\n") {
		t.Fatalf("managed block missing pointer:\n%s", gi)
	}
	for _, args := range [][]string{{"init"}, {"check-ignore", ".satelle/logs"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil && args[0] == "check-ignore" {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		} else if err != nil && args[0] == "init" {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
}

func TestInitPointerNoExtraEntry_IgnoredSatelle(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".satelle/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatal(err)
	}
	gi, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	// The operator line remains; the managed block must not add .satelle/logs.
	for _, line := range strings.Split(string(gi), "\n") {
		if strings.TrimSpace(line) == ".satelle/logs" {
			t.Fatalf("ignore-all repo must not gain pointer entry:\n%s", gi)
		}
	}
}

func TestListLegacyResidueIgnoresLogsPointer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "logs")); err != nil {
		t.Fatal(err)
	}
	got := listLegacyResidue(dir)
	for _, n := range got {
		if strings.HasPrefix(n, "logs") {
			t.Fatalf("pointer reported as residue: %v", got)
		}
	}
	if err := os.Remove(filepath.Join(dir, "logs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	got = listLegacyResidue(dir)
	found := false
	for _, n := range got {
		if n == "logs/" {
			found = true
		}
	}
	if !found {
		t.Fatalf("real logs dir must be residue: %v", got)
	}
}

func TestScaffoldTomlNamesLogsLocationAndPointer(t *testing.T) {
	if !strings.Contains(scaffoldToml, "~/.satelle/<repo-key>/logs") {
		t.Error("seed must name runtime location")
	}
	if !strings.Contains(scaffoldToml, ".satelle/logs") {
		t.Error("seed must name the pointer")
	}
}

func TestLogsCommentGuard(t *testing.T) {
	root := ".."
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			trim := strings.TrimSpace(line)
			if !strings.HasPrefix(trim, "//") && !strings.HasPrefix(trim, "*") {
				continue
			}
			if !strings.Contains(line, ".satelle/logs") {
				continue
			}
			if strings.Contains(line, "pointer") || strings.Contains(line, "symlink") {
				continue
			}
			t.Errorf("%s:%d storage claim without pointer: %s", path, i+1, trim)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReindexDoesNotIndexPointerLogs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink pointer")
	}
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(repo, ".satelle", "logs", "dispatch", "leak.md")
	if err := os.MkdirAll(filepath.Dir(md), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(md, []byte("---\nname: leak\ntype: skill\n---\n# leak\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	rout, err := runRootIn(t, "", "reindex")
	if err != nil {
		t.Fatalf("reindex: %v\n%s", err, rout)
	}
	if strings.Contains(rout, "leak") || strings.Contains(strings.ToLower(rout), ".satelle/logs") {
		t.Fatalf("reindex mentioned pointer content:\n%s", rout)
	}
	for kind, dir := range cfg.ResolveAuthoredDirs(repo) {
		_ = kind
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repo, path)
			if strings.Contains(filepath.ToSlash(rel), ".satelle/logs") {
				t.Errorf("authored walk saw pointer path %s", rel)
			}
			return nil
		})
	}
}
