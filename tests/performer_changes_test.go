//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// sty_af0b1761: a read-only readiness performer reports the story configuration
// it wants as `changes`, and the binary applies it on the record. The performer
// here is a stub process behind a real binding, granted no shell or write tool,
// once as a read-only pi binding and once as a claude-command binding; it never
// touches the store. The step is a container's own readiness step (backlog →
// ready, waiting on its children) declaring propose and apply_changes.

const changesDoneToml = `["*"]
obligations = ["raised", "readied", "coded", "closed"]

[epic-parent]
obligations = ["raised", "ready", "epic-coded", "epic-closed"]
`

const changesStepToml = `[raised]
status = "backlog"
start = true

[ready]
status = "ready"
agent = "ready-reviewer"
skills = ["ready-review"]
waits_on_children = true
schedule = "parallel"
propose = true
mark = "ready"
apply_changes = { ops = ["add-tags", "create-child"], tags = ["order:", "lane:", "surface:"] }
reviewers = ["gate-rec"]
reviewer_agent = "reviewer"
requires = ["raised"]

[readied]
status = "plan"
agent = "ready-reviewer"
skills = ["ready-review"]
propose = true
mark = "ready"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
freeze = true
requires = ["readied"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[epic-coded]
status = "in_progress"
agent = "executor"
requires = ["ready"]

[epic-closed]
status = "done"
terminal = true
requires = ["epic-coded"]

[[gate]]
skill = "satelle-estimate-actual-review"
on = ["__never__"]
`

const changesSkillFixture = `---
name: %s
scope: project
type: skill
tags: [type:skill]
description: Fixture skill for the reported-changes tests.
---

# %s (fixture)

Judge the story and report the configuration changes you want in a changes list.
`

const changesReviewerSkillFixture = `---
name: gate-rec
scope: project
type: skill
tags: [type:skill]
description: Fixture reviewer for the reported-changes tests — judge the readiness the performer produced.
---

# gate-rec (fixture)

Judge the story as the transition payload presents it. Respond with one JSON
object: {"decision": "accept" | "reject", "notes": "why"}. The decision field is
accept or reject, and notes says why.
`

const (
	drivingSession = "driving-sess"
	drivingModel   = "driving-model"
	performerName  = "ready-reviewer"
)

// changesBinding is one read-only transport the stub performer runs behind.
type changesBinding struct {
	name  string
	exe   string // the stub's file name: the adapter is recognised by it
	tools string
	// command builds the binding's command line around the stub path.
	command func(stub string) string
	// wrap renders the performer's final text the way its harness prints it.
	wrap func(t *testing.T, text string) string
	// model is the model the performer's output reports; "" means none.
	model string
}

var changesBindings = []changesBinding{
	{
		name: "pi", exe: "pi", tools: "read,grep,find,ls",
		command: func(stub string) string { return stub + " -p --tools {tools} {system}" },
		wrap:    func(_ *testing.T, text string) string { return text },
	},
	{
		name: "claude command", exe: "claude", tools: "Read,Grep,Glob,read_file",
		command: func(stub string) string {
			return stub + " -p --output-format json --disallowedTools Write,Edit,NotebookEdit,Bash --append-system-prompt {system} --allowedTools {tools}"
		},
		wrap: func(t *testing.T, text string) string {
			env, err := json.Marshal(map[string]any{
				"type": "result", "result": text,
				"usage":      map[string]int{"input_tokens": 1, "output_tokens": 1},
				"modelUsage": map[string]any{"claude-stub-model": map[string]any{"inputTokens": 1, "outputTokens": 1, "costUSD": 0}},
			})
			if err != nil {
				t.Fatal(err)
			}
			return string(env)
		},
		model: "claude-stub-model",
	},
}

// changesWorld is a repo with an epic, two children and the stub performer.
type changesWorld struct {
	repo, stubDir        string
	epic, a, b           string
	binding              changesBinding
	gatePayload, reviewr string
}

// runDriven runs the binary as the driving session.
func (w changesWorld) runDriven(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runEnv(t, testBin, w.repo, []string{config.SessionEnv + "=" + drivingSession}, args...)
}

