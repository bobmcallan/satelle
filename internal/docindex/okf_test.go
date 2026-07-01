package docindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOKFConformance(t *testing.T) {
	cases := []struct {
		name, body string
		ok         bool
	}{
		{"events", "---\ntype: Table\n---\n\n# Events", true},
		{"events", "# Events\n\nno frontmatter", false},
		{"events", "---\ntitle: x\n---\n\nbody", false}, // frontmatter but no type
		{"index", "# Documents\n\n* [a](a.md)", true},   // reserved, exempt
		{"log", "## 2026-06-29\n* Creation", true},      // reserved, exempt
	}
	for _, c := range cases {
		err := OKFConformance(c.name, c.body)
		if (err == nil) != c.ok {
			t.Errorf("OKFConformance(%q) ok=%v, want %v (err=%v)", c.name, err == nil, c.ok, err)
		}
	}
}

func TestNormalizeOKF_AddsFrontmatter(t *testing.T) {
	body := "# Commit summary — sty_abc123\n\n- **Commit:** `deadbeef`\n"
	mod := time.Date(2026, 6, 29, 8, 4, 1, 0, time.UTC)
	out, changed := normalizeOKF("commit-summary-sty_abc123", body, mod)
	if !changed {
		t.Fatal("a frontmatter-less doc should be normalized")
	}
	// Conformant and carrying the recommended fields.
	if err := OKFConformance("commit-summary-sty_abc123", out); err != nil {
		t.Errorf("normalized doc not conformant: %v", err)
	}
	for _, want := range []string{
		"type: commit-summary",
		"title: Commit summary — sty_abc123",
		"timestamp: '2026-06-29T08:04:01Z'",
		"tags:\n- commit-summary",
		// no plain-prose line → description falls back to the title.
		"description: Commit summary — sty_abc123",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("normalized frontmatter missing %q\n---\n%s", want, out)
		}
	}
	// Body is preserved after the frontmatter.
	if !strings.Contains(out, "- **Commit:** `deadbeef`") {
		t.Errorf("body not preserved:\n%s", out)
	}
	// Idempotent: a second pass makes no change.
	if out2, changed2 := normalizeOKF("commit-summary-sty_abc123", out, mod); changed2 || out2 != out {
		t.Errorf("normalizeOKF not idempotent")
	}
}

func TestDeriveDescription_SkipsNonProse(t *testing.T) {
	// A commit-summary shape (headings, bullets, numbered ACs, a code fence) has
	// no plain prose line, so description derivation yields "" → caller falls back
	// to the title rather than grabbing a bullet or a numbered criterion.
	body := "# Commit summary\n\n- **Commit:** `abc`\n\n## Acceptance criteria\n\n1. First criterion text.\n2. Second.\n\n## Files\n\n```\na.go | 1 +\n```\n"
	if d := deriveDescription(body); d != "" {
		t.Errorf("deriveDescription on an all-structure body = %q, want \"\"", d)
	}
	// A genuine prose paragraph IS picked up.
	if d := deriveDescription("# Heading\n\nThis is the summary sentence.\n"); d != "This is the summary sentence." {
		t.Errorf("deriveDescription prose = %q", d)
	}
}

func TestNormalizeOKF_PreservesExistingType(t *testing.T) {
	body := "---\ntype: Playbook\ntitle: Keep me\n---\n\n# Keep me"
	out, changed := normalizeOKF("playbook", body, time.Now())
	if changed || out != body {
		t.Errorf("a doc with a non-empty type must be left untouched, got changed=%v", changed)
	}
}

func TestNormalizeOKF_InjectsTypeAndPreservesScalars(t *testing.T) {
	// A typeless attachment (story/name frontmatter) gains a type while its other
	// scalar keys survive.
	body := "---\nstory: sty_x\nname: plan\n---\n\n# Plan body"
	out, changed := normalizeOKF("plan", body, time.Now())
	if !changed {
		t.Fatal("a typeless doc should be normalized")
	}
	if err := OKFConformance("plan", out); err != nil {
		t.Errorf("not conformant after inject: %v", err)
	}
	if !strings.Contains(out, "story: sty_x") || !strings.Contains(out, "name: plan") {
		t.Errorf("existing scalar keys not preserved:\n%s", out)
	}
}

func TestNormalizeOKF_ReservedSkipped(t *testing.T) {
	for _, name := range []string{"index", "log"} {
		body := "# " + name + "\n\nno frontmatter"
		if out, changed := normalizeOKF(name, body, time.Now()); changed || out != body {
			t.Errorf("reserved %s.md must not be normalized", name)
		}
	}
}

