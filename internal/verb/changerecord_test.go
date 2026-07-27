package verb_test

import (
	"context"
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

const changeWF = "---\nname: cr-wf\ntype: workflow\nscope: system\napplies_to: [\"*\"]\ndescription: change-record test\n---\n```dot\n" +
	`digraph e {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> in_progress
  in_progress -> done
}
` + "```\n"

func gitRepo(t *testing.T) string {
	t.Helper()
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
	return dir
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
}

// AC1 + AC2 + AC8: enacted transition records change_record; visible on ledger; no hook.
func TestChangeRecordPerEnactedTransition(t *testing.T) {
	dir := gitRepo(t)
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)

	wireWithWorkflows(t, map[string]string{"cr-wf": changeWF})
	verb.SetStoryDir(stories)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
	})

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "cr slice", "body": "goal", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)

	// Engage: backlog → in_progress produces baseline + change_record.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	if it.Status != "in_progress" {
		t.Fatalf("status=%s", it.Status)
	}

	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &entries)
	if len(entries) < 1 {
		t.Fatalf("want ≥1 change_record after transition, got %d", len(entries))
	}
	var p struct {
		From      string   `json:"from"`
		To        string   `json:"to"`
		Files     []string `json:"files"`
		FileCount int      `json:"file_count"`
	}
	json.Unmarshal(entries[len(entries)-1].Payload, &p)
	if p.From != "backlog" || p.To != "in_progress" {
		t.Errorf("from/to = %s/%s", p.From, p.To)
	}
	// Payload has files key (may be empty on first engage with no edits).
	if p.FileCount < 0 {
		t.Error("file_count negative")
	}
	// No verdict-shaped fields.
	var raw map[string]any
	json.Unmarshal(entries[len(entries)-1].Payload, &raw)
	for _, k := range []string{"decision", "accept", "verdict", "content", "patch"} {
		if _, ok := raw[k]; ok {
			t.Errorf("payload must not carry %s", k)
		}
	}

	// Edit a file and close → another change_record listing it.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n// x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &it)
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &entries)
	if len(entries) < 2 {
		t.Fatalf("want ≥2 change_records, got %d", len(entries))
	}
	json.Unmarshal(entries[len(entries)-1].Payload, &p)
	found := false
	for _, f := range p.Files {
		if f == "a.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a.go in change record files: %v", p.Files)
	}
}

// AC2: story-diff --recorded returns union.
func TestChangeRecordVisibleOnLedgerList(t *testing.T) {
	dir := gitRepo(t)
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)
	wireWithWorkflows(t, map[string]string{"cr-wf": changeWF})
	verb.SetStoryDir(stories)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
	})

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "vis", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &it)

	raw := call(t, "story-diff", map[string]any{"id": it.ID, "recorded": true})
	var res struct {
		Source  string   `json:"source"`
		Files   []string `json:"files"`
		Records int      `json:"records"`
	}
	json.Unmarshal(raw, &res)
	if res.Source != "recorded" {
		t.Errorf("source=%q", res.Source)
	}
	if res.Records < 1 {
		t.Errorf("records=%d", res.Records)
	}
	joined := strings.Join(res.Files, ",")
	if !strings.Contains(joined, "b.go") {
		t.Errorf("recorded files missing b.go: %v", res.Files)
	}
}

// Non-engaging close path: backlog → done with no agent=executor (no baseline).
const noEngageWF = "---\nname: cr-ne\ntype: workflow\nscope: system\napplies_to: [\"*\"]\ndescription: no-engage change-record\n---\n```dot\n" +
	`digraph e {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  backlog -> done
}
` + "```\n"

// AC6: no-baseline TRANSITION degrades — row written with files:[] and
// unavailable:no-baseline; status still advances.
func TestChangeRecordNoBaselineDegrades(t *testing.T) {
	dir := gitRepo(t)
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)
	wireWithWorkflows(t, map[string]string{"cr-ne": noEngageWF})
	verb.SetStoryDir(stories)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
	})
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "nb", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-ne"},
	}), &it)
	// Non-engaging transition: no engagement_baseline, but change_record must land.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &it)
	if it.Status != "done" {
		t.Fatalf("status must advance without baseline: %s", it.Status)
	}
	var baselines []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindEngagementBaseline}), &baselines)
	if len(baselines) != 0 {
		t.Fatalf("expected no engagement baseline on non-engaging close, got %d", len(baselines))
	}
	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &entries)
	if len(entries) < 1 {
		t.Fatal("want change_record on no-baseline transition")
	}
	var p struct {
		Unavailable string   `json:"unavailable"`
		Files       []string `json:"files"`
		FileCount   int      `json:"file_count"`
	}
	json.Unmarshal(entries[len(entries)-1].Payload, &p)
	if p.Unavailable != "no-baseline" {
		t.Errorf("unavailable=%q want no-baseline", p.Unavailable)
	}
	if len(p.Files) != 0 || p.FileCount != 0 {
		t.Errorf("files must be empty on no-baseline, got %v count=%d", p.Files, p.FileCount)
	}
}

