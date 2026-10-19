package snapsync

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// ignoringDriver is testDriver wired as the CLI wires it: the same Ignore that
// the driver filters its inputs with also decides which hosted heads an area owns.
func ignoringDriver(t *testing.T, h *memHosted, ignoreFile string) *Driver {
	t.Helper()
	d := testDriver(t, h, nil)
	d.Ignore = config.ParseIgnore(ignoreFile)
	d.Owns = func(area, p string) bool { return strings.HasPrefix(p, area+"/") && !d.Ignore.Match(p) }
	return d
}

// A path .satelleignore lists is never a difference: the hosted snapshot, the
// hosted head and the recorded base may all hold it with other content than the
// tree's (or the tree may hold no copy at all), and the area still reads in step.
func TestStatusIgnoredPathIsNeverADifference(t *testing.T) {
	const keep, secret = "skills/a.md", "skills/secret.md"
	h := newMemHosted()
	h.snapshot(map[string]string{keep: "a\n", secret: "hosted\n"})
	local := map[string]string{keep: SHA([]byte("a\n"))}

	// Without the ignore the base holds a file the tree lacks: a difference. This
	// is what makes the next assertion a proof rather than a tautology.
	plain := testDriver(t, h, nil)
	saveBase(t, plain, map[string]string{keep: "a\n", secret: "base\n"})
	if st := statusOf(t, plain, local); st.State == StateInStep {
		t.Fatalf("without an ignore the state = %q, want a difference", st.State)
	}

	d := ignoringDriver(t, h, "secret.md\n")
	saveBase(t, d, map[string]string{keep: "a\n", secret: "base\n"})
	if st := statusOf(t, d, local); st.State != StateInStep {
		t.Fatalf("with secret.md ignored the state = %q (pending %v), want %q", st.State, st.Pending, StateInStep)
	}
}

// The ignored path's hosted copy is not a pull entry either, and the filter does
// not touch the base that is saved back.
func TestPlanPullDropsIgnoredPaths(t *testing.T) {
	const keep, secret = "skills/a.md", "skills/secret.md"
	h := newMemHosted()
	h.snapshot(map[string]string{keep: "a\n", secret: "hosted\n"})
	d := ignoringDriver(t, h, "secret.md\n")
	saveBase(t, d, map[string]string{keep: "a\n", secret: "base\n"})

	job, err := d.PlanPull(context.Background(), "skills", map[string]string{keep: SHA([]byte("a\n"))})
	if err != nil || job == nil {
		t.Fatalf("PlanPull = %v, %v", job, err)
	}
	for _, e := range job.Plan.Entries {
		if e.Path == secret {
			t.Errorf("the plan names the ignored path: %+v", e)
		}
	}
	if _, ok := job.Plan.Base.Files[secret]; ok {
		t.Errorf("the plan's base carries the ignored path: %v", job.Plan.Base.Files)
	}
}

func TestLoadBaseFiltersACopyAndLeavesTheSavedBaseAlone(t *testing.T) {
	const keep, secret = "skills/a.md", "skills/secret.md"
	d := ignoringDriver(t, newMemHosted(), "secret.md\n")
	saveBase(t, d, map[string]string{keep: "a\n", secret: "s\n"})

	b, err := d.loadBase("skills")
	if err != nil || b == nil {
		t.Fatalf("loadBase = %v, %v", b, err)
	}
	if _, ok := b.Files[secret]; ok || len(b.Files) != 1 {
		t.Errorf("loadBase Files = %v, want only %s", b.Files, keep)
	}
	d.Ignore = config.Ignore{}
	again, err := d.loadBase("skills")
	if err != nil || again == nil {
		t.Fatal(err)
	}
	if _, ok := again.Files[secret]; !ok {
		t.Errorf("the saved base lost %s: %v", secret, again.Files)
	}
}

func TestEffectiveFiltersTheSnapshotRecord(t *testing.T) {
	const keep, secret = "skills/a.md", "skills/secret.md"
	h := newMemHosted()
	h.snapshot(map[string]string{keep: "a\n", secret: "s\n"})
	d := ignoringDriver(t, h, "secret.md\n")

	_, _, rec, err := d.effective(context.Background(), "skills", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{keep: SHA([]byte("a\n"))}; !reflect.DeepEqual(rec.Files, want) {
		t.Errorf("effective Files = %v, want %v", rec.Files, want)
	}
}

// The default config/.env exclusion applies to a driver built with no Ignore.
func TestZeroIgnoreStillDropsTheDefault(t *testing.T) {
	got := DropIgnored(config.Ignore{}.Match, map[string]string{"config/.env": "1", "skills/a.md": "2"})
	if want := map[string]string{"skills/a.md": "2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DropIgnored = %v, want %v", got, want)
	}
}

func TestDropIgnoredReturnsTheSameMapWhenNothingMatches(t *testing.T) {
	in := map[string]string{"skills/a.md": "1"}
	out := DropIgnored(config.ParseIgnore("secret.md\n").Match, in)
	in["skills/b.md"] = "2" // an aliased map sees the write
	if len(out) != 2 {
		t.Errorf("a map with nothing to drop must come back as is, got %v", out)
	}
	if got := DropIgnored(nil, in); len(got) != 2 {
		t.Errorf("a nil ignore keeps everything, got %v", got)
	}
}

func TestOfflineDriftIgnoresIgnoredPaths(t *testing.T) {
	const keep, secret = "skills/a.md", "skills/secret.md"
	hosted.DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "state.json")
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = "" })
	server, project, repo, data := "https://example.test", "p", "/repo", t.TempDir()
	if err := hosted.SaveAreaBase(server, project, repo, "skills", hosted.AreaBase{Version: 1, Files: map[string]string{keep: "1", secret: "base"}}); err != nil {
		t.Fatal(err)
	}

	local := map[string]string{keep: "1"}
	drift, err := OfflineDrift(server, project, repo, data, "skills", local, nil, config.Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if len(drift.Changed) == 0 {
		t.Fatal("without an ignore the deleted file is a change")
	}
	drift, err = OfflineDrift(server, project, repo, data, "skills", local, nil, config.ParseIgnore("secret.md\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(drift.Changed) != 0 {
		t.Errorf("Changed = %v, want none for an ignored path", drift.Changed)
	}

	// And a local copy that differs from the base (or has none) is not one either.
	drift, err = OfflineDrift(server, project, repo, data, "skills", map[string]string{keep: "1", secret: "edited"}, nil, config.ParseIgnore("secret.md\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(drift.Changed) != 0 {
		t.Errorf("Changed = %v, want none for an edited ignored path", drift.Changed)
	}
}
