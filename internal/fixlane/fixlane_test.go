package fixlane

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

const repoRoot = "/repo"

// laneCfg declares a product surface and nothing else, so every other bound
// comes from the embedded default — the shape a real repo has.
func laneCfg(surface ...string) config.Config {
	return config.Config{FixLane: config.FixLaneConfig{ProductSurface: surface}}
}

func openStore(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func goodInput(path string) Input {
	return Input{StoryID: "sty_x", Status: "plan", Path: path, Reason: "typo in a comment", BoundLines: 3, ProvingTest: "TestReadme"}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		glob, rel string
		want      bool
	}{
		{"internal/**", "internal/cli/x.go", true},
		{"internal/**", "internal", false},
		{"internal/**", "cmd/x.go", false},
		{"**/x.go", "a/b/x.go", true},
		{"**/x.go", "x.go", true},
		{"go.mod", "go.mod", true},
		{"go.mod", "sub/go.mod", true}, // no "/": basename at any depth
		{"cmd/*/main.go", "cmd/satelle/main.go", true},
		{"cmd/*/main.go", "cmd/a/b/main.go", false},
		{"", "anything", false},
		{"[", "x", false}, // malformed never matches
	}
	for _, tc := range cases {
		if got := Match(tc.glob, tc.rel); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.glob, tc.rel, got, tc.want)
		}
	}
}