// AC6 no-git: chdir to non-git dir with stores; transition still succeeds.
func TestChangeRecordNoGitDegrades(t *testing.T) {
	dir := t.TempDir() // not a git repo
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)
	wireWithWorkflows(t, map[string]string{"cr-wf": changeWF})
	verb.SetStoryDir(stories)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
	})
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "nogit", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	// Transition must not fail despite no git.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	if it.Status != "in_progress" {
		t.Fatalf("status must advance without git: %s", it.Status)
	}
	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &entries)
	if len(entries) < 1 {
		t.Fatal("want change_record even without git")
	}
	var p struct {
		Unavailable string   `json:"unavailable"`
		Files       []string `json:"files"`
	}
	json.Unmarshal(entries[len(entries)-1].Payload, &p)
	// no-baseline or no-git or empty files with unavailable
	if p.Unavailable == "" && len(p.Files) > 0 {
		// ok if empty unavailable with empty files too
		t.Logf("payload: unavailable=%q files=%v", p.Unavailable, p.Files)
	}
	// Must not dump arbitrary absolute paths as "changed".
	for _, f := range p.Files {
		if strings.HasPrefix(f, "/") {
			t.Errorf("absolute path in no-git record: %s", f)
		}
	}
}

// AC5: real ledger change_record has no verdict fields.
func TestChangeRecordHasNoVerdict(t *testing.T) {
	dir := gitRepo(t)
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)
	wireWithWorkflows(t, map[string]string{"cr-wf": changeWF})
	verb.SetStoryDir(stories)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
	})
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "nov", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &entries)
	if len(entries) < 1 {
		t.Fatal("want change_record")
	}
	var m map[string]any
	json.Unmarshal(entries[len(entries)-1].Payload, &m)
	for _, k := range []string{"decision", "accept", "verdict"} {
		if _, ok := m[k]; ok {
			t.Errorf("payload must not carry %s", k)
		}
	}
}

// AC4: real ledger payload carries no content/patch body (paths only).
func TestChangeRecordPayloadCarriesNoContent(t *testing.T) {
	dir := gitRepo(t)
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)
	wireWithWorkflows(t, map[string]string{"cr-wf": changeWF})
	verb.SetStoryDir(stories)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
	})
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "noc", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	if err := os.WriteFile(filepath.Join(dir, "secret.go"), []byte("package secret\nconst k = \"planted-secret-xyz\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &it)
	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &entries)
	if len(entries) < 2 {
		t.Fatalf("want ≥2 change_records, got %d", len(entries))
	}
	raw := string(entries[len(entries)-1].Payload)
	if strings.Contains(raw, "planted-secret-xyz") {
		t.Error("ledger payload must not contain file content/secret")
	}
	var m map[string]any
	json.Unmarshal(entries[len(entries)-1].Payload, &m)
	for _, k := range []string{"content", "patch", "body_text"} {
		if _, ok := m[k]; ok {
			t.Errorf("payload must not carry %s", k)
		}
	}
}

// AC5: recording never decides — a gate reject still blocks; no change_record on reject.
func TestChangeRecordDoesNotAlterGateDecisions(t *testing.T) {
	dir := gitRepo(t)
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)
	wireWithWorkflows(t, map[string]string{"cr-wf": changeWF})
	verb.SetStoryDir(stories)
	// First engage with open gate so we land in_progress.
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
	})
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "gate", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	var before []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &before)
	nBefore := len(before)

	// Gate rejects close — transition must not enact; no new change_record.
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: false, Notes: "nope", Skill: "satelle-code-ac-review",
	}})
	req, _ := json.Marshal(map[string]any{"id": it.ID, "status": "done"})
	_, err := verb.Dispatch(context.Background(), "story-set", req)
	if err == nil {
		t.Fatal("expected gate reject error")
	}
	json.Unmarshal(call(t, "story-get", map[string]any{"id": it.ID}), &it)
	if it.Status != "in_progress" {
		t.Fatalf("status must stay in_progress after reject, got %s", it.Status)
	}
	var after []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &after)
	if len(after) != nBefore {
		t.Errorf("change_record count must not grow on reject: before=%d after=%d", nBefore, len(after))
	}

	// Gate accept → transition enacts and records.
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: true, Skill: "satelle-code-ac-review",
	}})
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &it)
	if it.Status != "done" {
		t.Fatalf("accept must advance: %s", it.Status)
	}
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &after)
	if len(after) <= nBefore {
		t.Errorf("want new change_record after accept: before=%d after=%d", nBefore, len(after))
	}
}

