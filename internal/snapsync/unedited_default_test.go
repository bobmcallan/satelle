package snapsync

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// memHosted is an in-memory hosted copy: snapshot records by version and blob
// heads by path, enough to drive a Driver without a server.
type memHosted struct {
	recs  []Record // recs[0] is version 1
	blobs map[string][]byte
}

func newMemHosted() *memHosted { return &memHosted{blobs: map[string][]byte{}} }

// snapshot appends a record naming exactly the given files and stores their bytes.
func (m *memHosted) snapshot(files map[string]string) {
	shas := map[string]string{}
	for p, body := range files {
		m.blobs[p] = []byte(body)
		shas[p] = SHA([]byte(body))
	}
	m.recs = append(m.recs, Record{Parent: len(m.recs), Nonce: "n", Files: shas})
}

// memRecords is the Records side of a memHosted.
type memRecords struct{ *memHosted }

func (m memRecords) Head(context.Context, string) (int, error) { return len(m.recs), nil }
func (m memRecords) Read(_ context.Context, _ string, v int) ([]byte, bool, error) {
	if v < 1 || v > len(m.recs) {
		return nil, false, nil
	}
	return m.recs[v-1].Encode(), true, nil
}
func (m memRecords) Put(context.Context, string, []byte) (int, error) { return 0, nil }

// memBlobs is the Blobs side of a memHosted.
type memBlobs struct{ *memHosted }

func (m memBlobs) Heads(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for p, b := range m.blobs {
		out[p] = SHA(b)
	}
	return out, nil
}
func (m memBlobs) Get(_ context.Context, p string) ([]byte, error) {
	b, ok := m.blobs[p]
	if !ok {
		return nil, ErrBlobMissing
	}
	return b, nil
}
func (m memBlobs) Put(context.Context, string, []byte) (bool, error) { return false, nil }

// markedDefault stands in for config.UneditedDefault: any body carrying DEFAULT.
func markedDefault(_ string, body []byte) bool { return strings.Contains(string(body), "DEFAULT") }

func testDriver(t *testing.T, h *memHosted, skip func(string, []byte) bool) *Driver {
	t.Helper()
	hosted.DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "state.json")
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = "" })
	return &Driver{
		Server: "https://example.test", Project: "p", RepoRoot: "/repo", DataDir: t.TempDir(),
		Records: memRecords{h}, Blobs: memBlobs{h}, SkipBody: skip,
		Owns: func(area, p string) bool { return strings.HasPrefix(p, area+"/") },
	}
}

