package verb

// Internal tests for change-record caps and payload shape (sty_948ad5df).

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func wireCR(t *testing.T) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	SetWorkItemStore(db.Stories)
	SetLedgerStore(db.Ledger)
	SetDocIndexStore(db.DocIndex)
	SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		db.Close()
		SetWorkItemStore(nil)
		SetLedgerStore(nil)
		SetDocIndexStore(nil)
		SetLeaseStore(nil)
	})
}

// AC4: patch over changeRecordPatchLimit is truncated with marker; attachment written.
func TestChangeRecordPatchAttachedAndCapped(t *testing.T) {
	prev := changeRecordPatchLimit
	changeRecordPatchLimit = 64
	t.Cleanup(func() { changeRecordPatchLimit = prev })

	wireCR(t)
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "t@t")
	run("git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "init")

	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	stories := filepath.Join(dir, "stories")
	if err := os.MkdirAll(stories, 0o755); err != nil {
		t.Fatal(err)
	}
	SetStoryDir(stories)
	t.Cleanup(func() { SetStoryDir("") })

	ctx := context.Background()
	now := time.Now()
	ws, err := requireWorkItem()
	if err != nil {
		t.Fatal(err)
	}
	it, err := ws.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "cap", Category: "feature",
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	// Plant engagement baseline at current HEAD.
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = dir
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(shaOut))
	bp, _ := json.Marshal(engagementBaselinePayload{HeadSHA: sha, To: "in_progress"})
	appendLedgerEntry(ctx, it.ID, ledger.KindEngagementBaseline, "executor", "baseline", bp, now)

	// Large edit so git patch exceeds 64-byte cap.
	big := strings.Repeat("x", 400)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n// "+big+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	recordChangeSet(ctx, it, "in_progress", "done", now)

	ls, err := requireLedger()
	if err != nil {
		t.Fatal(err)
	}
	recs, err := ls.ListByStory(ctx, it.ID, ledger.KindChangeRecord)
	if err != nil || len(recs) < 1 {
		t.Fatalf("want change_record: err=%v n=%d", err, len(recs))
	}
	var p changeRecordPayload
	if err := json.Unmarshal(recs[len(recs)-1].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if !p.PatchAttached {
		t.Error("want PatchAttached")
	}
	if !p.PatchTruncated {
		t.Error("want PatchTruncated under 64-byte cap")
	}
	if p.PatchName == "" {
		t.Error("want PatchName")
	}
	// Attachment on disk carries truncation marker.
	att := filepath.Join(stories, it.ID, p.PatchName+".md")
	// writeAttachedDoc may use different naming — list dir.
	entries, _ := os.ReadDir(filepath.Join(stories, it.ID))
	var body []byte
	for _, e := range entries {
		b, rerr := os.ReadFile(filepath.Join(stories, it.ID, e.Name()))
		if rerr == nil && strings.Contains(string(b), "truncated at") {
			body = b
			break
		}
		if rerr == nil && p.PatchName != "" && strings.Contains(e.Name(), "change") {
			body = b
		}
	}
	if len(body) == 0 {
		// Fallback: any file under story dir.
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(stories, it.ID, e.Name()))
			body = b
			_ = att
			break
		}
	}
	if !strings.Contains(string(body), "truncated at") {
		t.Errorf("attachment body missing truncation marker; files=%v body=%q", entries, truncate(string(body), 200))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// AC4 structural: real changeRecordPayload JSON has no content/patch/verdict fields.
func TestChangeRecordPayloadShapeNoContent(t *testing.T) {
	p := changeRecordPayload{
		From: "a", To: "b", Files: []string{"secret.go"}, FileCount: 1,
		PatchAttached: true, PatchName: "change-a-b", PatchTruncated: true,
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"content", "patch", "body_text", "decision", "accept", "verdict"} {
		if _, ok := m[k]; ok {
			t.Errorf("payload must not carry %s", k)
		}
	}
	if _, ok := m["files"]; !ok {
		t.Error("want files key")
	}
	// Real ledger row uses the same type.
	wireCR(t)
	ctx := context.Background()
	now := time.Now()
	ws, err := requireWorkItem()
	if err != nil {
		t.Fatal(err)
	}
	it, err := ws.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "shape", Category: "feature",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	rawPayload, _ := json.Marshal(p)
	appendLedgerEntry(ctx, it.ID, ledger.KindChangeRecord, "executor", "shape", rawPayload, now)
	ls, _ := requireLedger()
	recs, _ := ls.ListByStory(ctx, it.ID, ledger.KindChangeRecord)
	if len(recs) < 1 {
		t.Fatal("no row")
	}
	var got map[string]any
	json.Unmarshal(recs[0].Payload, &got)
	for _, k := range []string{"content", "patch", "decision", "verdict"} {
		if _, ok := got[k]; ok {
			t.Errorf("ledger payload must not carry %s", k)
		}
	}
}

// sty_6469025e: non-md under authored root, agents.toml under configDir, db excluded.
func TestSubstrateChangedFilesWidened(t *testing.T) {
	repo := t.TempDir()
	skills := filepath.Join(repo, ".satelle", "skills")
	cfg := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Hour)
	if err := os.WriteFile(filepath.Join(skills, "x.dot"), []byte("digraph{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "agents.toml"), []byte("[e]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "satelle.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "deployed.version"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, p := range []string{
		filepath.Join(skills, "x.dot"),
		filepath.Join(cfg, "agents.toml"),
		filepath.Join(cfg, "satelle.db"),
		filepath.Join(cfg, "deployed.version"),
	} {
		_ = os.Chtimes(p, now, now)
	}

	got := substrateChangedFiles(repo, map[string]string{"skills": skills}, cfg, since)
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "x.dot") {
		t.Errorf("want non-md authored file: %v", got)
	}
	if !strings.Contains(joined, "agents.toml") {
		t.Errorf("want agents.toml: %v", got)
	}
	if strings.Contains(joined, "satelle.db") || strings.Contains(joined, "deployed.version") {
		t.Errorf("runtime files must be excluded: %v", got)
	}
	if n := substrateChangedFiles(repo, map[string]string{"skills": skills}, cfg, time.Time{}); len(n) != 0 {
		t.Errorf("zero since must be empty, got %v", n)
	}
}

