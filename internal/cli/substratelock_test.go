package cli

// sty_992cffc6 — the edit gate itself refuses substrate changes while a story
// holds a performing seat, on every harness. The lock is decided by one pure,
// harness-neutral predicate (substrateLocked) reached from `hook gate`; these
// tests pin the predicate, the gate wiring, the neutrality between a
// claude-shaped and a grok-shaped invocation, the lane out, the carve-outs, the
// opt-out, the ledger row, and the reported petilio regression (sty_cc9663e0).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// lockExemptToml is the config a seeded repo carries: .satelle/ and the
// footprint exempt from the engaged-story gate, dump names exempt by glob. The
// lock key is appended per case so absent and explicit-empty stay separate.
const lockExemptToml = `[review]
gate_create = false

[gate]
edit_exempt_paths = [".satelle/", ".gitignore", ".claude/", ".grok/", ".pi/", "/tmp/"]
edit_exempt_globs = ["sty_*_body.md", "sty_*_ac.md"]
`

func TestSubstrateLockedPredicate(t *testing.T) {
	root := "/repo"
	lock := []string{"/repo/.satelle/"}
	footprint := []string{"/repo/.gitignore", "/repo/.claude/", "/repo/.grok/", "/repo/.pi/"}
	globs := []string{"sty_*_body.md", "sty_*_ac.md"}
	cases := []struct {
		name   string
		lock   []string
		target string
		want   bool
	}{
		{"workflow binding", lock, "/repo/.satelle/workflows/agents.toml", true},
		{"skill", lock, "/repo/.satelle/skills/x.md", true},
		{"the data dir itself", lock, "/repo/.satelle", true},
		{"config", lock, "/repo/.satelle/satelle.toml", true},
		{"product code is not substrate", lock, "/repo/internal/x.go", false},
		{"a sibling with the same prefix text", lock, "/repo/.satellite/x.md", false},
		{"story dump under the lock is carved out", lock, "/repo/.satelle/sty_1_body.md", false},
		{"ac dump under the lock is carved out", lock, "/repo/.satelle/documents/sty_1_ac.md", false},
		{"claude footprint", lock, "/repo/.claude/settings.json", false},
		{"grok footprint", lock, "/repo/.grok/hooks/satelle.json", false},
		{"pi footprint", lock, "/repo/.pi/extensions/satelle.ts", false},
		{"managed gitignore", lock, "/repo/.gitignore", false},
		{"/tmp is never locked", lock, "/tmp/scratch/x.md", false},
		{"no lock roots locks nothing", nil, "/repo/.satelle/workflows/agents.toml", false},
		{"a lock root cannot lock the footprint", []string{"/repo/.claude/"}, "/repo/.claude/settings.json", false},
		{"a lock root cannot lock a dump glob", []string{"/repo/docs/"}, "/repo/docs/sty_9_body.md", false},
		{"an operator prefix is locked", []string{"/repo/policy/"}, "/repo/policy/rules.md", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := substrateLocked(tc.lock, footprint, globs, root, tc.target); got != tc.want {
				t.Fatalf("substrateLocked(%v, %q) = %v, want %v", tc.lock, tc.target, got, tc.want)
			}
		})
	}
}

// One answer about the footprint: the lock's carve-out starts from the seeded
// managed list, so a footprint entry added there is carved out here too, and
// .pi/ (deployed lazily by the pi extension) is carved out as well.
func TestSubstrateLockFootprintCoversManagedEntries(t *testing.T) {
	got := substrateLockFootprint()
	for _, want := range append(append([]string{}, managedEditExemptEntries...), ".pi/") {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("substrateLockFootprint() = %v is missing %q", got, want)
		}
	}
}

// claudeEditEvent carries no session_id, so the session is unstamped and binds
// to the one live seat the fixtures hold; the tests that need a session that
// matches NO seat present one explicitly (holdersRepo).
func claudeEditEvent(absPath string) string {
	b, _ := json.Marshal(map[string]any{
		"transcript_path": "/x/transcript.jsonl", "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": absPath, "old_string": "a", "new_string": "b"},
	})
	return string(b)
}

// grokEditEvent is the grok shape: camelCase envelope, its own tool name, and a
// repo-RELATIVE path.
func grokEditEvent(relPath string) string {
	b, _ := json.Marshal(map[string]any{
		"hookEventName": "pre_tool_use", "sessionId": "g1", "toolName": "search_replace",
		"toolInput": map[string]any{"filePath": relPath, "oldString": "a", "newString": "b"},
	})
	return string(b)
}

