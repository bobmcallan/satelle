//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Panel gates: a route declares seats and a combine skill, the engine runs the
// reviewer once per seat, and the check's exit code is the skill verdict.
// Changing the declaration changes the outcome; no rule name is compiled.

const panelReviewSkill = `---
name: panel-review
type: skill
description: Fixture reviewer judged once per panel seat.
tags: [type:skill, type:reviewer]
---

# Panel review

Return JSON with decision and notes. decision is accept or reject.
`

const panelCheckSkill = `---
name: panel-check
type: skill
description: Fixture reviewer that is itself a functional check.
tags: [type:skill, type:functional-check]
---

# Panel check

The check is this skill's one decision.

` + "```check\n#!/bin/sh\necho ok\nexit 0\n```\n"

func panelRoute(panel, combine, extra string) (done, step string) {
	step = `[meta]
name = "step"
type = "workflow"
scope = "project"
description = "panel fixture"

[panel-raised]
status = "backlog"
start = true

[panel-coded]
status = "in_progress"
reviewers = ["panel-review"]
panel = ` + panel + `
`
	if combine != "" {
		step += "combine = \"" + combine + "\"\n"
	}
	step += `requires = ["panel-raised"]

[panel-closed]
status = "done"
terminal = true
requires = ["panel-coded"]

[[gate]]
skill = "satelle-estimate-actual-review"
on = ["__never__"]
`
	step += extra
	done = `[meta]
name = "done"
type = "workflow"
scope = "project"
description = "panel fixture"

["*"]
obligations = ["panel-raised", "panel-coded", "panel-closed"]
`
	return done, step
}