// Deletion-only recording: a verb that removed git-ignored substrate is the only
// source of evidence for it, so RecordPathMutation must ledger the paths even
// though nothing on disk remains to enumerate (sty_30d3bd99 AC1/AC3).
func TestRecordPathMutationRecordsDeletedPaths(t *testing.T) {
	wireCR(t)
	ctx := context.Background()
	now := time.Now()
	ws, err := requireWorkItem()
	if err != nil {
		t.Fatal(err)
	}
	it, err := ws.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "prune", Category: "substrate",
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	root := changeRecordRepoRoot()
	// Paths that do NOT exist — the whole point is that they were removed.
	gone := []string{
		filepath.Join(root, ".satelle", "principles", "b-gone.md"),
		filepath.Join(root, ".satelle", "principles", "a-gone.md"),
	}
	if err := RecordPathMutation(ctx, it.ID, gone, "in_progress", "in_progress", now); err != nil {
		t.Fatal(err)
	}

	files, records, err := recordedChangeSet(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if records != 1 {
		t.Fatalf("want exactly 1 change_record, got %d", records)
	}
	want := []string{".satelle/principles/a-gone.md", ".satelle/principles/b-gone.md"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Errorf("recorded files = %v, want %v", files, want)
	}

	// The row must declare itself partial, or the anchor walk cannot skip it.
	ls, err := requireLedger()
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := ls.ListByStory(ctx, it.ID, ledger.KindChangeRecord)
	var p changeRecordPayload
	if err := json.Unmarshal(recs[len(recs)-1].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if !p.PartialEnumeration {
		t.Error("a verb-written row must be marked PartialEnumeration")
	}
	if p.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", p.FileCount)
	}

	// Empty story id, empty path set, and out-of-root paths write no row.
	if err := RecordPathMutation(ctx, "", gone, "in_progress", "in_progress", now); err != nil {
		t.Fatal(err)
	}
	if err := RecordPathMutation(ctx, it.ID, nil, "in_progress", "in_progress", now); err != nil {
		t.Fatal(err)
	}
	if err := RecordPathMutation(ctx, it.ID, []string{"/elsewhere/x.md"}, "in", "in", now); err != nil {
		t.Fatal(err)
	}
	if _, records, _ = recordedChangeSet(ctx, it.ID); records != 1 {
		t.Errorf("empty/out-of-root mutations must append no row, got %d records", records)
	}
}

// A partial row must NOT become the mtime anchor: if it did, substrate edited
// before the prune but never enumerated would silently drop out of the next
// transition's change set — the same evidence loss sty_30d3bd99 exists to fix.
func TestPartialRowDoesNotAdvanceMtimeAnchor(t *testing.T) {
	wireCR(t)
	ctx := context.Background()
	ws, err := requireWorkItem()
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	it, err := ws.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "anchor", Category: "substrate",
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	bp, _ := json.Marshal(engagementBaselinePayload{HeadSHA: "deadbeef", To: "in_progress"})
	appendLedgerEntry(ctx, it.ID, ledger.KindEngagementBaseline, "executor", "baseline", bp, base)

	anchor, ok := changeRecordSinceTime(ctx, it.ID)
	if !ok {
		t.Fatal("want an anchor from the engagement baseline")
	}

	// A prune lands well after the baseline.
	if err := RecordPathMutation(ctx, it.ID,
		[]string{filepath.Join(changeRecordRepoRoot(), ".satelle", "principles", "x.md")},
		"in_progress", "in_progress", time.Now()); err != nil {
		t.Fatal(err)
	}

	got, ok := changeRecordSinceTime(ctx, it.ID)
	if !ok {
		t.Fatal("anchor disappeared after a partial row")
	}
	if !got.Equal(anchor) {
		t.Errorf("partial row moved the mtime anchor: %v -> %v", anchor, got)
	}

	// A FULL row is still allowed to anchor.
	fp, _ := json.Marshal(changeRecordPayload{From: "in_progress", To: "done", Files: []string{"docs/a.md"}, FileCount: 1})
	full := time.Now().Add(time.Minute)
	appendLedgerEntry(ctx, it.ID, ledger.KindChangeRecord, "executor", "full", fp, full)
	got, ok = changeRecordSinceTime(ctx, it.ID)
	if !ok || got.Equal(anchor) {
		t.Errorf("a full enumeration must advance the anchor, got %v (baseline %v)", got, anchor)
	}
}