// AC2 + AC6: one case per refused class; the refusal names the class AND is a
// ledger row carrying it.
func TestRecordRefusesEachClassAndLogsIt(t *testing.T) {
	cfg := laneCfg("internal/**", "cmd/**")
	cfg.FixLane.GateSkills = []string{"internal/config/substrate/skills/**"}
	cases := []struct {
		name  string
		mut   func(*Input)
		path  string
		class string
	}{
		{"product surface", nil, "internal/cli/x.go", ClassProductSurface},
		{"test file is not special-cased", nil, "internal/cli/x_test.go", ClassProductSurface},
		{"gate skill", nil, ".satelle/skills/satelle-code-ac-review.md", ClassGateSkill},
		{"embedded gate skill", nil, "internal/config/substrate/skills/x.md", ClassGateSkill},
		{"reviewer rubric: the agents layer", nil, ".satelle/workflows/agents.toml", ClassReviewerRubric},
		{"reviewer rubric: workspace layer", nil, ".satelle/workflows/agents.workspace.toml", ClassReviewerRubric},
		{"reviewer rubric: legacy agents file", nil, ".satelle/agents.toml", ClassReviewerRubric},
		{"repo config carries the bound itself", nil, ".satelle/satelle.toml", ClassRepoConfig},
		{"local overlay carries it too", nil, ".satelle/satelle.local.toml", ClassRepoConfig},
		{"workflow", nil, ".satelle/workflows/step.toml", ClassWorkflow},
		{"principle", nil, ".satelle/principles/satelle-yagni.md", ClassPrinciple},
		{"constitution", nil, ".satelle/constitution.md", ClassPrinciple},
		{"no proving test", func(in *Input) { in.ProvingTest = "  " }, "README.md", ClassNoProvingTest},
		{"no reason", func(in *Input) { in.Reason = "" }, "README.md", ClassNoReason},
		{"no size bound", func(in *Input) { in.BoundLines = 0 }, "README.md", ClassNoBound},
		{"over the ceiling", func(in *Input) { in.BoundLines = 10_000 }, "README.md", ClassOverBound},
		{"no engaged story", func(in *Input) { in.StoryID = "" }, "README.md", ClassNoStory},
		{"outside the repo", nil, "/etc/passwd", ClassOutsideRepo},
		{"climbs out of the repo", nil, "../elsewhere.md", ClassOutsideRepo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openStore(t, filepath.Join(t.TempDir(), "s.db"))
			in := goodInput(tc.path)
			if tc.mut != nil {
				tc.mut(&in)
			}
			_, err := Record(context.Background(), db.Ledger, cfg, repoRoot, in, time.Now())
			var ref *Refusal
			if !errors.As(err, &ref) || ref.Class != tc.class {
				t.Fatalf("Record = %v, want a refusal of class %q", err, tc.class)
			}
			if got := err.Error(); !contains(got, tc.class) {
				t.Errorf("refusal %q does not name class %q", got, tc.class)
			}
			rows, err := db.Ledger.List(context.Background(), ledger.ListFilter{Kind: ledger.KindFixClaim})
			if err != nil || len(rows) != 1 {
				t.Fatalf("want exactly one fix_claim row for the refusal, got %d (%v)", len(rows), err)
			}
			var p ClaimPayload
			if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
				t.Fatal(err)
			}
			if p.Decision != DecisionRefused || p.RefusedClass != tc.class {
				t.Errorf("refusal row = %+v, want decision refused, class %q", p, tc.class)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// AC3: a repo that sets no bound falls to a documented default, never to
// "anything goes" — and a repo that declares no product surface has no lane.
func TestBoundDefaultsAreNotAnythingGoes(t *testing.T) {
	def := config.EmbeddedFixLane()
	if err := config.EmbeddedFixLaneErr(); err != nil {
		t.Fatal(err)
	}
	if def.MaxLines <= 0 {
		t.Fatalf("embedded default max_lines = %d, want a positive documented ceiling", def.MaxLines)
	}
	cfg := laneCfg("cmd/**") // sets NO max_lines
	if got := cfg.ResolveFixLane(repoRoot).MaxLines; got != def.MaxLines {
		t.Fatalf("unset max_lines resolved to %d, want the embedded default %d", got, def.MaxLines)
	}
	db := openStore(t, filepath.Join(t.TempDir(), "s.db"))
	ctx := context.Background()
	in := goodInput("README.md")
	in.BoundLines = def.MaxLines
	if _, err := Record(ctx, db.Ledger, cfg, repoRoot, in, time.Now()); err != nil {
		t.Fatalf("a claim at the default ceiling should be granted: %v", err)
	}
	in.BoundLines = def.MaxLines + 1
	if _, err := Record(ctx, db.Ledger, cfg, repoRoot, in, time.Now()); err == nil {
		t.Fatal("a claim over the default ceiling must be refused, not waved through")
	}
	// A repo may tighten or raise the ceiling in its own configuration.
	cfg.FixLane.MaxLines = 2
	in.BoundLines = 3
	if _, err := Record(ctx, db.Ledger, cfg, repoRoot, in, time.Now()); err == nil {
		t.Fatal("a repo's own max_lines must bind")
	}
	// No product surface declared: the lane is closed, not open.
	_, err := Record(ctx, db.Ledger, config.Config{}, repoRoot, goodInput("README.md"), time.Now())
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Class != ClassUndeclaredBound {
		t.Fatalf("no [fix_lane] product_surface: got %v, want class %s", err, ClassUndeclaredBound)
	}
	// A repo cannot subtract from the embedded refusal classes.
	cfg = laneCfg("cmd/**")
	cfg.FixLane.GateSkills = []string{"other/**"}
	if _, err := Record(ctx, db.Ledger, cfg, repoRoot, goodInput(".satelle/skills/x.md"), time.Now()); err == nil {
		t.Fatal("the embedded gate-skill class must survive a repo's own list")
	}
}

// The protected classes follow where the repo REALLY keeps its substrate. A repo
// that relocates data_dir or a substrate root keeps every refusal, and the old
// ".satelle/" spelling is not what protects it. No class is dead: each matches a
// path satelle itself creates.
func TestSubstrateClassesFollowTheRepoLayout(t *testing.T) {
	cfg := laneCfg("app/**")
	cfg.DataDir = "process"
	cfg.SubstrateRoots = map[string]string{"skills": "meta", "principles": "/somewhere/else"}
	cases := []struct{ path, class string }{
		{"process/workflows/step.toml", ClassWorkflow},
		{"process/workflows/agents.toml", ClassReviewerRubric},
		{"process/constitution.md", ClassPrinciple},
		{"process/satelle.toml", ClassRepoConfig},
		{"process/satelle.local.toml", ClassRepoConfig},
		{"meta/skills/satelle-code-ac-review.md", ClassGateSkill}, // [substrate_roots] skills = "meta" → meta/skills
	}
	for _, tc := range cases {
		db := openStore(t, filepath.Join(t.TempDir(), "s.db"))
		_, err := Record(context.Background(), db.Ledger, cfg, repoRoot, goodInput(tc.path), time.Now())
		var ref *Refusal
		if !errors.As(err, &ref) || ref.Class != tc.class {
			t.Errorf("%s: got %v, want class %s", tc.path, err, tc.class)
		}
	}
	// The default spelling no longer names substrate in this layout: it is an
	// ordinary path, so the claim is judged on its own fields.
	db := openStore(t, filepath.Join(t.TempDir(), "s.db"))
	if _, err := Record(context.Background(), db.Ledger, cfg, repoRoot, goodInput(".satelle/skills/x.md"), time.Now()); err != nil {
		t.Errorf(".satelle/skills is not this repo's skills root and must not be a refused class: %v", err)
	}
	// An absolute root outside the repo derives no in-repo glob (such a path is
	// refused as outside-repo before any class is consulted).
	if _, err := Record(context.Background(), db.Ledger, cfg, repoRoot, goodInput("/somewhere/else/principles/p.md"), time.Now()); err == nil {
		t.Error("a path outside the repo must be refused")
	}
	// A directory whose name carries glob metacharacters matches itself only.
	odd := laneCfg("app/**")
	odd.DataDir = "we[ird]*"
	if _, err := Record(context.Background(), db.Ledger, odd, repoRoot, goodInput("we[ird]*/principles/p.md"), time.Now()); err == nil {
		t.Error("a data dir with glob metacharacters must still protect its own principles")
	}
	if _, err := Record(context.Background(), db.Ledger, odd, repoRoot, goodInput("weirdx/principles/p.md"), time.Now()); err != nil {
		t.Errorf("an escaped glob must not match a different directory: %v", err)
	}
}

// AC3: no bound literal in Go. Every integer literal in the lane's non-test
// sources is 0, 1 or 2 (indexing, halving, tabwriter padding) — the ceiling and
// the path classes live in configuration.
func TestNoBoundLiteralInGo(t *testing.T) {
	// A bound spelled inside a string (a seeded config comment, a help line) is
	// still a bound literal in Go: `max_lines = <digits>` must be rendered from
	// the embedded default, never typed.
	spelled := regexp.MustCompile(`(?i)max_lines\s*=\s*\d`)
	for _, file := range []string{"fixlane.go", "report.go", "../config/fixlane.go", "../cli/cmd_fix.go", "../cli/fixlane_hook.go", "../cli/fixlane_seed.go"} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok {
				return true
			}
			switch lit.Kind {
			case token.INT:
				if strings.HasPrefix(lit.Value, "0o") { // an os.FileMode, not a bound
					return true
				}
				if v, err := strconv.Atoi(lit.Value); err != nil || v > 2 {
					t.Errorf("%s: integer literal %s at %s — a bound belongs in [fix_lane], not in Go", file, lit.Value, fset.Position(lit.Pos()))
				}
			case token.STRING:
				if spelled.MatchString(lit.Value) {
					t.Errorf("%s: string literal at %s spells a max_lines value — render it from EmbeddedFixLane()", file, fset.Position(lit.Pos()))
				}
			}
			return true
		})
		for _, cg := range f.Comments {
			if spelled.MatchString(cg.Text()) {
				t.Errorf("%s: comment at %s spells a max_lines value", file, fset.Position(cg.Pos()))
			}
		}
	}
}

