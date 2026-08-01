package verb_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestEngagementBaselineIdempotentAndDiff: first engage records baseline once;
// park/re-enter keeps one row; story-diff enumerates tracked+untracked (sty_da169e03).
func TestEngagementBaselineIdempotentAndDiff(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "a.txt")
	run("git", "commit", "-m", "init")
	head, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	headSHA := strings.TrimSpace(string(head))

	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	wireWithWorkflows(t, routeHalves(
		"## *\n- raised\n- coded\n- closed\npark: blocked\n",
		"## backlog\nstart: true\nprovides: raised\n\n"+
			"## in_progress\nagent: executor\nprovides: coded\nrequires: raised\n\n"+
			"## done\nterminal: true\nprovides: closed\nrequires: coded\n"))
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "baseline slice", "body": "goal for engagement baseline",
		"acceptance_criteria": "1. baseline recorded",
		"category":            "feature",
		"tags":                []string{"workflow:eng"},
	}), &it)

	if _, err := dispatchRaw(t, "story-diff", map[string]any{"id": it.ID}); err == nil {
		t.Fatal("want error with no baseline")
	} else if !strings.Contains(err.Error(), "no engagement baseline") {
		t.Errorf("error should mention no baseline: %v", err)
	}

	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	if it.Status != "in_progress" {
		t.Fatalf("status=%s", it.Status)
	}

	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindEngagementBaseline}), &entries)
	if len(entries) != 1 {
		t.Fatalf("want 1 engagement_baseline, got %d: %+v", len(entries), entries)
	}
	var p struct {
		HeadSHA string `json:"head_sha"`
		To      string `json:"to"`
	}
	json.Unmarshal(entries[0].Payload, &p)
	if p.HeadSHA != headSHA {
		t.Errorf("head_sha=%q want %q", p.HeadSHA, headSHA)
	}
	if p.To != "in_progress" {
		t.Errorf("to=%q", p.To)
	}

	// Park then re-enter performing: still one baseline, original sha.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "blocked"}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindEngagementBaseline}), &entries)
	if len(entries) != 1 {
		t.Fatalf("after park/re-enter want still 1 baseline, got %d", len(entries))
	}
	json.Unmarshal(entries[0].Payload, &p)
	if p.HeadSHA != headSHA {
		t.Errorf("after re-enter head_sha=%q want original %q", p.HeadSHA, headSHA)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new_untracked.txt"), []byte("u\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := call(t, "story-diff", map[string]any{"id": it.ID})
	var res struct {
		Files    []string `json:"files"`
		Stat     string   `json:"stat"`
		Baseline string   `json:"baseline_sha"`
		Note     string   `json:"note"`
	}
	json.Unmarshal(raw, &res)
	if res.Baseline != headSHA {
		t.Errorf("diff baseline=%q", res.Baseline)
	}
	if !strings.Contains(res.Note, "enumeration only") {
		t.Errorf("note should disclaim verdict: %q", res.Note)
	}
	foundA, foundU := false, false
	for _, f := range res.Files {
		if f == "a.txt" {
			foundA = true
		}
		if f == "new_untracked.txt" {
			foundU = true
		}
	}
	if !foundA {
		t.Errorf("files should include a.txt: %v", res.Files)
	}
	if !foundU {
		t.Errorf("files should include untracked new_untracked.txt: %v", res.Files)
	}
	if res.Stat == "" {
		t.Error("empty stat")
	}

	rawStdin := call(t, "story-diff", map[string]any{
		"story": map[string]any{"id": it.ID},
		"from":  "blocked",
		"to":    "in_progress",
	})
	var resStdin struct {
		StoryID string `json:"story_id"`
	}
	json.Unmarshal(rawStdin, &resStdin)
	if resStdin.StoryID != it.ID {
		t.Errorf("stdin form story_id=%q", resStdin.StoryID)
	}

	raw2 := call(t, "story-diff", map[string]any{"id": it.ID, "patch": true})
	var res2 struct {
		Patch string `json:"patch"`
	}
	json.Unmarshal(raw2, &res2)
	if res2.Patch == "" {
		t.Error("want patch body with patch:true")
	}
}