// denyReasonOf reads the model-visible reason out of either harness's deny
// envelope (claude: hookSpecificOutput.permissionDecisionReason; grok: reason).
func denyReasonOf(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "{")
	if i < 0 {
		t.Fatalf("no deny JSON on stdout: %q", out)
	}
	var env struct {
		Reason             string `json:"reason"`
		Decision           string `json:"decision"`
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	dec := json.NewDecoder(strings.NewReader(out[i:]))
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("deny JSON: %v in %q", err, out)
	}
	if env.HookSpecificOutput.PermissionDecision == "deny" {
		return env.HookSpecificOutput.PermissionDecisionReason
	}
	if env.Decision == "deny" {
		return env.Reason
	}
	t.Fatalf("stdout is not a deny envelope: %q", out)
	return ""
}

// lockRepo is a repo with one story holding a settled live seat at in_progress —
// a state the route allocates to the in-loop executor, so the ORDINARY gate
// would allow the edit and any refusal below can only be the substrate lock.
// gateExtra is appended under [gate] (the lock key, or nothing).
func lockRepo(t *testing.T, category, gateExtra string) (repo, storyID string) {
	t.Helper()
	repo = editStateRepo(t, "in_progress", "in_progress", false)
	cfg := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfg, []byte(lockExemptToml+gateExtra), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	items, err := db.Stories.List(ctx, workitem.ListFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("stories: %v n=%d", err, len(items))
	}
	if category != "" && category != items[0].Category {
		if _, err := db.Stories.Update(ctx, items[0].ID, workitem.UpdateInput{Category: &category}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	return repo, items[0].ID
}

// AC1: a locked path is refused under a held seat, and the deny names the
// seat-holding story, the locked path and the lane out.
func TestSubstrateLockDeniesLockedPathUnderSeat(t *testing.T) {
	repo, id := lockRepo(t, "feature", "") // key ABSENT: the default lock, no migrate has run
	for _, rel := range []string{".satelle/skills/x.md", ".satelle/workflows/agents.toml", ".satelle/satelle.toml"} {
		out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, rel)), "hook", "gate")
		if err == nil {
			t.Fatalf("%s must be refused while %s holds a performing seat:\n%s", rel, id, out)
		}
		reason := denyReasonOf(t, out)
		for _, want := range []string{id, rel, "substrate lock", `category "substrate"`, "satelle-workflow-change-review", "finish or park " + id} {
			if !strings.Contains(reason, want) {
				t.Errorf("deny for %s missing %q: %s", rel, want, reason)
			}
		}
	}
	// The lock is precise: product code the executor may edit stays allowed, so
	// the refusals above are the lock and not a seat that failed to resolve.
	if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, "internal", "foo.go")), "hook", "gate"); err != nil {
		t.Fatalf("product code under the executor seat must stay allowed: %v\n%s", err, out)
	}
}

// AC2: one shared predicate — the SAME path refused through a claude-shaped and
// a grok-shaped invocation yields the same reason, each in its own envelope.
func TestSubstrateLockSameDenialForClaudeAndGrokShapes(t *testing.T) {
	repo, _ := lockRepo(t, "feature", "")
	const rel = ".satelle/workflows/agents.toml"

	cOut, cErr := runRootIn(t, claudeEditEvent(filepath.Join(repo, rel)), "hook", "gate")
	gOut, gErr := runRootIn(t, grokEditEvent(rel), "hook", "gate")
	if cErr == nil || gErr == nil {
		t.Fatalf("both shapes must be refused: claude err=%v grok err=%v\nclaude:%s\ngrok:%s", cErr, gErr, cOut, gOut)
	}
	if !strings.Contains(cOut, "hookSpecificOutput") || strings.Contains(cOut, `"decision"`) {
		t.Errorf("claude must get its own envelope: %s", cOut)
	}
	if !strings.Contains(gOut, `"decision":"deny"`) || strings.Contains(gOut, "hookSpecificOutput") {
		t.Errorf("grok must get its own envelope: %s", gOut)
	}
	if c, g := denyReasonOf(t, cOut), denyReasonOf(t, gOut); c != g {
		t.Fatalf("the two harnesses must get one identical denial:\nclaude: %s\ngrok:   %s", c, g)
	}
}