func (w changesWorld) mustDriven(t *testing.T, args ...string) string {
	t.Helper()
	out, err := w.runDriven(t, args...)
	if err != nil {
		t.Fatalf("satelle %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// publishDrivingModel records the driving session's in-loop model where the
// binary reads it, so a child wrongly attributed to that session is caught.
func publishDrivingModel(t *testing.T) {
	t.Helper()
	prev, had := os.LookupEnv("SATELLE_HOME")
	_ = os.Setenv("SATELLE_HOME", isolatedHome(t))
	defer func() {
		if had {
			_ = os.Setenv("SATELLE_HOME", prev)
		} else {
			_ = os.Unsetenv("SATELLE_HOME")
		}
	}()
	config.PublishSessionModel(drivingSession, "in-loop", drivingModel, "claude", "")
}

func newChangesWorld(t *testing.T, b changesBinding) changesWorld {
	t.Helper()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n\n[categories]\nenforce = \"off\"\n")
	writeRouteFixture(t, repo, changesDoneToml, changesStepToml)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "ready-review.md"), fmt.Sprintf(changesSkillFixture, "ready-review", "ready-review"))
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "gate-rec.md"), changesReviewerSkillFixture)

	w := changesWorld{repo: repo, stubDir: t.TempDir(), binding: b}
	stub := filepath.Join(w.stubDir, b.exe)
	performer := fmt.Sprintf(`#!/bin/sh
dir=%q
printf '%%s\n' "$@" >> "$dir/argv.txt"
cat > "$dir/payload.last"
if grep -q '"category":"epic-parent"' "$dir/payload.last"; then
  cat "$dir/report.txt"
else
  echo '{"decision":"accept","notes":""}'
fi
`, w.stubDir)
	if err := os.WriteFile(stub, []byte(performer), 0o755); err != nil {
		t.Fatal(err)
	}
	// The reviewer records the transition payload it judged, then accepts.
	w.gatePayload = filepath.Join(w.stubDir, "gate-payload.json")
	w.reviewr = filepath.Join(w.stubDir, "gate-recorder.sh")
	recorder := fmt.Sprintf("#!/bin/sh\ncat > %q\necho '{\"decision\":\"accept\",\"notes\":\"\"}'\n", w.gatePayload)
	if err := os.WriteFile(w.reviewr, []byte(recorder), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"), fmt.Sprintf(
		"[reviewer]\ncommand = \"%s {system} {tools} {model}\"\nisolation = \"operator-attested\"\n\n"+
			"[%s]\ncommand = %q\ntools = %q\nmodel = \"stub-model\"\n",
		w.reviewr, performerName, b.command(stub), b.tools))
	mustRun(t, testBin, repo, "reindex")
	publishDrivingModel(t)

	create := func(args ...string) string {
		out := mustRun(t, testBin, repo, append([]string{"story", "create"}, args...)...)
		id := extractID(out, "sty_")
		if id == "" {
			t.Fatalf("no story id:\n%s", out)
		}
		return id
	}
	w.epic = create("--category", "epic-parent", "--title", "Epic", "--body", "umbrella", "--tags", "epic:rr")
	w.a = create("--category", "feature", "--title", "Child A", "--body", "a", "--acceptance", "1. a", "--tags", "epic:rr")
	w.b = create("--category", "feature", "--title", "Child B", "--body", "b", "--acceptance", "1. b", "--tags", "epic:rr")
	return w
}

// report writes what the stub performer prints when it judges the epic.
func (w changesWorld) report(t *testing.T, final map[string]any) {
	t.Helper()
	text, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(w.stubDir, "report.txt"), w.binding.wrap(t, string(text))+"\n")
}

func (w changesWorld) repairs() map[string]any {
	return map[string]any{"decision": "accept", "notes": "repaired", "changes": []map[string]any{
		{"op": "add-tags", "story": w.a, "tags": []string{"order:2", "surface:cli"}},
		{"op": "add-tags", "story": w.epic, "tags": []string{"lane:trunk"}},
		{"op": "create-child", "title": "Filed by the read-only performer", "body": "found missing",
			"acceptance": "1. it is filed", "category": "feature", "tags": []string{"surface:cli"}},
	}}
}

type storyJSON struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	ParentID string   `json:"parent_id"`
	Tags     []string `json:"tags"`
}

func (w changesWorld) story(t *testing.T, id string) storyJSON {
	t.Helper()
	var s storyJSON
	if err := json.Unmarshal([]byte(mustRun(t, testBin, w.repo, "story", "get", id)), &s); err != nil {
		t.Fatalf("story get %s: %v", id, err)
	}
	return s
}

type ledgerRow struct {
	Kind    string          `json:"kind"`
	Actor   string          `json:"actor"`
	Payload json.RawMessage `json:"payload"`
}

func (w changesWorld) rows(t *testing.T, id, kind string) []ledgerRow {
	t.Helper()
	var rows []ledgerRow
	out := mustRun(t, testBin, w.repo, "ledger", "list", "--story", id, "--kind", kind, "--json")
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ledger list %s %s: %v\n%s", id, kind, err, out)
	}
	return rows
}

// sourceOfRow is the "source" a row's payload names, or "".
func sourceOfRow(r ledgerRow) string {
	var p struct {
		Source string `json:"source"`
	}
	_ = json.Unmarshal(r.Payload, &p)
	return p.Source
}