// AC4, structurally: the lane cannot change what a gate judges because it
// cannot reach it. Neither the lane package nor its edit-gate hook imports
// anything that loads a skill, a route or a reviewer set.
func TestLaneDoesNotImportWhatAGateJudgesBy(t *testing.T) {
	forbidden := []string{"/agentstep", "/verb", "/wfdot", "/wfroute", "/wfgovern", "/docindex", "/agentcli"}
	for _, file := range []string{"fixlane.go", "report.go", "../cli/fixlane_hook.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			for _, bad := range forbidden {
				if strings.HasSuffix(p, bad) {
					t.Errorf("%s imports %s — the lane must not be able to reach what a gate judges", file, p)
				}
			}
		}
	}
}

// AC5: the claim row is written before any edit can be licensed, and survives
// the process — a fresh open of the same database reads it back, and the use
// row that a consuming edit writes sorts after it.
func TestClaimRowPrecedesUseAndSurvivesProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	ctx := context.Background()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	claimAt := time.Now().Add(-time.Minute)
	c, err := Record(ctx, db.Ledger, laneCfg("internal/**"), repoRoot, goodInput("README.md"), claimAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := Consume(ctx, db.Ledger, c, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db2 := openStore(t, path) // a new handle: what the next process sees
	rows, err := db2.Ledger.ListByStory(ctx, "sty_x", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Kind != ledger.KindFixClaim || rows[1].Kind != ledger.KindFixClaimUse {
		t.Fatalf("timeline = %v, want [fix_claim fix_claim_used]", kinds(rows))
	}
	if !rows[0].CreatedAt.Before(rows[1].CreatedAt) {
		t.Errorf("claim row (%v) does not precede the use row (%v)", rows[0].CreatedAt, rows[1].CreatedAt)
	}
	var p ClaimPayload
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Path != "README.md" || p.Reason == "" || p.BoundLines != 3 || p.ProvingTest != "TestReadme" {
		t.Errorf("claim row lacks path/reason/bound/proving test: %+v", p)
	}
}

func kinds(rows []ledger.Entry) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Kind)
	}
	return out
}