// AC3: no story engaged — the exempt path stays writable, with the key absent
// and with it present; a released (seatless) story does not lock either.
func TestSubstrateLockInertWithoutSeat(t *testing.T) {
	for _, tc := range []struct{ name, gate string }{
		{"key absent", ""},
		{"key present", "lock_substrate_paths = [\".satelle/\"]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := tempRepo(t)
			t.Chdir(repo)
			if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(lockExemptToml+tc.gate), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate"); err != nil {
				t.Fatalf("with no story engaged an exempt path must stay writable: %v\n%s", err, out)
			}
		})
	}

	t.Run("seat released", func(t *testing.T) {
		repo, id := lockRepo(t, "feature", "")
		forceRelease(t, id)
		if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate"); err != nil {
			t.Fatalf("a story whose seat was released holds no performing seat, so the lock is inert: %v\n%s", err, out)
		}
	})
}

// AC4: a story in the substrate lane changes substrate under its own live seat;
// a story that is not in that lane is refused.
func TestSubstrateLockLaneOut(t *testing.T) {
	t.Run("substrate lane allowed", func(t *testing.T) {
		repo, _ := lockRepo(t, "substrate", "")
		if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate"); err != nil {
			t.Fatalf("a substrate-lane story must be able to change substrate: %v\n%s", err, out)
		}
		if rows := ledgerRows(t, ledger.KindSubstrateLockDeny); len(rows) != 0 {
			t.Errorf("an allowed edit must record no refusal, got %d", len(rows))
		}
	})
	t.Run("product story refused", func(t *testing.T) {
		repo, _ := lockRepo(t, "feature", "")
		if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate"); err == nil {
			t.Fatalf("a product story must be refused:\n%s", out)
		}
	})
}

// AC5: what stays writable while the lock is on.
func TestSubstrateLockCarveOutsStayWritable(t *testing.T) {
	repo, _ := lockRepo(t, "feature", "")
	writable := []string{
		filepath.Join(repo, ".satelle", "sty_abc12345_body.md"),
		filepath.Join(repo, ".satelle", "documents", "sty_abc12345_ac.md"),
		filepath.Join(repo, ".claude", "settings.json"),
		filepath.Join(repo, ".grok", "hooks", "satelle.json"),
		filepath.Join(repo, ".pi", "extensions", "satelle.ts"),
		filepath.Join(repo, ".gitignore"),
		"/tmp/satelle-lock-scratch.md",
		filepath.Join(os.TempDir(), "satelle-lock-scratch.md"),
	}
	for _, p := range writable {
		if out, err := runRootIn(t, claudeEditEvent(p), "hook", "gate"); err != nil {
			t.Errorf("%s must stay writable under the lock: %v\n%s", p, err, out)
		}
	}
	// A lock list that names a carve-out cannot lock it.
	repo2, _ := lockRepo(t, "feature", "lock_substrate_paths = [\".satelle/\", \".claude/\", \".pi/\"]\n")
	for _, p := range []string{filepath.Join(repo2, ".claude", "settings.json"), filepath.Join(repo2, ".pi", "extensions", "satelle.ts")} {
		if out, err := runRootIn(t, claudeEditEvent(p), "hook", "gate"); err != nil {
			t.Errorf("%s is the footprint satelle deploys and must never be locked: %v\n%s", p, err, out)
		}
	}
}

// AC6: which prefixes are locked is configuration. An operator list replaces the
// default, and an explicit empty list is the opt-out. Absent (default lock) and
// present-but-empty are separate hot-path cases.
func TestSubstrateLockConfigurableAndOptOut(t *testing.T) {
	t.Run("operator list locks its prefix, not the default", func(t *testing.T) {
		repo, _ := lockRepo(t, "feature", "lock_substrate_paths = [\"policy/\"]\n")
		if err := os.MkdirAll(filepath.Join(repo, "policy"), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, "policy", "rules.md")), "hook", "gate"); err == nil {
			t.Fatalf("the operator's prefix must be locked:\n%s", out)
		}
		if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate"); err != nil {
			t.Fatalf("a list that omits .satelle/ replaces the default: %v\n%s", err, out)
		}
	})
	t.Run("explicit empty list is the opt-out", func(t *testing.T) {
		repo, _ := lockRepo(t, "feature", "lock_substrate_paths = []\n")
		if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate"); err != nil {
			t.Fatalf("lock_substrate_paths = [] must leave the substrate writable: %v\n%s", err, out)
		}
	})
	t.Run("absent key is the default lock", func(t *testing.T) {
		repo, _ := lockRepo(t, "feature", "")
		if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate"); err == nil {
			t.Fatalf("an absent key must lock .satelle/ by default:\n%s", out)
		}
	})
	t.Run("help and the seeded config name the key", func(t *testing.T) {
		out, err := runRoot(t, "hook", "gate", "--help")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"lock_substrate_paths", ".satelle/", "satelle doctor", "edit_exempt_paths"} {
			if !strings.Contains(out, want) {
				t.Errorf("hook gate --help missing %q", want)
			}
		}
		if !strings.Contains(scaffoldToml, "# lock_substrate_paths = [\".satelle/\"]") {
			t.Error("the seeded satelle.toml must document the key beside edit_exempt_paths")
		}
		// The documented-but-commented key leaves the key ABSENT: the default lock,
		// never the opt-out.
		paths, optOut := config.ParseLockSubstratePaths(scaffoldToml)
		if optOut || !reflect.DeepEqual(paths, []string{".satelle/"}) {
			t.Errorf("seeded scaffold parses to (%v, optOut=%v)", paths, optOut)
		}
	})
}