func listDir(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// TestStageKeepsBinaryDefault (sty_097833e8 AC3): a hosted file the predicate calls
// an unedited default is not written over the tree and is not parked as a
// conflict; authored files still are.
func TestStageKeepsBinaryDefault(t *testing.T) {
	h := newMemHosted()
	h.snapshot(map[string]string{
		"principles/seed.md":     "---\n---\nDEFAULT older\n",
		"principles/clash.md":    "---\n---\nDEFAULT clash\n",
		"principles/authored.md": "authored\n",
	})
	d := testDriver(t, h, markedDefault)
	// The local side is the push-side view: a machine's own edit of clash.md is in
	// it, and the unedited seed is not.
	local := map[string]string{"principles/clash.md": SHA([]byte("my edit\n"))}
	job, err := d.PlanPull(context.Background(), "principles", local)
	if err != nil || job == nil {
		t.Fatalf("PlanPull: %v %v", job, err)
	}
	if job.Plan.Count(Conflict) != 1 || job.Plan.Count(Write) != 2 {
		t.Fatalf("test premise: want 1 conflict and 2 writes, got %+v", actions(job.Plan))
	}
	var out strings.Builder
	d.Out = &out
	writes, err := d.Stage(context.Background(), job)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	var got []string
	for _, w := range writes {
		got = append(got, w.Path)
	}
	if want := []string{"principles/authored.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}
	if tree := listDir(t, d.DataDir); len(tree) != 0 {
		t.Errorf("Stage put %v on disk — no conflict copy may be parked for a binary default", tree)
	}
	if job.Plan.Count(Conflict) != 0 || len(job.Plan.Base.Unmerged) != 0 {
		t.Errorf("a binary default must not stay a conflict: %+v unmerged=%v", actions(job.Plan), job.Plan.Base.Unmerged)
	}
	if want := []string{"principles/clash.md", "principles/seed.md"}; !reflect.DeepEqual(sorted(job.Kept), want) {
		t.Errorf("Kept = %v, want %v", job.Kept, want)
	}
	if !strings.Contains(out.String(), "kept binary default") {
		t.Errorf("the skipped file is not reported:\n%s", out.String())
	}
	if job.Changed() != 1 {
		t.Errorf("Changed = %d, want 1 (only the authored file)", job.Changed())
	}
	// The base records the snapshot's sha for the skipped paths, so a later plan has
	// nothing to do for them and a push is not blocked as behind.
	for _, p := range []string{"principles/seed.md", "principles/clash.md"} {
		if job.Plan.Base.Files[p] == "" {
			t.Errorf("base lost %s", p)
		}
	}
	again := PlanPull(&job.Plan.Base, h.recs[0], 1, map[string]string{"principles/clash.md": local["principles/clash.md"], "principles/authored.md": SHA([]byte("authored\n"))}, mustHeads(t, h), DefaultSet{})
	for _, e := range again.Entries {
		switch e.Action {
		case Write, Delete, Conflict, Incomplete:
			t.Errorf("a second plan still has work for %s: %v", e.Path, e.Action)
		}
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func mustHeads(t *testing.T, h *memHosted) map[string]string {
	t.Helper()
	heads, err := memBlobs{h}.Heads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return heads
}

// TestPullNeverDeletesALocalSeedTheHostedCopyDropped (AC7): the base records a seed
// from an earlier pull; the hosted snapshot later drops it. The seed is not in the
// push-side local view, so the plan has no Delete, and the file stays on disk.
func TestPullNeverDeletesALocalSeedTheHostedCopyDropped(t *testing.T) {
	seedBody := "---\n---\nDEFAULT seed\n"
	h := newMemHosted()
	h.snapshot(map[string]string{"principles/keep.md": "keep\n"}) // v1 no longer names seed.md
	d := testDriver(t, h, markedDefault)
	seedOnDisk := filepath.Join(d.DataDir, "principles", "seed.md")
	if err := os.MkdirAll(filepath.Dir(seedOnDisk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedOnDisk, []byte(seedBody), 0o644); err != nil {
		t.Fatal(err)
	}
	base := hosted.AreaBase{Version: 0, Files: map[string]string{
		"principles/seed.md": SHA([]byte(seedBody)), "principles/keep.md": SHA([]byte("keep\n")),
	}}
	if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, "principles", base); err != nil {
		t.Fatal(err)
	}

	// Real view: the seed is filtered out of local, so base-has / hosted-dropped /
	// local-lacks plans nothing for it.
	local := map[string]string{"principles/keep.md": SHA([]byte("keep\n"))}
	job, err := d.PlanPull(context.Background(), "principles", local)
	if err != nil || job == nil {
		t.Fatalf("PlanPull: %v %v", job, err)
	}
	if n := job.Plan.Count(Delete) + job.Plan.Count(RemovedStale); n != 0 {
		t.Errorf("plan removes %d file(s): %+v", n, actions(job.Plan))
	}
	if _, err := d.Stage(context.Background(), job); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := os.Stat(seedOnDisk); err != nil {
		t.Errorf("the seed was removed from disk: %v", err)
	}

	// Belt and braces: even if a view did carry the seed (an older caller), a Delete
	// whose local bytes the predicate calls a default is not carried out.
	withSeed := map[string]string{"principles/keep.md": SHA([]byte("keep\n")), "principles/seed.md": SHA([]byte(seedBody))}
	if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, "principles", base); err != nil {
		t.Fatal(err)
	}
	job, err = d.PlanPull(context.Background(), "principles", withSeed)
	if err != nil || job == nil {
		t.Fatalf("PlanPull: %v %v", job, err)
	}
	if job.Plan.Count(Delete) != 1 {
		t.Fatalf("test premise: want a Delete for the seed, got %+v", actions(job.Plan))
	}
	if _, err := d.Stage(context.Background(), job); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := os.Stat(seedOnDisk); err != nil {
		t.Errorf("Stage deleted a local binary default: %v", err)
	}
}

// writeTree writes files under repo/.satelle.
func writeTree(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(repo, ".satelle", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func embeddedSkill(t *testing.T, name string) string {
	t.Helper()
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == name {
			return d.Body
		}
	}
	t.Fatalf("no embedded skill %s", name)
	return ""
}

// TestStatusInStepWithAnUneditedSeedTheHostedCopyLacks (AC6): this machine still
// holds an unedited seed; the hosted snapshot does not carry it. The area is in
// step, not unpushed.
func TestStatusInStepWithAnUneditedSeedTheHostedCopyLacks(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Sync: map[string]string{"skills": "personal"}}
	step := embeddedSkill(t, "satelle-step-summary")
	writeTree(t, repo, map[string]string{
		"skills/satelle-step-summary.md": config.ApplyStamp(step, config.SHA(step)),
		"skills/mine.md":                 "authored\n",
	})

	h := newMemHosted()
	h.snapshot(map[string]string{"skills/mine.md": "authored\n"})
	d := testDriver(t, h, config.UneditedDefault)
	if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, "skills", hosted.AreaBase{
		Version: 1, Files: map[string]string{"skills/mine.md": SHA([]byte("authored\n"))},
	}); err != nil {
		t.Fatal(err)
	}

	local, err := LocalShas(cfg, repo, "skills", config.Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if _, has := local["skills/satelle-step-summary.md"]; has {
		t.Fatalf("LocalShas carries an unedited seed: %v", local)
	}
	st, err := d.Status(context.Background(), "skills", local)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateInStep {
		t.Errorf("state = %q, want %q (pending=%v)", st.State, StateInStep, st.Pending)
	}
}

// TestLocalShasUnchangedWhenASeedConverges (AC8): an upgrade rewrites an unedited
// seed from embedded body A to body B; the sha view a push or status reads is
// identical before and after.
func TestLocalShasUnchangedWhenASeedConverges(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Sync: map[string]string{"skills": "personal"}}
	step := embeddedSkill(t, "satelle-step-summary")
	older := step + "\nolder default body\n"
	writeTree(t, repo, map[string]string{
		"skills/satelle-step-summary.md": config.ApplyStamp(older, config.SHA(older)),
		"skills/mine.md":                 "authored\n",
	})
	before, err := LocalShas(cfg, repo, "skills", config.Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, repo, map[string]string{"skills/satelle-step-summary.md": config.ApplyStamp(step, config.SHA(step))})
	after, err := LocalShas(cfg, repo, "skills", config.Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("converging a seed changed LocalShas: %v -> %v", before, after)
	}
}