// TestNormalizeType covers the OKF type-key migration for authored substrate
// (sty_889d277c): rename a legacy kind:, drop a redundant kind: beside an
// existing type:, insert a missing type:, and leave a conformant doc unchanged.
func TestNormalizeType(t *testing.T) {
	// Legacy kind: -> type: (value preserved, other keys intact).
	in := "---\nname: x\nkind: skill\ndescription: d\n---\n\nbody"
	out, changed := normalizeType(in, "skill")
	if !changed || !strings.Contains(out, "type: skill") || strings.Contains(out, "kind:") {
		t.Errorf("rename kind->type failed: %q", out)
	}
	if !strings.Contains(out, "name: x") || !strings.Contains(out, "description: d") || !strings.Contains(out, "\nbody") {
		t.Errorf("other content not preserved: %q", out)
	}
	// Redundant kind: alongside type: -> drop kind:.
	in2 := "---\ntype: skill\nname: x\nkind: skill\n---\nb"
	out2, changed2 := normalizeType(in2, "skill")
	if !changed2 || strings.Contains(out2, "kind:") {
		t.Errorf("redundant kind not dropped: %q", out2)
	}
	// Missing both -> insert type:.
	in3 := "---\nname: x\ndescription: d\n---\nb"
	out3, changed3 := normalizeType(in3, "principle")
	if !changed3 || !strings.Contains(out3, "type: principle") {
		t.Errorf("missing type not inserted: %q", out3)
	}
	// Already conformant -> unchanged.
	in4 := "---\ntype: skill\nname: x\n---\nb"
	if _, changed4 := normalizeType(in4, "skill"); changed4 {
		t.Errorf("conformant doc should be unchanged")
	}
	// No frontmatter -> unchanged.
	if _, changed5 := normalizeType("no frontmatter", "skill"); changed5 {
		t.Errorf("frontmatter-less doc should be unchanged")
	}
}

func TestMaterializeOKF_RendersFilesIndexAndLog(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)
	items := []OKFItem{
		{Name: "sty_aaa", Type: "story", Title: "Alpha", Description: "first", Body: "# Alpha\n\nbody a", Timestamp: ts},
		{Name: "sty_bbb", Type: "story", Title: "Beta", Body: "# Beta\n\nbody b", Timestamp: ts.Add(time.Hour)},
	}
	if err := MaterializeOKF(dir, "Backlog", items, ts); err != nil {
		t.Fatal(err)
	}
	// per-item files exist, are OKF-conformant, carry the generated marker + banner.
	for _, it := range items {
		b, err := os.ReadFile(filepath.Join(dir, it.Name+".md"))
		if err != nil {
			t.Fatalf("missing %s.md: %v", it.Name, err)
		}
		s := string(b)
		if err := OKFConformance(it.Name, s); err != nil {
			t.Errorf("%s not OKF-conformant: %v", it.Name, err)
		}
		if !isGenerated(s) {
			t.Errorf("%s missing generated marker", it.Name)
		}
		if !strings.Contains(s, "do not edit") {
			t.Errorf("%s missing do-not-edit banner", it.Name)
		}
		if !strings.Contains(s, "body "+strings.TrimPrefix(it.Name, "sty_")[:1]) {
			t.Errorf("%s body not preserved:\n%s", it.Name, s)
		}
	}
	// index.md lists both, okf_version + heading.
	idx, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`okf_version: "0.1"`, "# Backlog", "[Alpha](sty_aaa.md)", "[Beta](sty_bbb.md)"} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("index.md missing %q:\n%s", want, idx)
		}
	}
	// log.md is a date-grouped changelog.
	lg, err := os.ReadFile(filepath.Join(dir, "log.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lg), "## 2026-07-01") || !strings.Contains(string(lg), "sty_bbb.md") {
		t.Errorf("log.md not a proper changelog:\n%s", lg)
	}
}

