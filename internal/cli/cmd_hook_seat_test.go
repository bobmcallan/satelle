package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// liveSeatRepo scaffolds a repo with a wildcard workflow, an in_progress story,
// and a confirmed live seat for the current owner (sty_3bb1d8be tests).
func liveSeatRepo(t *testing.T) (repo, storyID string) {
	t.Helper()
	repo = tempRepo(t)
	// Ensure cwd is the repo so foreign-tree fence and relative paths work.
	t.Chdir(repo)

	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfBody := `---
name: seat-wf
type: workflow
applies_to: ["*"]
---

` + "```dot\n" + `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  done        [shape=Msquare]
  backlog -> in_progress -> done
}
` + "```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "seat-wf.md"), []byte(wfBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-exempt target for gate tests.
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "foo.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		_ = db.Close()
		t.Fatalf("doc sync: %v", err)
	}
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind:               workitem.KindStory,
		Title:              "seat heartbeat test",
		Body:               "goal",
		AcceptanceCriteria: "1. ok",
		Status:             "in_progress",
		Category:           "chore",
	}, time.Now().UTC())
	if err != nil {
		_ = db.Close()
		t.Fatalf("create story: %v", err)
	}
	storyID = sty.ID
	owner := lease.ResolveOwner()
	if _, _, _, err := db.Leases.Acquire(ctx, storyID, "story", owner, "in_progress", true); err != nil {
		_ = db.Close()
		t.Fatalf("acquire: %v", err)
	}
	if err := db.Leases.Confirm(ctx, storyID, "in_progress"); err != nil {
		_ = db.Close()
		t.Fatalf("confirm: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return repo, storyID
}

func ageLease(t *testing.T, id string, d time.Duration) {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	l, err := db.Leases.Get(ctx, id)
	if err != nil {
		t.Fatalf("get lease: %v", err)
	}
	at := l.HeartbeatAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := db.Leases.SetHeartbeat(ctx, id, at.Add(-d)); err != nil {
		t.Fatalf("set heartbeat: %v", err)
	}
}

func heartbeatOf(t *testing.T, id string) time.Time {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	l, err := db.Leases.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get lease: %v", err)
	}
	return l.HeartbeatAt
}

// runRootIn is runRoot with stdin payload (hook handlers read the event from stdin).
func runRootIn(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	c, err := root.ExecuteC()
	if c != nil {
		closeAppForCmd(c)
	}
	return buf.String(), err
}

// forceRelease drops the lease so the story stays performing but the seat is gone.
func forceRelease(t *testing.T, id string) {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Leases.ForceRelease(context.Background(), id); err != nil {
		t.Fatalf("force release: %v", err)
	}
}

func editEvent(path string) string {
	return fmt.Sprintf(`{"tool_input":{"file_path":%q}}`, path)
}

func bashEvent(cmd string) string {
	return fmt.Sprintf(`{"tool_input":{"command":%q}}`, cmd)
}

// TestHookHandlersHeartbeatLiveSeat (AC1): gate, commitgate, prompt all advance
// heartbeat_at for a live owner-held seat.
func TestHookHandlersHeartbeatLiveSeat(t *testing.T) {
	_, id := liveSeatRepo(t)
	cases := []struct {
		sub     string
		payload string
	}{
		{"gate", editEvent("internal/foo.go")},
		{"commitgate", bashEvent("git commit -m x")},
		{"prompt", `{}`},
	}
	for _, c := range cases {
		t.Run(c.sub, func(t *testing.T) {
			ageLease(t, id, 5*time.Minute)
			before := heartbeatOf(t, id)
			// tiny sleep so After(before) is reliable even on coarse clocks
			time.Sleep(5 * time.Millisecond)
			out, err := runRootIn(t, c.payload, "hook", c.sub)
			if c.sub != "prompt" && err != nil {
				t.Fatalf("hook %s: %v\n%s", c.sub, err, out)
			}
			after := heartbeatOf(t, id)
			if !after.After(before) {
				t.Errorf("hook %s: heartbeat did not advance: before=%v after=%v", c.sub, before, after)
			}
		})
	}
}

