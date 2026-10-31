package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/doctor"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/verb"
)

func TestInitChangedAreas(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Sync: map[string]string{"settings": "personal", "skills": "personal"}}
	files := func(paths ...string) []verb.SeededFile {
		var out []verb.SeededFile
		for _, p := range paths {
			out = append(out, verb.SeededFile{Path: p})
		}
		return out
	}
	for _, c := range []struct {
		name    string
		changed []verb.SeededFile
		want    []string
	}{
		{"nothing changed", nil, nil},
		{"satelle.toml is settings", files(".satelle/satelle.toml"), []string{"settings"}},
		{"a skill file is skills", files(".satelle/skills/mine.md"), []string{"skills"}},
		{"areas are de-duplicated and sorted", files(".satelle/skills/a.md", ".satelle/satelle.toml", ".satelle/skills/b.md"), []string{"settings", "skills"}},
		{"a local area is not synced", files(".satelle/documents/doc.md", ".satelle/constitution.md"), nil},
		{"files no area owns", files(".satelle/deployed.version", ".satelle/hooks/satelle-hook.sh"), nil},
	} {
		if got := initChangedAreas(cfg, repo, c.changed); !slices.Equal(got, c.want) {
			t.Errorf("%s: areas = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestInitPushCmdBindsRoot: the push init runs is this binary's own `sync push`,
// bound to the healed root by its working directory and SATELLE_CONFIG, so the
// child's pre-run opens that root's store and no other.
func TestInitPushCmdBindsRoot(t *testing.T) {
	root := t.TempDir()
	c, err := initPushCmd(root)
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if c.Path != exe || !slices.Equal(c.Args[1:], []string{"sync", "push", "--dry-run=false"}) {
		t.Errorf("push command = %s %v, want %s sync push --dry-run=false", c.Path, c.Args, exe)
	}
	if c.Dir != root {
		t.Errorf("push must run in the root: Dir = %q, want %q", c.Dir, root)
	}
	want := "SATELLE_CONFIG=" + filepath.Join(root, ".satelle", "satelle.toml")
	var bound string // the last assignment is the one the child sees
	for _, kv := range c.Env {
		if strings.HasPrefix(kv, "SATELLE_CONFIG=") {
			bound = kv
		}
	}
	if bound != want {
		t.Errorf("push must bind SATELLE_CONFIG to the root's satelle.toml (%s): %v", want, c.Env)
	}
}

const initPushFailedLine = "init changed synced substrate (settings) but the push failed — run `satelle sync push`"

// TestInitPushesChangedSubstrate (AC7): an init that changes synced substrate in
// a bound repo publishes it through `satelle sync push`, so doctor shows no
// sync.substrate_unpushed after one init; an init that changes nothing pushes
// nothing and says nothing; and a failing push ends init's output with one line
// naming the areas and the command, while init itself succeeds.
func TestInitPushesChangedSubstrate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())
	ts, f := newFakeSyncServer(t)
	seedCred(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	syncedToml := func(project string) string {
		return fmt.Sprintf("[sync]\nproject = %q\nskills = \"personal\"\nsettings = \"personal\"\nstale_after = \"24h\"\n%s = \"1h\"\n",
			project, retiredHoldStaleAfterKey)
	}
	unpushed := func(repo string) []health.Finding {
		var out []health.Finding
		for _, f := range doctor.CheckSubstrateSync(repo, filepath.Join(repo, ".satelle"), config.GlobalDir(), time.Now()) {
			if f.ID == health.IDSyncSubstrateUnpushed {
				out = append(out, f)
			}
		}
		return out
	}
	// publish pushes repo by hand so its synced base holds the retired key init
	// will remove — the estate a breaking release's heal starts from.
	publish := func(repo string) {
		t.Helper()
		pointAt(t, repo)
		if out, err := runRoot(t, "sync", "push", "--dry-run=false"); err != nil {
			t.Fatalf("baseline push: %v\n%s", err, out)
		}
		if got := unpushed(repo); len(got) != 0 {
			t.Fatalf("baseline must be in step: %v", got)
		}
	}

	// Control: the same heal with no push leaves the settings area unpushed, so
	// the assertions below fail on an init that only rewrites satelle.toml.
	control := syncConfigRepo(t, syncedToml("probe-control"))
	publish(control)
	if err := runInit(io.Discard, control, true, nil); err != nil {
		t.Fatalf("control init: %v", err)
	}
	if got := unpushed(control); len(got) == 0 {
		t.Fatal("guard: init's rewrite of satelle.toml must leave settings unpushed when nothing pushes it")
	}

	repo := syncConfigRepo(t, syncedToml("probe"))
	publish(repo)
	var roots []string
	prev := initPush
	t.Cleanup(func() { initPush = prev })
	initPush = func(out io.Writer, root string) error {
		roots = append(roots, root)
		o, err := runRoot(t, "sync", "push", "--dry-run=false")
		fmt.Fprint(out, o)
		return err
	}

	var first strings.Builder
	if err := runInit(&first, repo, true, nil); err != nil {
		t.Fatalf("init: %v\n%s", err, first.String())
	}
	if !slices.Equal(roots, []string{repo}) {
		t.Fatalf("one init that changed settings must push once, bound to the repo: %v\n%s", roots, first.String())
	}
	if body, _, ok := f.cfg.get("probe", config.ConfigName, 0); !ok || strings.Contains(string(body), retiredHoldStaleAfterKey) {
		t.Errorf("the hosted settings must be the healed satelle.toml (ok=%v):\n%s", ok, body)
	}
	if got := unpushed(repo); len(got) != 0 {
		t.Errorf("after one init doctor must show no sync.substrate_unpushed: %v", got)
	}
	if strings.Contains(first.String(), "push failed") {
		t.Errorf("a push that succeeded must print no failure line:\n%s", first.String())
	}

	var second strings.Builder
	if err := runInit(&second, repo, true, nil); err != nil {
		t.Fatalf("second init: %v", err)
	}
	if len(roots) != 1 || strings.Contains(second.String(), "push") {
		t.Errorf("an init that changed nothing must push nothing and print no push line (pushes=%d):\n%s", len(roots), second.String())
	}

	ts.Close()
	tomlPath := filepath.Join(repo, ".satelle", config.ConfigName)
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tomlPath, []byte(strings.Replace(string(raw), "[sync]\n", "[sync]\n"+retiredHoldStaleAfterKey+" = \"1h\"\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	var third strings.Builder
	if err := runInit(&third, repo, true, nil); err != nil {
		t.Fatalf("a failed push must not fail init: %v\n%s", err, third.String())
	}
	lines := strings.Split(strings.TrimRight(third.String(), "\n"), "\n")
	if last := lines[len(lines)-1]; last != initPushFailedLine {
		t.Errorf("init's output must end with the push-failed line:\n got %q\nwant %q\n%s", last, initPushFailedLine, third.String())
	}
	if n := strings.Count(third.String(), "push failed"); n != 1 {
		t.Errorf("exactly one push-failed line, got %d:\n%s", n, third.String())
	}
}