// Expiry: a claim is consumed by ONE edit and dead at the story's next
// transition. A standing licence is what the lane exists to prevent.
func TestClaimIsNotAStandingLicence(t *testing.T) {
	ctx := context.Background()
	db := openStore(t, filepath.Join(t.TempDir(), "s.db"))
	cfg := laneCfg("internal/**")
	t0 := time.Now().Add(-time.Hour)

	c, err := Record(ctx, db.Ledger, cfg, repoRoot, goodInput("README.md"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Live(ctx, db.Ledger, "sty_x", "README.md"); !ok {
		t.Fatal("a fresh claim must be live")
	}
	if _, ok, _ := Live(ctx, db.Ledger, "sty_x", "CHANGELOG.md"); ok {
		t.Fatal("a claim on README.md must not license CHANGELOG.md")
	}
	if err := Consume(ctx, db.Ledger, c, 2, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Live(ctx, db.Ledger, "sty_x", "README.md"); ok {
		t.Fatal("a SECOND edit must be denied: the claim was consumed by the first")
	}

	// A second claim, unconsumed, dies at the story's next transition.
	if _, err := Record(ctx, db.Ledger, cfg, repoRoot, goodInput("README.md"), t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Live(ctx, db.Ledger, "sty_x", "README.md"); !ok {
		t.Fatal("the new claim must be live before any transition")
	}
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: "sty_x", Kind: ledger.KindStatusTransition, Body: "plan→in_progress"}, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Live(ctx, db.Ledger, "sty_x", "README.md"); ok {
		t.Fatal("a claim must be dead once the story transitions")
	}
	// Another story's claim is not this story's licence.
	other := goodInput("README.md")
	other.StoryID = "sty_other"
	if _, err := Record(ctx, db.Ledger, cfg, repoRoot, other, t0.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Live(ctx, db.Ledger, "sty_x", "README.md"); ok {
		t.Fatal("a claim on another story must not license this one")
	}
}

// One claim licenses ONE edit even when edits race. Every racer first sees the
// claim live (the check-then-act window a non-atomic Consume leaves open), then
// they all consume at once — through separate handles on one database file, as
// separate hook processes would. Exactly one wins; the rest get ErrConsumed and
// exactly one use row exists.
func TestConsumeIsAtomicUnderConcurrentEdits(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "s.db")
	const racers = 8
	handles := make([]*store.DB, racers)
	for i := range handles {
		handles[i] = openStore(t, path)
	}
	c, err := Record(ctx, handles[0].Ledger, laneCfg("internal/**"), repoRoot, goodInput("README.md"), time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for i, h := range handles { // everyone passes the liveness check first
		if _, ok, err := Live(ctx, h.Ledger, "sty_x", "README.md"); err != nil || !ok {
			t.Fatalf("racer %d: claim must look live before the race: ok=%v err=%v", i, ok, err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, racers)
	for _, h := range handles {
		go func(h *store.DB) {
			<-start
			results <- Consume(ctx, h.Ledger, c, 2, time.Now())
		}(h)
	}
	close(start)
	wins, lost := 0, 0
	for i := 0; i < racers; i++ {
		switch err := <-results; {
		case err == nil:
			wins++
		case errors.Is(err, ErrConsumed):
			lost++
		default:
			t.Errorf("unexpected consume error: %v", err)
		}
	}
	if wins != 1 || lost != racers-1 {
		t.Fatalf("consume winners = %d, losers = %d, want exactly 1 and %d", wins, lost, racers-1)
	}
	uses, err := handles[0].Ledger.List(ctx, ledger.ListFilter{Kind: ledger.KindFixClaimUse})
	if err != nil || len(uses) != 1 {
		t.Fatalf("want exactly one use row, got %d (%v)", len(uses), err)
	}
	if err := Consume(ctx, handles[0].Ledger, c, 2, time.Now()); !errors.Is(err, ErrConsumed) {
		t.Errorf("a later consume = %v, want ErrConsumed", err)
	}
}

// AC7: over any window the rows alone yield the claim count, the size
// distribution, the refused-claim count by class, and the per-gate reject count.
func TestFiguresFromSyntheticLedger(t *testing.T) {
	ctx := context.Background()
	db := openStore(t, filepath.Join(t.TempDir(), "s.db"))
	cfg := laneCfg("internal/**")
	day := func(d int) time.Time { return time.Date(2026, 9, d, 12, 0, 0, 0, time.UTC) }

	rec := func(d int, path string, lines int) Claim {
		in := goodInput(path)
		in.BoundLines = lines
		c, _ := Record(ctx, db.Ledger, cfg, repoRoot, in, day(d))
		return c
	}
	c1 := rec(1, "README.md", 3)
	rec(2, "CHANGELOG.md", 5)
	rec(3, "scripts/x.sh", 9)
	rec(4, "internal/cli/x.go", 3)    // refused: product-surface
	rec(5, "internal/cli/y.go", 3)    // refused: product-surface
	rec(6, ".satelle/skills/a.md", 3) // refused: gate-skill
	rec(20, "README.md", 7)           // outside the window below
	if err := Consume(ctx, db.Ledger, c1, 2, day(1).Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reject := func(d int, skill string) {
		p, _ := json.Marshal(map[string]any{"skill": skill, "accept": false})
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: "sty_x", Kind: ledger.KindReviewReject, Payload: p}, day(d)); err != nil {
			t.Fatal(err)
		}
	}
	reject(2, "satelle-code-ac-review")
	reject(3, "satelle-code-ac-review")
	reject(4, "satelle-story-scope-review")
	reject(25, "satelle-code-ac-review") // outside the window

	f, err := Report(ctx, db.Ledger, day(1), day(10))
	if err != nil {
		t.Fatal(err)
	}
	if f.Granted != 3 || f.Refused != 3 || f.Claims() != 6 {
		t.Errorf("claim count = granted %d refused %d total %d, want 3/3/6", f.Granted, f.Refused, f.Claims())
	}
	if f.RefusedByClass[ClassProductSurface] != 2 || f.RefusedByClass[ClassGateSkill] != 1 || len(f.RefusedByClass) != 2 {
		t.Errorf("refused by class = %v, want product-surface:2 gate-skill:1", f.RefusedByClass)
	}
	if b := f.Bound; b.Count != 3 || b.Min != 3 || b.Max != 9 || b.Median != 5 || b.Total != 17 || b.Counts[5] != 1 {
		t.Errorf("size distribution = %+v, want n=3 min=3 median=5 max=9 total=17", b)
	}
	if f.Consumed != 1 || f.Used.Count != 1 || f.Used.Max != 2 {
		t.Errorf("consumed = %d, used = %+v, want one edit of 2 lines", f.Consumed, f.Used)
	}
	if f.RejectsByGate["satelle-code-ac-review"] != 2 || f.RejectsByGate["satelle-story-scope-review"] != 1 || len(f.RejectsByGate) != 2 {
		t.Errorf("rejects by gate = %v, want code-ac:2 scope:1", f.RejectsByGate)
	}
	// An open window sees the rows the bounded one excluded.
	all, err := Report(ctx, db.Ledger, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if all.Granted != 4 || all.RejectsByGate["satelle-code-ac-review"] != 3 {
		t.Errorf("open window = granted %d rejects %v, want 4 and code-ac:3", all.Granted, all.RejectsByGate)
	}
}