func writePanelSeats(t *testing.T, repo, record string, seats []string) {
	t.Helper()
	if err := os.MkdirAll(record, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(repo, "panel-seat.sh")
	script := `#!/bin/sh
seat="$1"
record="$2"
shift 2
mkdir -p "$record"
printf '%s\n' "$@" > "$record/$seat.argv"
cat > "$record/$seat.stdin"
echo "$seat" >> "$record/invoked"
if [ -f "$record/mode-$seat" ]; then
  cat "$record/mode-$seat"
  exit 0
fi
printf '%s\n' "{\"decision\":\"accept\",\"notes\":\"from-$seat\"}"
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, seat := range seats {
		// One-shot flags ride the argv the stub records. No resume flag is added.
		fmtSeat(t, &b, seat, stub, record)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"), b.String())
}

func fmtSeat(t *testing.T, b *strings.Builder, seat, stub, record string) {
	t.Helper()
	b.WriteString("[" + seat + "]\n")
	b.WriteString("role = \"reviewer\"\n")
	b.WriteString("interface = \"command\"\n")
	b.WriteString("isolation = \"operator-attested\"\n")
	b.WriteString("command = \"" + stub + " " + seat + " " + record + " --no-session -p {system}\"\n\n")
}

func writePanelFixture(t *testing.T, repo, record, panel, combine, extra string, seats []string) {
	t.Helper()
	mustRun(t, testBin, repo, "init")
	done, step := panelRoute(panel, combine, extra)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), step)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "panel-review.md"), panelReviewSkill)
	writePanelSeats(t, repo, record, seats)
	mustRun(t, testBin, repo, "reindex")
}

func panelStory(t *testing.T, repo, title string) string {
	t.Helper()
	out := mustRun(t, testBin, repo, "story", "create",
		"--title", title,
		"--body", "Prove a panel runs the reviewer once per seat and folds the check.",
		"--acceptance", "1. each declared seat runs once",
		"--category", "feature")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id:\n%s", out)
	}
	return id
}

type panelRow struct {
	Kind    string          `json:"kind"`
	Body    string          `json:"body"`
	Payload json.RawMessage `json:"payload"`
}

func panelLedger(t *testing.T, repo, id string) []panelRow {
	t.Helper()
	out := mustRun(t, testBin, repo, "ledger", "list", "--story", id, "--json")
	var rows []panelRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ledger list: %v\n%s", err, out)
	}
	return rows
}

type panelPayload struct {
	Skill   string `json:"skill"`
	Accept  bool   `json:"accept"`
	Notes   string `json:"notes"`
	Combine string `json:"combine"`
	Seat    string `json:"seat"`
	Command string `json:"command"`
	Seats   []struct {
		Seat        string `json:"seat"`
		Accept      bool   `json:"accept"`
		Unavailable bool   `json:"unavailable"`
		Notes       string `json:"notes"`
	} `json:"seats"`
}

func reviewRows(t *testing.T, rows []panelRow, kind string) []panelPayload {
	t.Helper()
	var out []panelPayload
	for _, row := range rows {
		if row.Kind != kind {
			continue
		}
		var p panelPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			t.Fatalf("payload: %v\n%s", err, row.Payload)
		}
		out = append(out, p)
	}
	return out
}

func TestPanelRunsEachSeatColdAndFolds(t *testing.T) {
	repo := t.TempDir()
	record := filepath.Join(repo, "panel-record")
	writePanelFixture(t, repo, record, `["seat-a", "seat-b"]`, "satelle-panel-all-accept", "", []string{"seat-a", "seat-b"})
	id := panelStory(t, repo, "Panel once per seat")

	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "in_progress"`) {
		t.Fatalf("combined accept should advance:\n%s", got)
	}

	invoked, err := os.ReadFile(filepath.Join(record, "invoked"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(invoked))
	if strings.Join(lines, ",") != "seat-a,seat-b" {
		t.Fatalf("invoked = %q, want seat-a then seat-b", lines)
	}
	for _, seat := range []string{"seat-a", "seat-b"} {
		argv, err := os.ReadFile(filepath.Join(record, seat+".argv"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(argv), "--no-session") || !strings.Contains(string(argv), "-p") {
			t.Errorf("%s argv is not a one-shot invocation:\n%s", seat, argv)
		}
		if strings.Contains(string(argv), "--resume") || strings.Contains(string(argv), "--session") {
			t.Errorf("%s argv resumes a conversation:\n%s", seat, argv)
		}
	}
	aOut := "from-seat-a"
	bIn, _ := os.ReadFile(filepath.Join(record, "seat-b.stdin"))
	bArgv, _ := os.ReadFile(filepath.Join(record, "seat-b.argv"))
	if strings.Contains(string(bIn), aOut) || strings.Contains(string(bArgv), aOut) {
		t.Errorf("seat-a stdout leaked into seat-b argv or stdin")
	}
	aIn, _ := os.ReadFile(filepath.Join(record, "seat-a.stdin"))
	aArgv, _ := os.ReadFile(filepath.Join(record, "seat-a.argv"))
	if strings.Contains(string(aIn), "from-seat-b") || strings.Contains(string(aArgv), "from-seat-b") {
		t.Errorf("seat-b stdout leaked into seat-a argv or stdin")
	}

	rows := panelLedger(t, repo, id)
	accepts := reviewRows(t, rows, "review_accept")
	if len(accepts) != 1 || accepts[0].Skill != "panel-review" || accepts[0].Combine != "satelle-panel-all-accept" || len(accepts[0].Seats) != 2 {
		t.Fatalf("combined accept = %+v, want one panel-review row with two seats", accepts)
	}
	if n := countSeatInvocations(rows, "panel-review"); n != 2 {
		t.Fatalf("agent_invocation seats = %d, want 2", n)
	}
}

func countSeatInvocations(rows []panelRow, skill string) int {
	n := 0
	for _, row := range rows {
		if row.Kind != "agent_invocation" {
			continue
		}
		var p panelPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			continue
		}
		if p.Skill == skill && p.Seat != "" && p.Command != "" {
			n++
		}
	}
	return n
}

func TestPanelAllAcceptRejectsADissent(t *testing.T) {
	repo := t.TempDir()
	record := filepath.Join(repo, "panel-record")
	writePanelFixture(t, repo, record, `["seat-a", "seat-b"]`, "satelle-panel-all-accept", "", []string{"seat-a", "seat-b"})
	writeFile(t, filepath.Join(record, "mode-seat-b"), "{\"decision\":\"reject\",\"notes\":\"stub-reject-notes\"}\n")
	id := panelStory(t, repo, "All-accept dissent")

	out, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if err == nil {
		t.Fatalf("all-accept must reject a dissent:\n%s", out)
	}
	if !strings.Contains(out, "panel-review") {
		t.Errorf("refusal must name the reviewer skill:\n%s", out)
	}
	if strings.Contains(out, "rejected by seat-") {
		t.Errorf("refusal must not treat a seat as a skill reject:\n%s", out)
	}
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "backlog"`) {
		t.Fatalf("status must stay backlog:\n%s", got)
	}
	rows := panelLedger(t, repo, id)
	rejects := reviewRows(t, rows, "review_reject")
	if len(rejects) != 1 || rejects[0].Skill != "panel-review" || rejects[0].Combine != "satelle-panel-all-accept" {
		t.Fatalf("review_reject = %+v, want one skill row", rejects)
	}
	if len(rejects[0].Seats) != 2 || rejects[0].Seats[1].Seat != "seat-b" || rejects[0].Seats[1].Notes != "stub-reject-notes" || rejects[0].Seats[1].Accept {
		t.Fatalf("seats = %+v, want the stub notes on the rejecting seat", rejects[0].Seats)
	}
	for _, row := range rows {
		if row.Kind != "review_reject" {
			continue
		}
		var p panelPayload
		_ = json.Unmarshal(row.Payload, &p)
		if p.Skill == "seat-a" || p.Skill == "seat-b" {
			t.Errorf("seat name must not be a review_reject skill: %+v", p)
		}
	}
	if n := countSeatInvocations(rows, "panel-review"); n != 2 {
		t.Fatalf("started seats = %d, want 2 invocations", n)
	}
}

func TestPanelMajorityFollowsTheDeclaredCheck(t *testing.T) {
	repo := t.TempDir()
	record := filepath.Join(repo, "panel-record")
	writePanelFixture(t, repo, record, `["seat-a", "seat-b", "seat-c"]`, "satelle-panel-majority", "", []string{"seat-a", "seat-b", "seat-c"})
	writeFile(t, filepath.Join(record, "mode-seat-c"), "{\"decision\":\"reject\",\"notes\":\"minority\"}\n")
	id := panelStory(t, repo, "Majority two to one")

	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	rows := panelLedger(t, repo, id)
	accepts := reviewRows(t, rows, "review_accept")
	if len(accepts) != 1 || len(accepts[0].Seats) != 3 || accepts[0].Combine != "satelle-panel-majority" {
		t.Fatalf("majority accept = %+v", accepts)
	}
	var dissent bool
	for _, s := range accepts[0].Seats {
		if s.Seat == "seat-c" && !s.Accept && s.Notes == "minority" {
			dissent = true
		}
	}
	if !dissent {
		t.Fatalf("accept row must carry the rejecting seat: %+v", accepts[0].Seats)
	}
	if n := countSeatInvocations(rows, "panel-review"); n != 3 {
		t.Fatalf("invocations = %d, want 3", n)
	}

	// One accept and two rejects is not a strict majority. A fresh repo, so the
	// accepted story above does not hold the engagement seat.
	repo2 := t.TempDir()
	record2 := filepath.Join(repo2, "panel-record")
	writePanelFixture(t, repo2, record2, `["seat-a", "seat-b", "seat-c"]`, "satelle-panel-majority", "", []string{"seat-a", "seat-b", "seat-c"})
	writeFile(t, filepath.Join(record2, "mode-seat-b"), "{\"decision\":\"reject\",\"notes\":\"no\"}\n")
	writeFile(t, filepath.Join(record2, "mode-seat-c"), "{\"decision\":\"reject\",\"notes\":\"no\"}\n")
	id2 := panelStory(t, repo2, "Majority one to two")
	out, err := run(t, testBin, repo2, "story", "set", id2, "--status", "in_progress")
	if err == nil {
		t.Fatalf("1-2 must reject:\n%s", out)
	}
	rows = panelLedger(t, repo2, id2)
	rejects := reviewRows(t, rows, "review_reject")
	if len(rejects) != 1 || len(rejects[0].Seats) != 3 || rejects[0].Combine != "satelle-panel-majority" {
		t.Fatalf("majority reject = %+v", rejects)
	}
}

func TestPanelUnavailableIsNotAnAccept(t *testing.T) {
	repo := t.TempDir()
	record := filepath.Join(repo, "panel-record")
	// seat-missing is declared and has no binding.
	writePanelFixture(t, repo, record, `["seat-a", "seat-missing"]`, "satelle-panel-all-accept", "", []string{"seat-a"})
	id := panelStory(t, repo, "Missing seat")
	out, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if err == nil {
		t.Fatalf("all-accept must reject an unavailable seat:\n%s", out)
	}
	rows := panelLedger(t, repo, id)
	rejects := reviewRows(t, rows, "review_reject")
	if len(rejects) != 1 || len(rejects[0].Seats) != 2 {
		t.Fatalf("seats = %+v", rejects)
	}
	if rejects[0].Seats[0].Seat != "seat-a" || !rejects[0].Seats[0].Accept || rejects[0].Seats[0].Unavailable {
		t.Fatalf("first seat must keep its real verdict: %+v", rejects[0].Seats[0])
	}
	miss := rejects[0].Seats[1]
	if miss.Seat != "seat-missing" || miss.Accept || !miss.Unavailable {
		t.Fatalf("missing seat = %+v, want unavailable and not accept", miss)
	}
	for _, row := range rows {
		if row.Kind != "agent_invocation" || !strings.Contains(string(row.Payload), "seat-missing") {
			continue
		}
		t.Fatalf("a seat that never started must not have an invocation row:\n%s", row.Payload)
	}
	if !strings.Contains(rejects[0].Notes, "unavailable: seat-missing") {
		t.Errorf("combine check must name the unavailable seat, notes=%q", rejects[0].Notes)
	}

	// Majority of a declared 2 is 2. One accept is not enough.
	done, step := panelRoute(`["seat-a", "seat-missing"]`, "satelle-panel-majority", "")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), step)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), done)
	mustRun(t, testBin, repo, "reindex")
	id2 := panelStory(t, repo, "Missing seat majority")
	out, err = run(t, testBin, repo, "story", "set", id2, "--status", "in_progress")
	if err == nil {
		t.Fatalf("one of two is not a strict majority:\n%s", out)
	}
	rows = panelLedger(t, repo, id2)
	rejects = reviewRows(t, rows, "review_reject")
	if len(rejects) != 1 || !rejects[0].Seats[1].Unavailable || rejects[0].Seats[1].Accept {
		t.Fatalf("majority missing seat = %+v", rejects)
	}

	// Non-JSON output is unavailable, and the other seat's verdict remains.
	record3 := filepath.Join(repo, "panel-record-3")
	writePanelSeats(t, repo, record3, []string{"seat-a", "seat-b"})
	done, step = panelRoute(`["seat-a", "seat-b"]`, "satelle-panel-all-accept", "")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), step)
	writeFile(t, filepath.Join(record3, "mode-seat-b"), "this is not a verdict\n")
	mustRun(t, testBin, repo, "reindex")
	id3 := panelStory(t, repo, "Non-JSON seat")
	out, err = run(t, testBin, repo, "story", "set", id3, "--status", "in_progress")
	if err == nil {
		t.Fatalf("non-JSON seat must not count as accept:\n%s", out)
	}
	rows = panelLedger(t, repo, id3)
	rejects = reviewRows(t, rows, "review_reject")
	if len(rejects) != 1 || len(rejects[0].Seats) != 2 {
		t.Fatalf("non-JSON row = %+v", rejects)
	}
	if !rejects[0].Seats[0].Accept || rejects[0].Seats[0].Unavailable {
		t.Fatalf("other seat's verdict must remain: %+v", rejects[0].Seats[0])
	}
	if rejects[0].Seats[1].Accept || !rejects[0].Seats[1].Unavailable {
		t.Fatalf("non-JSON seat = %+v, want unavailable and not accept", rejects[0].Seats[1])
	}
}

func TestPanelDeclarationChangeNeedsNoCodeChange(t *testing.T) {
	repo := t.TempDir()
	record := filepath.Join(repo, "panel-record")
	writePanelFixture(t, repo, record, `["seat-a", "seat-b", "seat-c"]`, "satelle-panel-all-accept", "", []string{"seat-a", "seat-b", "seat-c"})
	writeFile(t, filepath.Join(record, "mode-seat-a"), "{\"decision\":\"accept\",\"notes\":\"from-seat-a\"}\n")
	writeFile(t, filepath.Join(record, "mode-seat-b"), "{\"decision\":\"accept\",\"notes\":\"from-seat-b\"}\n")
	writeFile(t, filepath.Join(record, "mode-seat-c"), "{\"decision\":\"reject\",\"notes\":\"from-seat-c\"}\n")
	id := panelStory(t, repo, "Declaration flip")

	out, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if err == nil {
		t.Fatalf("all-accept must reject accept/accept/reject:\n%s", out)
	}
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(record, "invoked")))); got != "seat-a\nseat-b\nseat-c" {
		t.Fatalf("invoked sections = %q, want seat-a, seat-b, seat-c", got)
	}

	// Same repo, same binary: only the combine declaration changes.
	_ = os.Remove(filepath.Join(record, "invoked"))
	done, step := panelRoute(`["seat-a", "seat-b", "seat-c"]`, "satelle-panel-majority", "")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), step)
	mustRun(t, testBin, repo, "reindex")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(record, "invoked")))); got != "seat-a\nseat-b\nseat-c" {
		t.Fatalf("after the flip, invoked = %q, want the same three sections", got)
	}

	// A repo-authored third rule, and a different panel, with the same binary.
	repoB := t.TempDir()
	recordB := filepath.Join(repoB, "panel-record")
	writePanelFixture(t, repoB, recordB, `["seat-b", "seat-c"]`, "satelle-panel-any-accept", "", []string{"seat-a", "seat-b", "seat-c"})
	writeFile(t, filepath.Join(recordB, "mode-seat-b"), "{\"decision\":\"accept\",\"notes\":\"from-seat-b\"}\n")
	writeFile(t, filepath.Join(recordB, "mode-seat-c"), "{\"decision\":\"reject\",\"notes\":\"from-seat-c\"}\n")
	writeFile(t, filepath.Join(repoB, ".satelle", "skills", "satelle-panel-any-accept.md"), anyAcceptSkill)
	mustRun(t, testBin, repoB, "reindex")
	idB := panelStory(t, repoB, "Authored any-accept")
	mustRun(t, testBin, repoB, "story", "set", idB, "--status", "in_progress")
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(recordB, "invoked")))); got != "seat-b\nseat-c" {
		t.Fatalf("repo B invoked = %q, want exactly seat-b and seat-c", got)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const anyAcceptSkill = `---
name: satelle-panel-any-accept
type: skill
description: Exit 0 when any declared seat accepted and is available.
tags: [type:skill, type:functional-check]
---

# Any-accept

Exit 0 when any seat has accept and is not unavailable.

` + "```check\n#!/usr/bin/env bash\nset -uo pipefail\npython3 -c \"\nimport json, sys\ndata = json.loads(sys.stdin.read())\nfor s in data.get('seats') or []:\n    if s.get('accept') and not s.get('unavailable'):\n        print('accept: ' + str(s.get('seat')))\n        sys.exit(0)\nprint('no accepting seat')\nsys.exit(1)\n\"\n```\n"

func TestAgentValidateNamesMissingCombineSkill(t *testing.T) {
	repo := t.TempDir()
	record := filepath.Join(repo, "panel-record")
	writePanelFixture(t, repo, record, `["seat-a", "seat-b"]`, "satelle-panel-not-a-rule", "", []string{"seat-a", "seat-b"})
	out, err := run(t, testBin, repo, "agent", "validate")
	if err == nil || !strings.Contains(out, "satelle-panel-not-a-rule") {
		t.Fatalf("validate must fail naming the missing combine skill: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "satelle-panel-not-a-rule.md"), strings.ReplaceAll(anyAcceptSkill, "satelle-panel-any-accept", "satelle-panel-not-a-rule"))
	out, err = run(t, testBin, repo, "agent", "validate")
	if err != nil && strings.Contains(out, "satelle-panel-not-a-rule") {
		t.Fatalf("adding the skill must clear the combine error with no binary change: %v\n%s", err, out)
	}
	if strings.Contains(out, "combine skill \"satelle-panel-not-a-rule\"") {
		t.Fatalf("combine skill still unresolved:\n%s", out)
	}
}

func TestPanelPlusBundleIsRefused(t *testing.T) {
	repo := t.TempDir()
	record := filepath.Join(repo, "panel-record")
	done, step := panelRoute(`["seat-a", "seat-b"]`, "satelle-panel-all-accept", "")
	step = strings.Replace(step, "reviewers = [\"panel-review\"]\n", "reviewers = [\"panel-review\", \"panel-review-b\"]\nbundle = true\n", 1)
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), step)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "panel-review.md"), panelReviewSkill)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "panel-review-b.md"), strings.ReplaceAll(panelReviewSkill, "name: panel-review", "name: panel-review-b"))
	writePanelSeats(t, repo, record, []string{"seat-a", "seat-b"})
	mustRun(t, testBin, repo, "reindex")

	vout, verr := run(t, testBin, repo, "agent", "validate")
	if verr == nil || !strings.Contains(vout, "panel") || !strings.Contains(vout, "bundle") {
		t.Fatalf("validate must name panel and bundle: %v\n%s", verr, vout)
	}
	id := panelStory(t, repo, "Panel and bundle")
	out, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if err == nil || !strings.Contains(out, "backlog→in_progress") || !strings.Contains(out, "panel") {
		t.Fatalf("gate must refuse the bundled panel by edge: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(record, "invoked")); statErr == nil {
		t.Fatal("a refused panel must not start seats")
	}
}

func TestPanelRefusesACheckSkillBeforeSeats(t *testing.T) {
	repo := t.TempDir()
	record := filepath.Join(repo, "panel-record")
	done, step := panelRoute(`["seat-a", "seat-b"]`, "satelle-panel-all-accept", "")
	step = strings.Replace(step, "reviewers = [\"panel-review\"]", "reviewers = [\"panel-check\"]", 1)
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), step)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "panel-check.md"), panelCheckSkill)
	writePanelSeats(t, repo, record, []string{"seat-a", "seat-b"})
	mustRun(t, testBin, repo, "reindex")
	id := panelStory(t, repo, "Check on a panel")
	out, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if err == nil || !strings.Contains(out, "panel-check") || !strings.Contains(out, "panel") {
		t.Fatalf("must refuse the check skill before seats: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(record, "invoked")); statErr == nil {
		t.Fatal("seats must not start when the reviewer is itself a check")
	}
}