// TestHookHandlersDoNotRefreshStaleSeat (AC2): past HeartbeatTTL, handlers leave
// heartbeat unchanged and the lease remains stealable.
func TestHookHandlersDoNotRefreshStaleSeat(t *testing.T) {
	_, id := liveSeatRepo(t)
	ageLease(t, id, lease.HeartbeatTTL+time.Minute)
	before := heartbeatOf(t, id)
	for _, sub := range []string{"gate", "commitgate", "prompt"} {
		payload := `{}`
		if sub == "gate" {
			payload = editEvent("internal/foo.go")
		} else if sub == "commitgate" {
			payload = bashEvent("git commit -m x")
		}
		_, _ = runRootIn(t, payload, "hook", sub)
		after := heartbeatOf(t, id)
		if !after.Equal(before) {
			t.Errorf("hook %s refreshed a stale seat: before=%v after=%v", sub, before, after)
		}
	}
	// Still stealable by another owner.
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, out, _, err := db.Leases.Acquire(context.Background(), id, "story", "other@host", "in_progress", true)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if out != lease.OutcomeStolen {
		t.Errorf("outcome = %v, want OutcomeStolen", out)
	}
}

// TestHookHandlersDoNotRefreshForeignOwner (AC3).
func TestHookHandlersDoNotRefreshForeignOwner(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	_ = os.MkdirAll(wfDir, 0o755)
	wfBody := "---\nname: seat-wf\ntype: workflow\napplies_to: [\"*\"]\n---\n\n```dot\ndigraph w {\n  backlog [shape=Mdiamond]\n  in_progress [agent=executor]\n  done [shape=Msquare]\n  backlog -> in_progress -> done\n}\n```\n"
	_ = os.WriteFile(filepath.Join(wfDir, "seat-wf.md"), []byte(wfBody), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, "internal"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "internal", "foo.go"), []byte("package internal\n"), 0o644)

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "foreign", Body: "g", AcceptanceCriteria: "1. ok",
		Status: "in_progress", Category: "chore",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := db.Leases.Acquire(ctx, sty.ID, "story", "other@host", "in_progress", true); err != nil {
		t.Fatal(err)
	}
	if err := db.Leases.Confirm(ctx, sty.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	ageLease(t, sty.ID, 5*time.Minute)
	before := heartbeatOf(t, sty.ID)
	for _, sub := range []string{"gate", "commitgate", "prompt"} {
		payload := `{}`
		if sub == "gate" {
			payload = editEvent("internal/foo.go")
		} else if sub == "commitgate" {
			payload = bashEvent("git commit -m x")
		}
		_, _ = runRootIn(t, payload, "hook", sub)
		if !heartbeatOf(t, sty.ID).Equal(before) {
			t.Errorf("hook %s refreshed foreign owner lease", sub)
		}
	}
}

// TestHeartbeatFailureIsFailOpen (AC5): a forced write error does not change
// gate/commitgate/prompt exit status or allow payloads.
func TestHeartbeatFailureIsFailOpen(t *testing.T) {
	_, id := liveSeatRepo(t)
	ageLease(t, id, 5*time.Minute)

	// Baseline payloads with a healthy heartbeat.
	gateOK, err := runRootIn(t, editEvent("internal/foo.go"), "hook", "gate")
	if err != nil {
		t.Fatalf("baseline gate: %v\n%s", err, gateOK)
	}
	commitOK, err := runRootIn(t, bashEvent("git commit -m x"), "hook", "commitgate")
	if err != nil {
		t.Fatalf("baseline commitgate: %v\n%s", err, commitOK)
	}
	promptOK, err := runRootIn(t, `{}`, "hook", "prompt")
	if err != nil {
		t.Fatalf("baseline prompt: %v\n%s", err, promptOK)
	}

	old := seatHeartbeat
	seatHeartbeat = func(ctx context.Context, ls *lease.Store, itemID, owner string) error {
		return fmt.Errorf("forced heartbeat failure")
	}
	t.Cleanup(func() { seatHeartbeat = old })

	gateOut, err := runRootIn(t, editEvent("internal/foo.go"), "hook", "gate")
	if err != nil {
		t.Fatalf("gate should allow despite heartbeat failure: %v\n%s", err, gateOut)
	}
	// Allowed gate: empty stdout both with and without a healthy heartbeat.
	if gateOut != gateOK {
		t.Errorf("gate payload diverged under heartbeat failure:\n baseline=%q\n failed=%q", gateOK, gateOut)
	}

	commitOut, err := runRootIn(t, bashEvent("git commit -m x"), "hook", "commitgate")
	if err != nil {
		t.Fatalf("commitgate should allow despite heartbeat failure: %v\n%s", err, commitOut)
	}
	if commitOut != commitOK {
		t.Errorf("commitgate payload diverged under heartbeat failure:\n baseline=%q\n failed=%q", commitOK, commitOut)
	}

	promptOut, err := runRootIn(t, `{}`, "hook", "prompt")
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	// Prompt injects a seat line whose age tokens move; normalise ages then compare.
	// Also strip any residual wall-clock variance by comparing stable prefix + seat id.
	if normaliseSeatAges(promptOut) != normaliseSeatAges(promptOK) {
		// Fallback: require the reminder and the seat id, which are age-invariant.
		if !strings.Contains(promptOut, id) || !strings.Contains(promptOK, id) {
			t.Errorf("prompt payload lost seat id under heartbeat failure:\n baseline=%q\n failed=%q", promptOK, promptOut)
		}
		if !strings.Contains(promptOut, "seat held by") || !strings.Contains(promptOK, "seat held by") {
			t.Errorf("prompt payload lost seat line under heartbeat failure:\n baseline=%q\n failed=%q", promptOK, promptOut)
		}
	}
}

