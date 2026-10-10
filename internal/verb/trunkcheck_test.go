package verb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// trunkWF is a one-step performing workflow: raised (backlog) → coded
// (in_progress, engaging) → closed.
var trunkWF = routeHalves(
	`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
`,
	`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)

// headGater records the HEAD of dir at every gated edge, which is how a test
// sees whether a fast-forward landed before the reviewers ran.
type headGater struct {
	t     *testing.T
	repos testutil.TrunkRepos
	dir   string
	seen  []string
}

func (g *headGater) Gate(context.Context, workitem.Item, string) (verb.GateDecision, error) {
	g.seen = append(g.seen, g.repos.Head(g.t, g.dir))
	return verb.GateDecision{}, nil
}

type trunkEnv struct {
	db    *store.DB
	repos testutil.TrunkRepos
	out   *bytes.Buffer
	gater *headGater
}

// wireTrunk wires the verbs over a store with the performing workflow, the
// trunk fixture, and the trunk check pointed at dir (the subject clone when
// dir is empty). unwired leaves the trunk seam unset.
func wireTrunk(t *testing.T, cfg config.TrunkConfig, dir string, unwired bool) trunkEnv {
	t.Helper()
	db := wireWithWorkflowsStore(t, trunkWF)
	repos := testutil.NewTrunkRepos(t)
	if dir == "" {
		dir = repos.Subject
	}
	out := new(bytes.Buffer)
	verb.SetTrunkOutput(out)
	if !unwired {
		verb.SetTrunkConfig(cfg, dir)
	}
	g := &headGater{t: t, repos: repos, dir: dir}
	verb.SetTransitionGater(g)
	return trunkEnv{db: db, repos: repos, out: out, gater: g}
}

func (e trunkEnv) create(t *testing.T, status string) (workitem.Item, error) {
	t.Helper()
	req := map[string]any{
		"title": "trunk " + status, "body": "goal", "acceptance_criteria": "1. checked",
		"category": "feature", "tags": []string{"workflow:eng"},
	}
	if status != "" {
		req["status"] = status
	}
	raw, err := dispatchRaw(t, "story-create", req)
	var it workitem.Item
	if err == nil {
		if jerr := json.Unmarshal(raw, &it); jerr != nil {
			t.Fatal(jerr)
		}
	}
	return it, err
}

func (e trunkEnv) engage(t *testing.T, id string) error {
	t.Helper()
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": id, "status": "in_progress"})
	return err
}

func (e trunkEnv) rows(t *testing.T, id, kind string) []ledger.Entry {
	t.Helper()
	es, err := e.db.Ledger.ListByStory(context.Background(), id, kind)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func (e trunkEnv) status(t *testing.T, id string) string {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-get", map[string]any{"id": id}), &it); err != nil {
		t.Fatal(err)
	}
	return it.Status
}

func (e trunkEnv) baselineHead(t *testing.T, id string) string {
	t.Helper()
	rows := e.rows(t, id, ledger.KindEngagementBaseline)
	if len(rows) != 1 {
		t.Fatalf("want 1 engagement_baseline, got %d", len(rows))
	}
	var p struct {
		HeadSHA string `json:"head_sha"`
	}
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.HeadSHA
}

func (e trunkEnv) dirty(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.repos.Subject, "seed.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e trunkEnv) line(t *testing.T) string {
	t.Helper()
	s := strings.TrimSpace(e.out.String())
	if strings.Count(s, "\n") != 0 {
		t.Fatalf("want one trunk line, got %q", s)
	}
	return s
}

func TestTrunkTransitionEngageFastForwardsBeforeGatesAndBaseline(t *testing.T) {
	e := wireTrunk(t, config.TrunkConfig{}, "", false)
	e.repos.PublishFromPusher(t, "a.txt")
	tip := e.repos.Git(t, e.repos.Remote, "rev-parse", "refs/heads/main")
	old := e.repos.Head(t, e.repos.Subject)

	it, err := e.create(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.engage(t, it.ID); err != nil {
		t.Fatal(err)
	}

	want := "satelle: trunk fast-forwarded main by 1 commit(s) " + old[:8] + ".." + tip[:8]
	if got := e.line(t); got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
	if got := e.baselineHead(t, it.ID); got != tip {
		t.Fatalf("baseline head_sha = %s, want the remote tip %s", got, tip)
	}
	if len(e.gater.seen) == 0 {
		t.Fatal("the gate stub never ran")
	}
	for _, h := range e.gater.seen {
		if h != tip {
			t.Fatalf("a gate saw HEAD %s, want the fast-forwarded tip %s", h, tip)
		}
	}
	rows := e.rows(t, it.ID, ledger.KindTrunkCheck)
	if len(rows) != 1 || rows[0].Body != want {
		t.Fatalf("trunk_check rows = %+v, want one with body %q", rows, want)
	}
	if e.status(t, it.ID) != "in_progress" {
		t.Fatalf("status = %s", e.status(t, it.ID))
	}
}

func TestTrunkCreateIntoEngagingFastForwardsBeforeBaseline(t *testing.T) {
	e := wireTrunk(t, config.TrunkConfig{}, "", false)
	e.repos.PublishFromPusher(t, "a.txt")
	tip := e.repos.Git(t, e.repos.Remote, "rev-parse", "refs/heads/main")

	it, err := e.create(t, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.line(t), "fast-forwarded main by 1 commit(s) ") {
		t.Fatalf("line = %q", e.out.String())
	}
	if got := e.baselineHead(t, it.ID); got != tip {
		t.Fatalf("baseline head_sha = %s, want the remote tip %s", got, tip)
	}
	if rows := e.rows(t, it.ID, ledger.KindTrunkCheck); len(rows) != 1 || rows[0].Body != e.line(t) {
		t.Fatalf("trunk_check rows = %+v", rows)
	}
}

func TestTrunkUnsetSeamChangesNothing(t *testing.T) {
	e := wireTrunk(t, config.TrunkConfig{}, "", true)
	e.repos.PublishFromPusher(t, "a.txt")
	before := e.repos.Head(t, e.repos.Subject)

	it, err := e.create(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.engage(t, it.ID); err != nil {
		t.Fatal(err)
	}
	if e.out.Len() != 0 {
		t.Fatalf("printed %q with the seam unset", e.out.String())
	}
	if rows := e.rows(t, it.ID, ledger.KindTrunkCheck); len(rows) != 0 {
		t.Fatalf("trunk_check rows with the seam unset: %+v", rows)
	}
	if after := e.repos.Head(t, e.repos.Subject); after != before {
		t.Fatalf("the subject moved from %s to %s with the seam unset", before, after)
	}
	if len(e.rows(t, it.ID, ledger.KindEngagementBaseline)) != 1 || e.status(t, it.ID) != "in_progress" {
		t.Fatal("the engage did not proceed as before")
	}
}

func TestTrunkLevelEngagesSilently(t *testing.T) {
	e := wireTrunk(t, config.TrunkConfig{}, "", false)
	head := e.repos.Head(t, e.repos.Subject)
	it, _ := e.create(t, "")
	if err := e.engage(t, it.ID); err != nil {
		t.Fatal(err)
	}
	if e.out.Len() != 0 || len(e.rows(t, it.ID, ledger.KindTrunkCheck)) != 0 {
		t.Fatalf("level trunk printed %q / wrote rows", e.out.String())
	}
	if e.baselineHead(t, it.ID) != head || e.repos.Head(t, e.repos.Subject) != head {
		t.Fatal("level engage moved the tree")
	}
}

// Each state, on a transition: the line, the ledger row, and whether the engage
// goes through.
func TestTrunkTransitionStates(t *testing.T) {
	cases := []struct {
		name    string
		cfg     config.TrunkConfig
		setup   func(t *testing.T, e trunkEnv)
		line    string
		refused bool
	}{
		{
			name: "ahead proceeds with a notice",
			setup: func(t *testing.T, e trunkEnv) {
				e.repos.Commit(t, e.repos.Subject, "mine.txt", "mine\n")
			},
			line: "satelle: trunk 1 unpushed commit(s)",
		},
		{
			name: "diverged refuses by default",
			setup: func(t *testing.T, e trunkEnv) {
				e.repos.Commit(t, e.repos.Subject, "mine.txt", "mine\n")
				e.repos.PublishFromPusher(t, "a.txt")
			},
			line:    "satelle: trunk diverged: 1 ahead, 1 behind",
			refused: true,
		},
		{
			name:    "dirty refuses by default",
			setup:   func(t *testing.T, e trunkEnv) { e.dirty(t) },
			line:    "satelle: trunk dirty tree on main",
			refused: true,
		},
		{
			name:  "dirty proceeds when the repo refuses nothing",
			cfg:   config.TrunkConfig{Refuse: &[]string{}},
			setup: func(t *testing.T, e trunkEnv) { e.dirty(t) },
			line:  "satelle: trunk dirty tree on main",
		},
		{
			name: "offline proceeds with a warning",
			setup: func(t *testing.T, e trunkEnv) {
				e.repos.Git(t, e.repos.Subject, "remote", "set-url", "origin", filepath.Join(filepath.Dir(e.repos.Remote), "gone.git"))
			},
			line: "satelle: trunk fetch from origin failed (proceeding): ",
		},
		{
			name: "offline refuses when the repo says so",
			cfg:  config.TrunkConfig{Refuse: &[]string{"offline"}},
			setup: func(t *testing.T, e trunkEnv) {
				e.repos.Git(t, e.repos.Subject, "remote", "set-url", "origin", filepath.Join(filepath.Dir(e.repos.Remote), "gone.git"))
			},
			line:    "satelle: trunk fetch from origin failed (proceeding): ",
			refused: true,
		},
		{
			name:  "check = false is silent",
			cfg:   config.TrunkConfig{Check: new(bool)},
			setup: func(t *testing.T, e trunkEnv) { e.dirty(t) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := wireTrunk(t, tc.cfg, "", false)
			it, _ := e.create(t, "")
			tc.setup(t, e)

			err := e.engage(t, it.ID)

			got := strings.TrimSpace(e.out.String())
			if tc.line == "" {
				if got != "" || len(e.rows(t, it.ID, ledger.KindTrunkCheck)) != 0 {
					t.Fatalf("a disabled check printed %q", got)
				}
			} else if !strings.HasPrefix(got, tc.line) || strings.Contains(got, "\n") {
				t.Fatalf("line = %q, want prefix %q", got, tc.line)
			}
			if tc.line != "" {
				rows := e.rows(t, it.ID, ledger.KindTrunkCheck)
				if len(rows) != 1 || rows[0].Body != got {
					t.Fatalf("trunk_check rows = %+v, want one with body %q", rows, got)
				}
			}
			if !tc.refused {
				if err != nil || e.status(t, it.ID) != "in_progress" {
					t.Fatalf("engage err = %v, status %s; want it to proceed", err, e.status(t, it.ID))
				}
				return
			}
			if err == nil {
				t.Fatal("engage was not refused")
			}
			if !strings.Contains(err.Error(), strings.TrimPrefix(strings.TrimSuffix(tc.line, ": "), "satelle: trunk ")) {
				t.Fatalf("error %q does not name what was found", err)
			}
			if !strings.Contains(err.Error(), "git pull --rebase") && !strings.Contains(err.Error(), "git stash") &&
				!strings.Contains(err.Error(), "restore access") {
				t.Fatalf("error %q names no reconcile step", err)
			}
			if st := e.status(t, it.ID); st != "backlog" {
				t.Fatalf("a refused engage left the story at %s, want backlog", st)
			}
			if len(e.gater.seen) != 0 {
				t.Fatal("a refused engage still dispatched a gate")
			}
			if active, _ := e.db.Leases.AnyActive(context.Background()); active {
				t.Fatal("a refused engage left the seat held")
			}
			if len(e.rows(t, it.ID, ledger.KindEngagementBaseline)) != 0 {
				t.Fatal("a refused engage recorded a baseline")
			}
		})
	}
}

// A refusal is cleared by reconciling: the same story then engages.
func TestTrunkRefusedEngageSucceedsOnceReconciled(t *testing.T) {
	e := wireTrunk(t, config.TrunkConfig{}, "", false)
	it, _ := e.create(t, "")
	e.dirty(t)
	if err := e.engage(t, it.ID); err == nil {
		t.Fatal("dirty engage was not refused")
	}
	e.repos.Git(t, e.repos.Subject, "checkout", "--", "seed.txt")
	e.out.Reset()
	if err := e.engage(t, it.ID); err != nil {
		t.Fatalf("engage after reconciling: %v", err)
	}
	if e.out.Len() != 0 {
		t.Fatalf("printed %q once level", e.out.String())
	}
}

// From a linked worktree on another branch the check reports and does not move
// trunk; with behind in the refuse set that refuses the engage.
func TestTrunkBehindFromLinkedWorktreeIsReportedNotMoved(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		cfg := config.TrunkConfig{}
		if refuse {
			cfg.Refuse = &[]string{"behind"}
		}
		name := "proceeds"
		if refuse {
			name = "refuses"
		}
		t.Run(name, func(t *testing.T) {
			e := wireTrunk(t, cfg, "", false)
			wt := filepath.Join(filepath.Dir(e.repos.Subject), "linked")
			e.repos.Git(t, e.repos.Subject, "worktree", "add", "--quiet", "-b", "feature", wt)
			verb.SetTrunkConfig(cfg, wt)
			e.gater.dir = wt
			e.repos.PublishFromPusher(t, "a.txt")
			before := e.repos.Git(t, e.repos.Subject, "rev-parse", "refs/heads/main")

			it, _ := e.create(t, "")
			err := e.engage(t, it.ID)

			if got := e.line(t); !strings.HasPrefix(got, "satelle: trunk behind origin/main by 1, not moved: ") {
				t.Fatalf("line = %q", got)
			}
			if after := e.repos.Git(t, e.repos.Subject, "rev-parse", "refs/heads/main"); after != before {
				t.Fatalf("trunk moved from a linked worktree: %s -> %s", before, after)
			}
			if refuse != (err != nil) {
				t.Fatalf("engage err = %v, refuse = %v", err, refuse)
			}
		})
	}
}

func TestTrunkCreateRefusalLeavesNothingBehind(t *testing.T) {
	e := wireTrunk(t, config.TrunkConfig{}, "", false)
	e.dirty(t)
	ledgerBefore, err := e.db.Ledger.Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	_, err = e.create(t, "in_progress")

	if err == nil || !strings.Contains(err.Error(), "create into in_progress refused") || !strings.Contains(err.Error(), "git stash") {
		t.Fatalf("create err = %v, want a refusal naming the reconcile step", err)
	}
	if got := e.line(t); got != "satelle: trunk dirty tree on main" {
		t.Fatalf("line = %q", got)
	}
	var list []workitem.Item
	if err := json.Unmarshal(call(t, "story-list", nil), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("a refused create inserted %d story row(s)", len(list))
	}
	if active, _ := e.db.Leases.AnyActive(context.Background()); active {
		t.Fatal("a refused create took a lease")
	}
	if n, _ := e.db.Ledger.Count(context.Background()); n != ledgerBefore {
		t.Fatalf("a refused create wrote %d ledger row(s)", n-ledgerBefore)
	}
}

// A create that proceeds past a non-refused state prints the line and ledgers it
// on the new story.
func TestTrunkCreateProceedsWithNotice(t *testing.T) {
	e := wireTrunk(t, config.TrunkConfig{}, "", false)
	e.repos.Commit(t, e.repos.Subject, "mine.txt", "mine\n")

	it, err := e.create(t, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.line(t); got != "satelle: trunk 1 unpushed commit(s)" {
		t.Fatalf("line = %q", got)
	}
	if rows := e.rows(t, it.ID, ledger.KindTrunkCheck); len(rows) != 1 || rows[0].Body != e.line(t) {
		t.Fatalf("trunk_check rows = %+v", rows)
	}
}

// A park and resume is a re-entry, not a first entry: the baseline exists, so
// the trunk is not checked again.
func TestTrunkCheckRunsOnFirstEntryOnly(t *testing.T) {
	e := wireTrunk(t, config.TrunkConfig{}, "", false)
	it, _ := e.create(t, "")
	if err := e.engage(t, it.ID); err != nil {
		t.Fatal(err)
	}
	call(t, "story-set", map[string]any{"id": it.ID, "status": "blocked"})
	e.repos.PublishFromPusher(t, "a.txt")
	e.out.Reset()
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	if e.out.Len() != 0 {
		t.Fatalf("a resume printed %q", e.out.String())
	}
}