func carriesTag(tags []string, tag string) bool {
	for _, x := range tags {
		if x == tag {
			return true
		}
	}
	return false
}

// grantsNothingButReading fails when the stub was launched with any tool beyond
// the binding's read-only grant: the proof the performer had no shell or write
// tool to apply the changes itself.
func (w changesWorld) grantsNothingButReading(t *testing.T) {
	t.Helper()
	argv, err := os.ReadFile(filepath.Join(w.stubDir, "argv.txt"))
	if err != nil {
		t.Fatalf("the performer stub never ran: %v", err)
	}
	if !strings.Contains(string(argv), w.binding.tools) {
		t.Errorf("the performer was not launched with its read-only grant %q:\n%s", w.binding.tools, argv)
	}
	for _, tool := range strings.Split(w.binding.tools, ",") {
		switch strings.ToLower(tool) {
		case "bash", "write", "edit", "notebookedit":
			t.Errorf("the binding grants the shell or write tool %q", tool)
		}
	}
}

// AC1, AC2, AC4 (under `story ready`): the added tags and the created child are on
// the record, applied by the binary for a performer that could only read.
func TestReportedChangesAppliedUnderStoryReady(t *testing.T) {
	for _, b := range changesBindings {
		t.Run(b.name, func(t *testing.T) {
			w := newChangesWorld(t, b)
			w.report(t, w.repairs())

			out := w.mustDriven(t, "story", "ready", w.epic)
			if !strings.Contains(out, w.epic+" marked") {
				t.Fatalf("the epic must be readied:\n%s", out)
			}
			w.grantsNothingButReading(t)

			a := w.story(t, w.a)
			if !carriesTag(a.Tags, "order:2") || !carriesTag(a.Tags, "surface:cli") {
				t.Errorf("child a tags = %v, want order:2 and surface:cli added", a.Tags)
			}
			epic := w.story(t, w.epic)
			if !carriesTag(epic.Tags, "lane:trunk") || !carriesTag(epic.Tags, "ready") || epic.Status != "backlog" {
				t.Errorf("epic = %+v, want lane:trunk added, marked, still in backlog", epic)
			}

			// The created child is a member the epic wave sees.
			var wave struct {
				Runnable []string `json:"runnable"`
			}
			if err := json.Unmarshal([]byte(mustRun(t, testBin, w.repo, "story", "wave", w.epic, "--json")), &wave); err != nil {
				t.Fatal(err)
			}
			var child storyJSON
			for _, id := range wave.Runnable {
				if id != w.a && id != w.b {
					child = w.story(t, id)
				}
			}
			if child.ID == "" || child.Title != "Filed by the read-only performer" {
				t.Fatalf("story wave must name the reported child among %v", wave.Runnable)
			}
			hasWorkflow := false
			for _, tag := range child.Tags {
				hasWorkflow = hasWorkflow || strings.HasPrefix(tag, "workflow:")
			}
			if child.ParentID != w.epic || !carriesTag(child.Tags, "epic:rr") || !carriesTag(child.Tags, "surface:cli") || !hasWorkflow {
				t.Errorf("child = %+v, want an epic member under the epic with its reported and stamped tags", child)
			}

			// Exactly one row records each change, by the performer, naming the step.
			updated := w.rows(t, w.a, "story_updated")
			var byPerformer []ledgerRow
			for _, r := range updated {
				if r.Actor == performerName {
					byPerformer = append(byPerformer, r)
				}
			}
			if len(byPerformer) != 1 || sourceOfRow(byPerformer[0]) != "performer:ready" {
				t.Errorf("child a: want exactly one story_updated row by %s naming performer:ready, got %+v", performerName, updated)
			}
			created := w.rows(t, child.ID, "story_created")
			if len(created) != 1 || created[0].Actor != performerName || sourceOfRow(created[0]) != "performer:ready" {
				t.Errorf("child: want exactly one story_created row by %s naming performer:ready, got %+v", performerName, created)
			}

			// Who created the child is the performer, never the driving session.
			creators := w.rows(t, child.ID, "session_model")
			if len(creators) != 1 {
				t.Fatalf("want one creator row, got %+v", creators)
			}
			var cr struct {
				Role      string `json:"role"`
				Model     string `json:"model"`
				Reason    string `json:"reason"`
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(creators[0].Payload, &cr); err != nil {
				t.Fatal(err)
			}
			wantModel := b.model
			if wantModel == "" {
				wantModel = "unknown"
			}
			if cr.Role != "creator" || cr.Model != wantModel || cr.SessionID != "" || cr.Model == drivingModel {
				t.Errorf("creator row = %+v, want the performer's model %q and no session (the driving session is %s / %s)", cr, wantModel, drivingSession, drivingModel)
			}
			if b.model == "" && cr.Reason == "" {
				t.Errorf("an unknown creator model must say why: %+v", cr)
			}
		})
	}
}

// AC1 (engaged route): the performer accepts and reports changes on the
// container's own backlog → ready edge; the repaired tags are on the record and in
// the payload the gate judged.
func TestReportedChangesAreInTheGatePayloadOnTheEngagedEdge(t *testing.T) {
	for _, b := range changesBindings {
		t.Run(b.name, func(t *testing.T) {
			w := newChangesWorld(t, b)
			w.report(t, w.repairs())

			w.mustDriven(t, "story", "set", w.epic, "--status", "ready")
			w.grantsNothingButReading(t)

			payload, err := os.ReadFile(w.gatePayload)
			if err != nil {
				t.Fatalf("the gate never ran: %v", err)
			}
			if !strings.Contains(string(payload), "lane:trunk") {
				t.Errorf("the gate must judge the repaired record; payload:\n%s", payload)
			}
			epic := w.story(t, w.epic)
			if epic.Status != "ready" || !carriesTag(epic.Tags, "lane:trunk") {
				t.Errorf("epic = %+v, want ready with lane:trunk on the record", epic)
			}
			if a := w.story(t, w.a); !carriesTag(a.Tags, "order:2") {
				t.Errorf("child a tags = %v, want order:2", a.Tags)
			}
		})
	}
}

// AC3 end to end: a batch that breaks a limit applies nothing, whatever the
// transport.
func TestReportedChangesBreakingALimitApplyNothing(t *testing.T) {
	for _, b := range changesBindings {
		t.Run(b.name, func(t *testing.T) {
			w := newChangesWorld(t, b)
			w.report(t, map[string]any{"decision": "accept", "changes": []map[string]any{
				{"op": "add-tags", "story": w.a, "tags": []string{"order:2"}},
				{"op": "add-tags", "story": w.a, "tags": []string{"order:3"}, "title": "renamed"},
			}})
			out, err := w.runDriven(t, "story", "ready", w.epic)
			if err == nil || !strings.Contains(out, "changes[1]") || !strings.Contains(out, "changes the definition of existing story") {
				t.Fatalf("want the round refused naming the change and the limit, err=%v\n%s", err, out)
			}
			if a := w.story(t, w.a); carriesTag(a.Tags, "order:2") {
				t.Errorf("nothing may be applied: %v", a.Tags)
			}
		})
	}
}

// AC3 end to end: a report with a field the contract does not know is refused
// before anything is applied.
func TestReportedChangesWithAnUnknownFieldAreRefused(t *testing.T) {
	w := newChangesWorld(t, changesBindings[0])
	w.report(t, map[string]any{"decision": "accept", "changes": []map[string]any{
		{"op": "add-tags", "story": w.a, "tags": []string{"order:2"}, "priority": "high"},
	}})
	out, err := w.runDriven(t, "story", "ready", w.epic)
	if err == nil || !strings.Contains(out, "changes[0]") {
		t.Fatalf("want the round refused naming the change, err=%v\n%s", err, out)
	}
	if a := w.story(t, w.a); carriesTag(a.Tags, "order:2") {
		t.Errorf("nothing may be applied: %v", a.Tags)
	}
}

// AC5 (story ready): a reject that carries changes applies them and records the
// reject; the status does not change. The engaged route, which parks the story
// blocked, is proved in internal/verb (TestRejectWithChangesParksTheEngagedStory)
// against a route that declares a park state.
func TestRejectWithChangesOnBothRoutes(t *testing.T) {
	rejecting := func(w changesWorld) map[string]any {
		return map[string]any{"decision": "reject", "notes": "premise wrong: no such lane", "changes": []map[string]any{
			{"op": "add-tags", "story": w.epic, "tags": []string{"lane:trunk"}},
		}}
	}
	b := changesBindings[0]
	t.Run("story ready", func(t *testing.T) {
		w := newChangesWorld(t, b)
		w.report(t, rejecting(w))
		out, err := w.runDriven(t, "story", "ready", w.epic)
		if err == nil || !strings.Contains(out, "premise wrong") {
			t.Fatalf("want a not-accepted round with the notes, err=%v\n%s", err, out)
		}
		epic := w.story(t, w.epic)
		if epic.Status != "backlog" || !carriesTag(epic.Tags, "lane:trunk") || carriesTag(epic.Tags, "ready") {
			t.Errorf("epic = %+v, want backlog, lane:trunk applied, unmarked", epic)
		}
		if rows := w.rows(t, w.epic, "performer_reject"); len(rows) != 1 {
			t.Errorf("want one performer_reject row, got %+v", rows)
		}
	})
}