// AC8: the refusal is a ledger row on the seat-holding story.
func TestSubstrateLockDenyIsRecordedOnLedger(t *testing.T) {
	repo, id := lockRepo(t, "feature", "")
	if _, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate"); err == nil {
		t.Fatal("expected a refusal")
	}
	rows := ledgerRows(t, ledger.KindSubstrateLockDeny)
	if len(rows) != 1 {
		t.Fatalf("want one %s row, got %d", ledger.KindSubstrateLockDeny, len(rows))
	}
	if rows[0].StoryID != id {
		t.Errorf("row story = %q, want %q", rows[0].StoryID, id)
	}
	var p substrateLockPayload
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil || p.Path != ".satelle/skills/x.md" || p.Lane != substrateLaneCategory {
		t.Errorf("payload = %s (%v), want path .satelle/skills/x.md and lane %s", rows[0].Payload, err, substrateLaneCategory)
	}
	if strings.Contains(string(rows[0].Payload), "claude") || strings.Contains(string(rows[0].Payload), "grok") {
		t.Errorf("the row must carry nothing harness-specific: %s", rows[0].Payload)
	}
}

// AC7, the petilio case (sty_cc9663e0): a story rewrites the [reviewer] binding
// in .satelle/workflows/agents.toml while performing. The harness applies an edit
// only when the hook allows it, so this drives the hook and applies the edit
// exactly then; the binding the story's NEXT gate resolves must be the one in
// force when the story started. The opt-out is the control: it lets the same
// edit through and the judge changes, so the test discriminates.
func TestSubstrateLockPetilioRewriteOfReviewerBinding(t *testing.T) {
	const before = "[executor]\nharness = \"in-loop\"\n\n[reviewer]\nharness = \"claude\"\n"
	const after = "[executor]\nharness = \"in-loop\"\n\n[reviewer]\nharness = \"grok\"\n"

	run := func(t *testing.T, gateExtra string) (applied bool, startBinding, nextGateBinding config.AgentBinding, onDisk string) {
		repo, _ := lockRepo(t, "feature", gateExtra)
		agentsPath := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
		if err := os.MkdirAll(filepath.Dir(agentsPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(agentsPath, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		load := func() config.AgentBinding {
			ac, err := config.LoadAgents(filepath.Join(repo, ".satelle", "workflows"))
			if err != nil {
				t.Fatalf("load agents: %v", err)
			}
			return ac.Reviewer
		}
		startBinding = load()
		if _, err := runRootIn(t, claudeEditEvent(agentsPath), "hook", "gate"); err == nil {
			applied = true
			if err := os.WriteFile(agentsPath, []byte(after), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(agentsPath)
		if err != nil {
			t.Fatal(err)
		}
		return applied, startBinding, load(), string(got)
	}

	applied, start, next, onDisk := run(t, "")
	if applied {
		t.Fatal("the edit that rewrites the reviewer binding must be denied at the hook")
	}
	if onDisk != before {
		t.Errorf("the denied edit changed the file:\n%s", onDisk)
	}
	if !reflect.DeepEqual(start, next) {
		t.Errorf("the story's next gate must dispatch on the binding in force at start: start %+v, next %+v", start, next)
	}

	applied, start, next, _ = run(t, "lock_substrate_paths = []\n")
	if !applied || reflect.DeepEqual(start, next) {
		t.Errorf("control: with the lock opted out the same edit lands and the judge changes (applied=%v, start %+v, next %+v)", applied, start, next)
	}
}

// holdersRepo is attributionRepo with the lock config written, plus two stories
// each holding a settled live seat for a session/worktree that is NOT this
// hook's ("s1", /w/none): the session matches neither seat.
func holdersRepo(t *testing.T, catA, catB string) (repo string, a, b workitem.Item) {
	t.Helper()
	db, create := attributionRepo(t)
	stubWorktree(t, "/w/none")
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(lockExemptToml), 0o644); err != nil {
		t.Fatal(err)
	}
	a, b = create("first", "in_progress"), create("second", "in_progress")
	ctx := context.Background()
	for _, u := range []struct {
		id, cat string
	}{{a.ID, catA}, {b.ID, catB}} {
		cat := u.cat
		if _, err := db.Stories.Update(ctx, u.id, workitem.UpdateInput{Category: &cat}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	// A shared SeatKey admits co-holders (epic mode); each needs its own worktree.
	for _, s := range []struct {
		it            workitem.Item
		tree, session string
	}{{a, "/w/a", "sess-a"}, {b, "/w/b", "sess-b"}} {
		if _, _, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
			ItemID: s.it.ID, Kind: "story", Owner: "alice", State: "in_progress", StorySeat: true,
			SeatKey: "epic-parent", Worktree: s.tree, SessionID: s.session,
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.Leases.Confirm(ctx, s.it.ID, "in_progress"); err != nil {
			t.Fatal(err)
		}
	}
	if live, _ := db.Leases.List(ctx); len(live) != 2 {
		t.Fatalf("fixture must hold two live seats, got %d", len(live))
	}
	return repo, a, b
}

// AC3, "never of the session's identity": a session that matches no seat is not
// thereby free of the lock. With several live holders the deny names all of
// them rather than picking one.
func TestSubstrateLockHeldAgainstSessionThatMatchesNoSeat(t *testing.T) {
	repo, a, b := holdersRepo(t, "feature", "feature")
	out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate")
	if err == nil {
		t.Fatalf("a session bound to no seat must not slip the lock:\n%s", out)
	}
	reason := denyReasonOf(t, out)
	for _, want := range []string{a.ID, b.ID, "hold performing seats", "substrate lock", "finish or park them"} {
		if !strings.Contains(reason, want) {
			t.Errorf("deny missing %q: %s", want, reason)
		}
	}
	// The same holds for a STAMPED session that matches neither seat.
	stranger, _ := json.Marshal(map[string]any{
		"session_id": "stranger", "transcript_path": "/x/transcript.jsonl", "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": filepath.Join(repo, ".satelle", "skills", "x.md"), "old_string": "a", "new_string": "b"},
	})
	if out, err := runRootIn(t, string(stranger), "hook", "gate"); err == nil {
		t.Fatalf("a stamped session matching no seat must not slip the lock:\n%s", out)
	}
	for _, id := range []string{a.ID, b.ID} {
		found := false
		for _, r := range ledgerRows(t, ledger.KindSubstrateLockDeny) {
			found = found || r.StoryID == id
		}
		if !found {
			t.Errorf("no %s row on %s", ledger.KindSubstrateLockDeny, id)
		}
	}
}

// The lane out is per holder: substrate change is allowed only when EVERY live
// holder is in the substrate lane; otherwise the deny names the ones that are not.
func TestSubstrateLockLaneOutIsPerHolder(t *testing.T) {
	t.Run("all holders in the lane", func(t *testing.T) {
		repo, _, _ := holdersRepo(t, "substrate", "substrate")
		if out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate"); err != nil {
			t.Fatalf("every holder in the substrate lane must allow the change: %v\n%s", err, out)
		}
	})
	t.Run("one holder outside the lane", func(t *testing.T) {
		repo, a, b := holdersRepo(t, "substrate", "feature")
		out, err := runRootIn(t, claudeEditEvent(filepath.Join(repo, ".satelle", "skills", "x.md")), "hook", "gate")
		if err == nil {
			t.Fatalf("a product-story holder must keep the lock on:\n%s", out)
		}
		reason := denyReasonOf(t, out)
		if !strings.Contains(reason, b.ID) || strings.Contains(reason, a.ID) {
			t.Errorf("the deny must name the holder outside the lane (%s) and not the substrate one (%s): %s", b.ID, a.ID, reason)
		}
	})
}