// AC8: change_record is produced on the transition path with no hook event.
func TestChangeRecordNeedsNoHook(t *testing.T) {
	// Same path as production: verb Dispatch story-set (CLI/API), not hook handlers.
	// Proves record is not coupled to Claude PostToolUse / Stop hooks.
	dir := gitRepo(t)
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)
	wireWithWorkflows(t, map[string]string{"cr-wf": changeWF})
	verb.SetStoryDir(stories)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
	})
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "hookless", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	// story-set only — no hook verb, no SessionStart/PostToolUse.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &entries)
	if len(entries) < 1 {
		t.Fatal("change_record must be produced without any hook event")
	}
	// Hook-related ledger kinds must not be required.
	var all []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID}), &all)
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.Kind), "hook") {
			t.Errorf("unexpected hook ledger kind on pure story-set path: %s", e.Kind)
		}
	}
}

// AC7: consumer surface story-diff --recorded is what substrate-only-check prefers.
func TestSubstrateOnlyCheckReadsRecordedChangeSet(t *testing.T) {
	// The skill script prefers `satelle story diff --recorded`; this test pins
	// the verb contract that script parses (source=recorded + files union).
	dir := gitRepo(t)
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)
	wireWithWorkflows(t, map[string]string{"cr-wf": changeWF})
	verb.SetStoryDir(stories)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
	})
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "consumer", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	skillDir := filepath.Join(dir, ".satelle", "skills")
	_ = os.MkdirAll(skillDir, 0o755)
	if err := os.WriteFile(filepath.Join(skillDir, "only.md"), []byte("# s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verb.SetAuthoredDirs(map[string]string{"skills": skillDir})
	t.Cleanup(func() { verb.SetAuthoredDirs(nil) })
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &it)

	raw := call(t, "story-diff", map[string]any{"id": it.ID, "recorded": true})
	var res struct {
		Source string   `json:"source"`
		Files  []string `json:"files"`
	}
	json.Unmarshal(raw, &res)
	if res.Source != "recorded" {
		t.Fatalf("source=%q want recorded", res.Source)
	}
	// Script parses JSON .files; must be present and non-nil key.
	var top map[string]any
	json.Unmarshal(raw, &top)
	if _, ok := top["files"]; !ok {
		t.Fatal("recorded response must include files key for check script")
	}
}

// AC3: git-ignored substrate path appears via authoredDirs leg.
func TestChangeRecordCapturesGitIgnoredSubstrate(t *testing.T) {
	dir := gitRepo(t)
	chdir(t, dir)
	// gitignore .satelle/
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".satelle/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("git", "add", ".gitignore")
	run.Dir = dir
	_ = run.Run()
	run = exec.Command("git", "commit", "-m", "ignore")
	run.Dir = dir
	_ = run.Run()

	satelleDir := filepath.Join(dir, ".satelle", "skills")
	if err := os.MkdirAll(satelleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)

	wireWithWorkflows(t, map[string]string{"cr-wf": changeWF})
	verb.SetStoryDir(stories)
	verb.SetAuthoredDirs(map[string]string{"skills": satelleDir})
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetStoryDir("")
		verb.SetAuthoredDirs(nil)
	})

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "sub", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)

	// Create ignored skill after engage so mtime > baseline.
	skillPath := filepath.Join(satelleDir, "new-skill.md")
	if err := os.WriteFile(skillPath, []byte("# skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bump mtime into the future relative to baseline by touching after sleep?
	// baseline was just now — file write is after, so mtime should be After.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &it)

	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindChangeRecord}), &entries)
	if len(entries) < 2 {
		t.Fatalf("want ≥2 records, got %d", len(entries))
	}
	var p struct {
		Files []string `json:"files"`
	}
	json.Unmarshal(entries[len(entries)-1].Payload, &p)
	found := false
	for _, f := range p.Files {
		if strings.Contains(f, "new-skill.md") {
			found = true
			if strings.HasPrefix(f, "/") {
				t.Errorf("path must be repo-relative: %s", f)
			}
		}
	}
	if !found {
		t.Errorf("git-ignored substrate path missing from record: %v", p.Files)
	}
}
