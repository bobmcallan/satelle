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
