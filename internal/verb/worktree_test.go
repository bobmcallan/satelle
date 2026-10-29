package verb_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

func wtGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// wtFixture is a main tree that gitignores ".toolA/" (dir-only), ".toolB/" and
// ".env", all present, with a .satelle data dir that is also ignored.
func wtFixture(t *testing.T) string {
	t.Helper()
	main := filepath.Join(t.TempDir(), "main")
	for _, d := range []string{".toolA", ".toolB", ".satelle"} {
		if err := os.MkdirAll(filepath.Join(main, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, s := range map[string]string{
		".gitignore":          ".toolA/\n.toolB/\n.env\n.satelle/\n",
		".toolA/a":            "A\n",
		".toolB/b":            "B\n",
		".env":                "SECRET=1\n",
		".satelle/satelle.db": "db\n",
		"README.md":           "r\n",
	} {
		if err := os.WriteFile(filepath.Join(main, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wtGit(t, main, "init", "-q", "-b", "main")
	wtGit(t, main, "config", "user.email", "t@example.com")
	wtGit(t, main, "config", "user.name", "t")
	wtGit(t, main, "add", "-A")
	wtGit(t, main, "commit", "-q", "-m", "A")
	return main
}

// declare wires the verb with the given [worktree] declaration, as the process
// config would.
func declare(t *testing.T, main string, w config.WorktreeConfig) {
	t.Helper()
	withWiring(t)
	verb.SetWorktreeConfig(config.Config{Worktree: w}, main)
}

func openWT(t *testing.T, req map[string]any) (verb.WorktreeResult, error) {
	t.Helper()
	raw, err := dispatchRaw(t, "story-worktree", req)
	var res verb.WorktreeResult
	if err == nil {
		if jerr := json.Unmarshal(raw, &res); jerr != nil {
			t.Fatal(jerr)
		}
	}
	return res, err
}

func wtExists(p string) bool { _, err := os.Lstat(p); return err == nil }

func tmplFor(t *testing.T, include ...string) config.WorktreeConfig {
	return config.WorktreeConfig{Include: include, Branch: "work/{id}", Path: "../trees/{id}"}
}

// AC1: the worktree carries every declared gitignored path, a declared path the
// main tree lacks is named, and nothing becomes dirty or stageable.
func TestStoryWorktreeCarriesDeclaredPaths(t *testing.T) {
	main := wtFixture(t)
	declare(t, main, tmplFor(t, ".toolA", ".env", ".absent"))

	res, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err != nil {
		t.Fatalf("story-worktree: %v", err)
	}
	if want := filepath.Join(filepath.Dir(main), "trees", "sty_1"); res.Path != want {
		t.Errorf("path = %q, want %q", res.Path, want)
	}
	if res.Branch != "work/sty_1" {
		t.Errorf("branch = %q", res.Branch)
	}
	if b, _ := os.ReadFile(filepath.Join(res.Path, ".toolA", "a")); string(b) != "A\n" {
		t.Errorf(".toolA/a = %q, want the main tree's content", b)
	}
	if b, _ := os.ReadFile(filepath.Join(res.Path, ".env")); string(b) != "SECRET=1\n" {
		t.Errorf(".env = %q", b)
	}
	if len(res.Missing) != 1 || res.Missing[0] != ".absent" {
		t.Errorf("missing = %v, want [.absent] reported by name", res.Missing)
	}
	if wtExists(filepath.Join(res.Path, ".satelle")) {
		t.Error("the data dir was carried into the worktree")
	}
	for _, dir := range []string{main, res.Path} {
		if st := wtGit(t, dir, "status", "--porcelain"); st != "" {
			t.Errorf("status in %s is not clean:\n%s", dir, st)
		}
	}
	wtGit(t, res.Path, "add", "-A")
	if st := wtGit(t, res.Path, "status", "--porcelain"); st != "" {
		t.Errorf("git add -A staged a carried path:\n%s", st)
	}
}

func TestStoryWorktreeRefusesAnUnignoredEntryByName(t *testing.T) {
	main := wtFixture(t)
	declare(t, main, tmplFor(t, "README.md"))
	_, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err == nil || !strings.Contains(err.Error(), `"README.md"`) {
		t.Fatalf("err = %v, want README.md refused by name", err)
	}
	if !strings.Contains(err.Error(), "git worktree remove") {
		t.Errorf("err = %v, want the cleanup commands named", err)
	}
	if wtExists(filepath.Join(filepath.Dir(main), "trees", "sty_1", "README.md")) {
		// The tracked README is there as content; it must not be a link.
		if fi, _ := os.Lstat(filepath.Join(filepath.Dir(main), "trees", "sty_1", "README.md")); fi.Mode()&os.ModeSymlink != 0 {
			t.Error("an unignored path was linked")
		}
	}
}

func TestStoryWorktreeBaseIsExplicitAndResolved(t *testing.T) {
	main := wtFixture(t)
	declare(t, main, tmplFor(t))
	if err := os.WriteFile(filepath.Join(main, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	wtGit(t, main, "checkout", "-q", "-b", "feature")
	wtGit(t, main, "add", "-A")
	wtGit(t, main, "commit", "-q", "-m", "B")
	featureHead := wtGit(t, main, "rev-parse", "feature")
	wtGit(t, main, "checkout", "-q", "main")

	if _, err := openWT(t, map[string]any{"id": "sty_1"}); err == nil || !strings.Contains(err.Error(), "--base") {
		t.Errorf("omitted base: err = %v, want --base named", err)
	} else {
		for _, want := range []string{"trunk", "help worktree"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("omitted base: err = %v, want %q", err, want)
			}
		}
	}
	if _, err := openWT(t, map[string]any{"id": "sty_1", "base": "nope"}); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("unknown base: err = %v", err)
	}
	res, err := openWT(t, map[string]any{"id": "sty_2", "base": "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if got := wtGit(t, res.Path, "rev-parse", "HEAD"); got != featureHead {
		t.Errorf("worktree HEAD = %s, want the base's commit %s", got, featureHead)
	}
}

func TestStoryWorktreeRefusesAnExistingBranch(t *testing.T) {
	main := wtFixture(t)
	declare(t, main, tmplFor(t))
	wtGit(t, main, "branch", "work/sty_1")
	before := wtGit(t, main, "rev-parse", "work/sty_1")
	_, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err == nil || !strings.Contains(err.Error(), `"work/sty_1"`) {
		t.Fatalf("err = %v, want the existing branch named", err)
	}
	if after := wtGit(t, main, "rev-parse", "work/sty_1"); after != before {
		t.Error("the existing branch moved")
	}
	if wtExists(filepath.Join(filepath.Dir(main), "trees", "sty_1")) {
		t.Error("a worktree was created over an existing branch")
	}
}

func TestStoryWorktreeWithNoTemplatesNamesTheFlags(t *testing.T) {
	main := wtFixture(t)
	declare(t, main, config.WorktreeConfig{})
	_, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err == nil || !strings.Contains(err.Error(), "--branch") {
		t.Fatalf("err = %v, want --branch required", err)
	}
	_, err = openWT(t, map[string]any{"id": "sty_1", "base": "main", "branch": "b1"})
	if err == nil || !strings.Contains(err.Error(), "--path") {
		t.Fatalf("err = %v, want --path required", err)
	}
	flagPath := filepath.Join(t.TempDir(), "tree")
	res, err := openWT(t, map[string]any{"id": "sty_1", "base": "main", "branch": "b1", "path": flagPath})
	if err != nil || res.Branch != "b1" || res.Path != flagPath {
		t.Fatalf("flags alone: res=%+v err=%v", res, err)
	}
}

func TestStoryWorktreeExistingIsIdempotentAndNeverDeletes(t *testing.T) {
	main := wtFixture(t)
	declare(t, main, config.WorktreeConfig{})
	// A worktree made the old way, with a hand-copied folder in it.
	hand := filepath.Join(t.TempDir(), "hand")
	wtGit(t, main, "worktree", "add", "-q", "-b", "hand", hand, "main")
	if err := os.MkdirAll(filepath.Join(hand, ".toolA"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hand, ".toolA", "a"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	declare(t, main, config.WorktreeConfig{Include: []string{".toolA", ".env"}})
	res, err := openWT(t, map[string]any{"id": "sty_1", "existing": hand})
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, e := range res.Entries {
		status[e.Entry] = e.Status
	}
	if !strings.Contains(status[".toolA"], "not a link") || status[".env"] != "carried" {
		t.Errorf("first run = %v", status)
	}
	if b, _ := os.ReadFile(filepath.Join(hand, ".toolA", "a")); string(b) != "stale" {
		t.Error("the hand-copied folder was altered or deleted")
	}

	res, err = openWT(t, map[string]any{"id": "sty_1", "existing": hand})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Entries {
		if e.Entry == ".env" && e.Status != "already carried" {
			t.Errorf("second run .env = %q, want already carried", e.Status)
		}
	}
	// The operator deletes the copy, runs again, and the link takes its place.
	if err := os.RemoveAll(filepath.Join(hand, ".toolA")); err != nil {
		t.Fatal(err)
	}
	if _, err := openWT(t, map[string]any{"id": "sty_1", "existing": hand}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(hand, ".toolA", "a")); string(b) != "A\n" {
		t.Errorf(".toolA/a = %q after the copy was replaced", b)
	}

	// --existing creates nothing and rejects creation flags and foreign trees.
	if _, err := openWT(t, map[string]any{"id": "sty_1", "existing": hand, "base": "main"}); err == nil {
		t.Error("--existing with --base was accepted")
	}
	if _, err := openWT(t, map[string]any{"id": "sty_1", "existing": main}); err == nil || !strings.Contains(err.Error(), "main tree") {
		t.Errorf("--existing on the main tree: %v", err)
	}
	if _, err := openWT(t, map[string]any{"id": "sty_1", "existing": t.TempDir()}); err == nil {
		t.Error("--existing on a non-repository was accepted")
	}
}

// AC3: only the declaration changes between runs; the same binary carries a
// different set each time, and an empty declaration carries nothing.
func TestStoryWorktreeFollowsTheDeclarationAlone(t *testing.T) {
	main := wtFixture(t)

	declare(t, main, tmplFor(t, ".toolA"))
	one, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !wtExists(filepath.Join(one.Path, ".toolA")) || wtExists(filepath.Join(one.Path, ".toolB")) {
		t.Error("worktree 1 should carry .toolA and not .toolB")
	}

	declare(t, main, tmplFor(t, ".toolB"))
	two, err := openWT(t, map[string]any{"id": "sty_2", "base": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !wtExists(filepath.Join(two.Path, ".toolB")) || wtExists(filepath.Join(two.Path, ".toolA")) {
		t.Error("worktree 2 should carry .toolB and not .toolA")
	}

	declare(t, main, tmplFor(t))
	three, err := openWT(t, map[string]any{"id": "sty_3", "base": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if wtExists(filepath.Join(three.Path, ".toolA")) || wtExists(filepath.Join(three.Path, ".toolB")) || wtExists(filepath.Join(three.Path, ".env")) {
		t.Error("an empty declaration carried something")
	}
	if st := wtGit(t, three.Path, "status", "--porcelain"); st != "" {
		t.Errorf("worktree 3 is not clean:\n%s", st)
	}

	// Branch and location follow their templates too.
	declare(t, main, config.WorktreeConfig{Branch: "other/{id}", Path: "../elsewhere/{id}"})
	four, err := openWT(t, map[string]any{"id": "sty_4", "base": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if four.Branch != "other/sty_4" || !strings.HasSuffix(four.Path, filepath.Join("elsewhere", "sty_4")) {
		t.Errorf("templates ignored: %+v", four)
	}
}

// The verb and the mechanism hold no repo convention: no branch prefix and no
// worktree directory name appears in Go.
func TestWorktreeCodeNamesNoRepoConvention(t *testing.T) {
	for _, p := range []string{"worktree.go", "../worktree/worktree.go", "../config/worktree.go"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range []string{"epic/", "-wt", ".claude", ".grok", ".pi\""} {
			if strings.Contains(string(b), lit) {
				t.Errorf("%s names %q, a repo convention that belongs in configuration", p, lit)
			}
		}
	}
}

// wiringRepo is a main tree holding files (path -> content) beside gitignore;
// `git add -A` commits whatever the gitignore does not cover. The user's global
// excludes file is masked so it cannot decide what the fixture tracks.
func wiringRepo(t *testing.T, gitignore string, files map[string]string) string {
	t.Helper()
	main := filepath.Join(t.TempDir(), "main")
	files[".gitignore"] = gitignore
	files["README.md"] = "r\n"
	for p, s := range files {
		full := filepath.Join(main, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wtGit(t, main, "init", "-q", "-b", "main")
	wtGit(t, main, "config", "user.email", "t@example.com")
	wtGit(t, main, "config", "user.name", "t")
	wtGit(t, main, "config", "core.excludesFile", os.DevNull)
	wtGit(t, main, "add", "-A")
	wtGit(t, main, "commit", "-q", "-m", "A")
	return main
}

func entryStatus(res verb.WorktreeResult) map[string]string {
	out := map[string]string{}
	for _, e := range res.Entries {
		out[e.Entry] = e.Status
	}
	return out
}

// sty_0dca3223 AC1: a gitignored pi extension is carried with no include entry.
func TestStoryWorktreeCarriesPiGateWiringUnlisted(t *testing.T) {
	main := wiringRepo(t, ".pi/\n", map[string]string{".pi/extensions/satelle.ts": "ext\n"})
	declare(t, main, tmplFor(t))

	res, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err != nil {
		t.Fatalf("story-worktree: %v", err)
	}
	link := filepath.Join(res.Path, ".pi", "extensions", "satelle.ts")
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a link (err %v)", link, err)
	}
	if b, _ := os.ReadFile(link); string(b) != "ext\n" {
		t.Errorf("carried extension = %q, want the main tree's content", b)
	}
	if got := entryStatus(res)[filepath.FromSlash(".pi/extensions/satelle.ts")]; got != "carried" {
		t.Errorf("entries = %+v, want .pi/extensions/satelle.ts carried", res.Entries)
	}
	if st := wtGit(t, res.Path, "status", "--porcelain"); st != "" {
		t.Errorf("worktree is not clean:\n%s", st)
	}
}

// sty_0dca3223 AC2: every harness's resolved gate_wiring is carried, embedded
// and operator-declared alike.
func TestStoryWorktreeCarriesEachHarnessGateWiring(t *testing.T) {
	for _, tc := range []struct{ harness, path string }{
		{"claude", ".claude/settings.json"},
		{"grok", ".grok/hooks/satelle.json"},
		{"pi", ".pi/extensions/satelle.ts"},
		{"cursor", ".cursor/hooks.json"},
		{"mybot", ".mybot/hooks.json"},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			dir := strings.SplitN(tc.path, "/", 2)[0]
			main := wiringRepo(t, dir+"/\n", map[string]string{tc.path: "w\n"})
			withWiring(t)
			verb.SetWorktreeConfig(config.Config{
				Worktree: tmplFor(t),
				Harness:  map[string]config.HarnessConfig{"mybot": {GateWiring: []string{".mybot/hooks.json"}}},
			}, main)

			res, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
			if err != nil {
				t.Fatalf("story-worktree: %v", err)
			}
			if got := entryStatus(res)[filepath.FromSlash(tc.path)]; got != "carried" {
				t.Errorf("entries = %+v, want %s carried", res.Entries, tc.path)
			}
			if b, _ := os.ReadFile(filepath.Join(res.Path, filepath.FromSlash(tc.path))); string(b) != "w\n" {
				t.Errorf("%s in worktree = %q", tc.path, b)
			}
		})
	}
}

// sty_0dca3223 AC2: a repo's [harness.<name>] gate_wiring overrides the
// embedded path for what is carried.
func TestStoryWorktreeCarriesOverriddenGateWiring(t *testing.T) {
	main := wiringRepo(t, ".claude/\n", map[string]string{
		".claude/settings.json":    "default\n",
		".claude/custom-hook.json": "custom\n",
	})
	withWiring(t)
	verb.SetWorktreeConfig(config.Config{
		Worktree: tmplFor(t),
		Harness:  map[string]config.HarnessConfig{"claude": {GateWiring: []string{".claude/custom-hook.json"}}},
	}, main)
	res, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err != nil {
		t.Fatal(err)
	}
	st := entryStatus(res)
	if st[filepath.FromSlash(".claude/custom-hook.json")] != "carried" {
		t.Errorf("entries = %+v, want the override carried", res.Entries)
	}
	if _, ok := st[filepath.FromSlash(".claude/settings.json")]; ok {
		t.Errorf("entries = %+v, the overridden embedded path was carried", res.Entries)
	}
}

// sty_0dca3223 AC3: absent wiring is skipped and not reported missing.
func TestStoryWorktreeSkipsAbsentGateWiring(t *testing.T) {
	main := wiringRepo(t, ".pi/\n", map[string]string{})
	declare(t, main, tmplFor(t))
	res, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err != nil {
		t.Fatalf("story-worktree: %v", err)
	}
	if len(res.Entries) != 0 || len(res.Missing) != 0 {
		t.Errorf("entries = %+v, missing = %v, want both empty", res.Entries, res.Missing)
	}
}

// sty_0dca3223 AC3: tracked wiring arrives through git and is not carried.
func TestStoryWorktreeSkipsTrackedGateWiring(t *testing.T) {
	main := wiringRepo(t, "", map[string]string{".claude/settings.json": "tracked\n"})
	declare(t, main, tmplFor(t))
	res, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err != nil {
		t.Fatalf("story-worktree: %v", err)
	}
	if len(res.Entries) != 0 || len(res.Missing) != 0 {
		t.Errorf("entries = %+v, missing = %v, want both empty", res.Entries, res.Missing)
	}
	p := filepath.Join(res.Path, ".claude", "settings.json")
	if fi, err := os.Lstat(p); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("%s should be the checked-out file, not a link (err %v)", p, err)
	}
}

// sty_0dca3223 AC3: an include entry that covers the wiring (itself or an
// ancestor directory) carries it once.
func TestStoryWorktreeIncludeCoversGateWiringOnce(t *testing.T) {
	main := wiringRepo(t, ".claude/\n", map[string]string{".claude/settings.json": "w\n"})
	declare(t, main, tmplFor(t, ".claude"))
	res, err := openWT(t, map[string]any{"id": "sty_1", "base": "main"})
	if err != nil {
		t.Fatalf("story-worktree: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Entry != ".claude" || res.Entries[0].Status != "carried" {
		t.Errorf("entries = %+v, want exactly .claude carried", res.Entries)
	}
	if fi, err := os.Lstat(filepath.Join(res.Path, ".claude")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".claude should be the one link (err %v)", err)
	}

	declare(t, main, tmplFor(t, ".claude/settings.json"))
	res, err = openWT(t, map[string]any{"id": "sty_2", "base": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 {
		t.Errorf("entries = %+v, want the exact include entry alone", res.Entries)
	}
}