func TestMaterializeOKF_PrunesStaleGeneratedOnly(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	// author a NON-generated file that must survive prune.
	authored := "---\ntype: document\n---\n\n# keep me"
	if err := os.WriteFile(filepath.Join(dir, "authored.md"), []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	// first pass writes two generated items.
	first := []OKFItem{
		{Name: "sty_aaa", Type: "story", Title: "A", Body: "a", Timestamp: ts},
		{Name: "sty_bbb", Type: "story", Title: "B", Body: "b", Timestamp: ts},
	}
	if err := MaterializeOKF(dir, "Backlog", first, ts); err != nil {
		t.Fatal(err)
	}
	// second pass drops sty_bbb — it must be pruned; sty_aaa + authored survive.
	if err := MaterializeOKF(dir, "Backlog", first[:1], ts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sty_bbb.md")); !os.IsNotExist(err) {
		t.Errorf("stale generated sty_bbb.md was not pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, "sty_aaa.md")); err != nil {
		t.Errorf("live sty_aaa.md was wrongly removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "authored.md")); err != nil {
		t.Errorf("authored (non-generated) file was wrongly pruned")
	}
}

func TestMaterializeOKF_WriteOnlyOnChange(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	items := []OKFItem{{Name: "sty_aaa", Type: "story", Title: "A", Body: "a", Timestamp: ts}}
	if err := MaterializeOKF(dir, "Backlog", items, ts); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "sty_aaa.md")
	fi, _ := os.Stat(p)
	older := ts.Add(-48 * time.Hour)
	if err := os.Chtimes(p, older, older); err != nil {
		t.Fatal(err)
	}
	// identical re-materialize must NOT rewrite the unchanged file.
	if err := MaterializeOKF(dir, "Backlog", items, ts); err != nil {
		t.Fatal(err)
	}
	fi2, _ := os.Stat(p)
	if !fi2.ModTime().Equal(older) {
		t.Errorf("unchanged file was rewritten (mtime advanced from set value): %v", fi2.ModTime())
	}
	_ = fi
}

func TestRefreshSummaryBundle_MigratesAndIndexes(t *testing.T) {
	docs := t.TempDir()
	// two top-level commit summaries (as push-review writes them today) + a normal doc.
	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(docs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("commit-summary-sty_aaa.md", "---\ntype: commit-summary\ntitle: Push summary — sty_aaa\ntimestamp: 2026-07-01T09:00:00Z\n---\n\n# Push summary")
	must("commit-summary-sty_bbb.md", "---\ntype: commit-summary\ntitle: Push summary — sty_bbb\ntimestamp: 2026-07-01T10:00:00Z\n---\n\n# Push summary")
	must("real-note.md", "---\ntype: document\n---\n\n# a real doc")

	refreshSummaryBundle(docs)

	sub := filepath.Join(docs, summaryBundleDir)
	// summaries migrated OUT of root INTO the sub-bundle.
	for _, n := range []string{"commit-summary-sty_aaa.md", "commit-summary-sty_bbb.md"} {
		if _, err := os.Stat(filepath.Join(docs, n)); !os.IsNotExist(err) {
			t.Errorf("%s was not migrated out of the root", n)
		}
		if _, err := os.Stat(filepath.Join(sub, n)); err != nil {
			t.Errorf("%s missing from the sub-bundle: %v", n, err)
		}
	}
	// the real doc stays at root.
	if _, err := os.Stat(filepath.Join(docs, "real-note.md")); err != nil {
		t.Errorf("non-summary doc was wrongly moved: %v", err)
	}
	// sub-bundle has its own reserved index.md + log.md.
	idx, err := os.ReadFile(filepath.Join(sub, "index.md"))
	if err != nil {
		t.Fatalf("sub-bundle index.md missing: %v", err)
	}
	if !strings.Contains(string(idx), "commit-summary-sty_aaa.md") || !strings.Contains(string(idx), "Story implementation summaries") {
		t.Errorf("sub-bundle index.md is wrong:\n%s", idx)
	}
	if _, err := os.Stat(filepath.Join(sub, "log.md")); err != nil {
		t.Errorf("sub-bundle log.md missing: %v", err)
	}
}

func TestMaterializeOKF_WritesReadOnlyViews(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	items := []OKFItem{{Name: "sty_aaa", Type: "story", Title: "A", Body: "a", Timestamp: ts}}
	if err := MaterializeOKF(dir, "Backlog", items, ts); err != nil {
		t.Fatal(err)
	}
	// every generated file (per-item + reserved index.md/log.md) is read-only 0o444.
	for _, f := range []string{"sty_aaa.md", "index.md", "log.md"} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("%s missing: %v", f, err)
		}
		if fi.Mode().Perm() != 0o444 {
			t.Errorf("%s mode = %o, want 0444 (read-only generated view)", f, fi.Mode().Perm())
		}
	}
	// a direct write to a read-only view fails at the OS layer.
	if err := os.WriteFile(filepath.Join(dir, "sty_aaa.md"), []byte("tampered"), 0o444); err == nil {
		t.Errorf("a direct write to the read-only view unexpectedly succeeded")
	}
	// but reindex still regenerates a CHANGED view (remove-then-write).
	items[0].Body = "a changed"
	if err := MaterializeOKF(dir, "Backlog", items, ts); err != nil {
		t.Fatalf("regeneration of a changed read-only view failed: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "sty_aaa.md"))
	if !strings.Contains(string(b), "a changed") {
		t.Errorf("changed view was not regenerated:\n%s", b)
	}
}