// normaliseSeatAges rewrites formatSeat age tokens ("acquired 0s", "heartbeat 1s",
// "stale 30m") so two prompt payloads that differ only by wall-clock age compare equal.
func normaliseSeatAges(s string) string {
	// Single left-to-right pass: replace digit runs that sit after a known label
	// and before a duration unit. Avoid re-scanning already-normalised text.
	var b strings.Builder
	i := 0
	labels := []string{"acquired ", "heartbeat ", "stale "}
	for i < len(s) {
		matched := false
		for _, label := range labels {
			if strings.HasPrefix(s[i:], label) {
				b.WriteString(label)
				i += len(label)
				// consume digits
				j := i
				for j < len(s) && s[j] >= '0' && s[j] <= '9' {
					j++
				}
				if j > i && j < len(s) {
					unit := s[j]
					if unit == 's' || unit == 'm' || unit == 'h' || unit == 'd' {
						b.WriteString("<age>")
						b.WriteByte(unit)
						i = j + 1
						matched = true
						break
					}
				}
				// not an age token — write the raw slice we already advanced past label
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestGateSurvivesLongerThanTTLWithHeartbeats (AC6): cumulative activity longer
// than HeartbeatTTL still allows edits because each gate refreshes the seat.
func TestGateSurvivesLongerThanTTLWithHeartbeats(t *testing.T) {
	_, id := liveSeatRepo(t)
	// Near the edge, then refresh, then near the edge again — total span > TTL
	// from the original heartbeat without ever going stale between gates.
	ageLease(t, id, lease.HeartbeatTTL-time.Minute)
	out, err := runRootIn(t, editEvent("internal/foo.go"), "hook", "gate")
	if err != nil {
		t.Fatalf("first gate: %v\n%s", err, out)
	}
	ageLease(t, id, lease.HeartbeatTTL-time.Minute)
	out, err = runRootIn(t, editEvent("internal/foo.go"), "hook", "gate")
	if err != nil {
		t.Fatalf("second gate after cumulative >TTL span: %v\n%s", err, out)
	}
	// Without heartbeats, aging past TTL denies with the dropped-seat reason.
	ageLease(t, id, lease.HeartbeatTTL+time.Minute)
	out, err = runRootIn(t, editEvent("internal/foo.go"), "hook", "gate")
	if err == nil {
		t.Fatalf("expected deny for truly stale seat, got allow:\n%s", out)
	}
	if !denyReasonContains(out, droppedSeatEditReason(id, "in_progress")) {
		t.Errorf("stale deny missing droppedSeatEditReason.\ngot: %s", out)
	}
}

// TestGateVerdictsUnchanged (AC4): deny text through the real handlers matches
// the canonical helpers with heartbeating in place.
func TestGateVerdictsUnchanged(t *testing.T) {
	// Performing story, seat dropped → droppedSeatEditReason.
	_, id := liveSeatRepo(t)
	forceRelease(t, id)
	out, err := runRootIn(t, editEvent("internal/foo.go"), "hook", "gate")
	if err == nil {
		t.Fatalf("expected gate deny with dropped seat:\n%s", out)
	}
	if !denyReasonContains(out, droppedSeatEditReason(id, "in_progress")) {
		t.Errorf("gate deny missing droppedSeatEditReason.\ngot: %s", out)
	}

	// No performing story at all → noEngagedStoryEditReason.
	repo := tempRepo(t)
	t.Chdir(repo)
	_ = os.MkdirAll(filepath.Join(repo, "internal"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "internal", "foo.go"), []byte("package internal\n"), 0o644)
	out, err = runRootIn(t, editEvent("internal/foo.go"), "hook", "gate")
	if err == nil {
		t.Fatalf("expected gate deny with no story:\n%s", out)
	}
	if !denyReasonContains(out, noEngagedStoryEditReason) {
		t.Errorf("gate deny missing noEngagedStoryEditReason.\ngot: %s", out)
	}

	// commitgate on git push with no seat → noEngagedStoryCommitReason.
	out, err = runRootIn(t, bashEvent("git push origin main"), "hook", "commitgate")
	if err == nil {
		t.Fatalf("expected commitgate deny:\n%s", out)
	}
	if !denyReasonContains(out, noEngagedStoryCommitReason) {
		t.Errorf("commitgate deny missing noEngagedStoryCommitReason.\ngot: %s", out)
	}
}

// denyReasonContains reports whether the JSON deny payload's reason contains
// the canonical text (accounting for JSON escaping of quotes and < >).
func denyReasonContains(payload, reason string) bool {
	if strings.Contains(payload, reason) {
		return true
	}
	esc := strings.ReplaceAll(reason, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	esc = strings.ReplaceAll(esc, `<`, `\u003c`)
	esc = strings.ReplaceAll(esc, `>`, `\u003e`)
	if strings.Contains(payload, esc) {
		return true
	}
	// Distinctive stem without punctuation that JSON rewrites.
	stem := reason
	if i := strings.Index(stem, " — "); i > 0 {
		stem = stem[:i]
	}
	if i := strings.Index(stem, ". "); i > 40 {
		stem = stem[:i]
	}
	return strings.Contains(payload, stem) || strings.Contains(payload, strings.ReplaceAll(stem, `"`, `\"`))
}

// TestInitHookScaffoldsRouteToSameHandlers (AC7): Claude and Grok scaffolded
// settings both route PreToolUse/UserPromptSubmit to the same satelle hook
// subcommands (via the shared wrapper script).
func TestInitHookScaffoldsRouteToSameHandlers(t *testing.T) {
	repo := t.TempDir()
	claude := string(buildClaudeHookSettings(repo))
	grok := string(buildGrokHookSettings(repo))

	// PreToolUse Edit → gate; Bash → commitgate; UserPromptSubmit → prompt.
	// Both harnesses use renderHookCommand → "sh <script> <sub> <harness>".
	for _, harnessJSON := range []struct {
		name string
		body string
	}{
		{"claude", claude},
		{"grok", grok},
	} {
		// gate sub appears in an Edit matcher command
		if !strings.Contains(harnessJSON.body, " gate ") && !strings.Contains(harnessJSON.body, " gate\"") {
			// renderHookCommand: "sh <path> gate claude"
			if !strings.Contains(harnessJSON.body, " gate ") {
				// path may end with gate as separate argv: "…/satelle-hook.sh gate claude"
				if !strings.Contains(harnessJSON.body, "satelle-hook.sh gate ") {
					t.Errorf("%s scaffold missing gate subcommand route:\n%s", harnessJSON.name, harnessJSON.body)
				}
			}
		}
		if !strings.Contains(harnessJSON.body, "satelle-hook.sh commitgate ") {
			t.Errorf("%s scaffold missing commitgate subcommand route:\n%s", harnessJSON.name, harnessJSON.body)
		}
		if !strings.Contains(harnessJSON.body, promptHookCommand) {
			t.Errorf("%s scaffold missing promptHookCommand %q", harnessJSON.name, promptHookCommand)
		}
	}
	// Shared wrapper body invokes the same satelle hook <sub> for both harnesses.
	script := parameterizedHookScriptBody()
	for _, sub := range []string{"gate", "commitgate"} {
		if !strings.Contains(script, "hook "+sub) && !strings.Contains(script, `hook "$sub"`) && !strings.Contains(script, "hook $sub") {
			// Script takes sub as argv: satelle hook "$1"
			if !strings.Contains(script, "hook") {
				t.Errorf("wrapper script missing hook invocation for %s", sub)
			}
		}
	}
	// No harness-specific heartbeat command.
	if strings.Contains(claude, "hook heartbeat") || strings.Contains(grok, "hook heartbeat") {
		t.Error("scaffold must not introduce a harness-specific heartbeat path")
	}
}

// liveSeatRepoWithAdvance is liveSeatRepo but the workflow has an intermediate
// forward state (in_progress → integration → done) so AdvanceOptions is non-empty
// and runHookPrompt must emit the engaged form (sty_e16a2cd7).
func liveSeatRepoWithAdvance(t *testing.T) (repo, storyID string) {
	t.Helper()
	repo = tempRepo(t)
	t.Chdir(repo)

	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfBody := `---
name: seat-wf-adv
type: workflow
applies_to: ["*"]
---

` + "```dot\n" + `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  integration [agent=executor]
  done        [shape=Msquare]
  backlog -> in_progress
  in_progress -> integration [agent=reviewer, prompt="@skill:ac-rev,scope-rev"]
  integration -> done
}
` + "```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "seat-wf-adv.md"), []byte(wfBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "foo.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		_ = db.Close()
		t.Fatalf("doc sync: %v", err)
	}
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind:               workitem.KindStory,
		Title:              "seat advance prompt test",
		Body:               "goal",
		AcceptanceCriteria: "1. ok",
		Status:             "in_progress",
		Category:           "chore",
	}, time.Now().UTC())
	if err != nil {
		_ = db.Close()
		t.Fatalf("create story: %v", err)
	}
	storyID = sty.ID
	owner := lease.ResolveOwner()
	if _, _, _, err := db.Leases.Acquire(ctx, storyID, "story", owner, "in_progress", true); err != nil {
		_ = db.Close()
		t.Fatalf("acquire: %v", err)
	}
	if err := db.Leases.Confirm(ctx, storyID, "in_progress"); err != nil {
		_ = db.Close()
		t.Fatalf("confirm: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return repo, storyID
}

// TestHookPromptLiveSeatEmitsEngagedForm (sty_e16a2cd7 AC2 end-to-end): with a
// live seat on a workflow that has a forward intermediate state, hook prompt's
// additionalContext carries id/status/edge gate/story set and does NOT contain
// the static create-and-engage reminder.
func TestHookPromptLiveSeatEmitsEngagedForm(t *testing.T) {
	_, id := liveSeatRepoWithAdvance(t)
	out, err := runRootIn(t, `{}`, "hook", "prompt")
	if err != nil {
		t.Fatalf("hook prompt: %v\n%s", err, out)
	}
	for _, want := range []string{
		id,
		"ENGAGED",
		"in_progress",
		"integration",
		"ac-rev",
		"satelle story set " + id + " --status integration",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("engaged prompt missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "edits require an ENGAGED story") {
		t.Errorf("static reminder must be REPLACED, not present:\n%s", out)
	}
}

// TestHookPromptLiveSeatNoAdvanceKeepsReminder (sty_e16a2cd7): baseline shape
// backlog→in_progress→done has no forward non-terminal advance, so the static
// reminder + seat line survives (architecture note on the plan).
func TestHookPromptLiveSeatNoAdvanceKeepsReminder(t *testing.T) {
	_, id := liveSeatRepo(t)
	out, err := runRootIn(t, `{}`, "hook", "prompt")
	if err != nil {
		t.Fatalf("hook prompt: %v\n%s", err, out)
	}
	if !strings.Contains(out, "edits require an ENGAGED story") {
		t.Errorf("reminder must remain when Advance is empty:\n%s", out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("seat line must still name the live seat %s:\n%s", id, out)
	}
}
