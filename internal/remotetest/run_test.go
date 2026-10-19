package remotetest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// shared records what the fake agents saw.
type shared struct {
	mu          sync.Mutex
	log         []string
	dirs        []string
	runnerCalls int
	sawInclude  bool
	reviewDocs  map[string]string
	reviewDiff  string
	decision    string
}

type performer struct{ s *shared }

func (p *performer) Name() string    { return "fake-performer" }
func (p *performer) Command() string { return "fake-agent -p {system}" }
func (p *performer) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	p.s.log = append(p.s.log, "perform")
	p.s.dirs = append(p.s.dirs, req.Dir)
	if _, err := os.Stat(filepath.Join(req.Dir, ".local", "wiring.json")); err == nil {
		p.s.sawInclude = true
	}
	if err := os.WriteFile(filepath.Join(req.Dir, "performed.txt"), []byte("hello\n"), 0o644); err != nil {
		return nil, err
	}
	return []byte(`{"artifact":{"body":"EVIDENCE-BODY"}}`), nil
}

type reviewer struct{ s *shared }

func (r *reviewer) Name() string    { return "fake-reviewer" }
func (r *reviewer) Command() string { return "fake-reviewer -p {system}" }
func (r *reviewer) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.log = append(r.s.log, "review")
	r.s.reviewDocs, r.s.reviewDiff = readPayload(req.Payload)
	if r.s.decision == "" {
		r.s.decision = `{"decision":"accept"}`
	}
	return []byte(r.s.decision), nil
}

// readPayload reads what a reviewer's work item names: each attached document and
// the diff, whether inline or as a file the payload points at.
func readPayload(payload string) (docs map[string]string, diff string) {
	docs = map[string]string{}
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &top) != nil {
		return docs, ""
	}
	var list []struct{ Name, Path, Body string }
	_ = json.Unmarshal(top["docs"], &list)
	for _, d := range list {
		body := d.Body
		if d.Path != "" {
			if b, err := os.ReadFile(d.Path); err == nil {
				body = string(b)
			}
		}
		docs[d.Name] = body
	}
	var d struct {
		PatchPath string `json:"patch_path"`
		Patch     string `json:"patch"`
	}
	if json.Unmarshal(top["diff"], &d) == nil {
		diff = d.Patch
		if d.PatchPath != "" {
			if b, err := os.ReadFile(d.PatchPath); err == nil {
				diff += string(b)
			}
		}
	}
	return docs, diff
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceIn(t *testing.T, root, rel, old, repl string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	writeFile(t, root, rel, strings.Replace(string(b), old, repl, 1))
}

// rig is a main repository, a valid remote definition and the fake agents.
type rig struct {
	t        *testing.T
	main     string
	remote   string
	sentinel string
	s        *shared
	rows     []ledger.AppendInput
	item     workitem.Item
	temp     string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	main, _ := filepath.EvalSymlinks(t.TempDir())
	gitIn(t, main, "init", "-q", "-b", "main")
	writeFile(t, main, ".gitignore", ".local/\n")
	writeFile(t, main, "README.md", "main tree\n")
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "init")
	writeFile(t, main, ".local/wiring.json", "{}")

	// The sentinel's directory is named plainly: t.TempDir embeds the subtest name,
	// which may itself contain "{command}".
	hostDir, err := os.MkdirTemp("", "host")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostDir) })
	r := &rig{t: t, main: main, s: &shared{}, temp: t.TempDir(), sentinel: filepath.Join(hostDir, "host-ran")}
	r.remote = remotedef.Dir(t.TempDir(), "edge")
	testutil.WriteRemoteFixture(t, r.remote, r.sentinel)
	if fs := remotedef.Validate(r.remote); len(fs) != 0 {
		t.Fatalf("the remote fixture must validate clean: %v", fs)
	}
	r.item = workitem.Item{ID: "sty_sample", Title: "sample", Category: "feature", Status: "backlog", AcceptanceCriteria: "1. it works"}
	return r
}

func (r *rig) opts() Options {
	return Options{
		Base: "main", MainRoot: r.main, TempDir: r.temp,
		NewEngine: func(docs agentstep.DocGetter, root string, agents config.AgentsConfig) (*agentstep.Engine, error) {
			e := agentstep.New(nil, docs, root, "")
			e.SetNamedAgents(agents.NamedBinding)
			e.SetReviewerBinding(agents.ReviewerBinding())
			e.SetWarnWriter(io.Discard)
			return e, nil
		},
		NewRunner: func(string, string) (agentcli.Runner, error) {
			r.s.mu.Lock()
			r.s.runnerCalls++
			r.s.mu.Unlock()
			return &performer{r.s}, nil
		},
		ReviewerRunner: &reviewer{r.s},
		Ledger: func(_ context.Context, in ledger.AppendInput) error {
			r.rows = append(r.rows, in)
			return nil
		},
	}
}

// noScratchLeft fails when a run left a worktree, a branch or a directory behind.
func (r *rig) noScratchLeft() {
	r.t.Helper()
	if wt := gitIn(r.t, r.main, "worktree", "list", "--porcelain"); strings.Count(wt, "worktree ") != 1 {
		r.t.Errorf("scratch worktree left behind:\n%s", wt)
	}
	if br := gitIn(r.t, r.main, "branch", "--list", "remote-test/*"); br != "" {
		r.t.Errorf("scratch branch left behind: %s", br)
	}
	if left, _ := os.ReadDir(r.temp); len(left) != 0 {
		r.t.Errorf("scratch directory left behind: %v", left)
	}
	for _, d := range r.s.dirs {
		if _, err := os.Stat(d); err == nil {
			r.t.Errorf("scratch tree %s still exists", d)
		}
	}
}

func (r *rig) nothingRan() {
	r.t.Helper()
	if len(r.s.log) != 0 {
		r.t.Errorf("an agent ran: %v", r.s.log)
	}
	if len(r.rows) != 0 {
		r.t.Errorf("ledger rows written: %v", r.rows)
	}
	r.noScratchLeft()
}

// AC1: every segment, in order; DispatchExecutor before Gate on each edge; one
// ledger row per edge; the host never touched; the scratch tree gone.
func TestRunWalksEverySegmentInOrder(t *testing.T) {
	r := newRig(t)
	rep, err := Run(context.Background(), "edge", r.remote, r.item, r.opts())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Join(r.s.log, ","); got != "perform,perform,review" {
		t.Errorf("agents ran %q, want the performer before the reviewer on each edge", got)
	}
	want := []struct {
		seg                        int
		from, to, verdict, binding string
	}{
		{0, "backlog", "plan", VerdictPerformed, "worker"},
		{0, "plan", "in_progress", VerdictAccept, "worker"},
		{1, "in_progress", "done", VerdictNoOp, ""},
	}
	if len(rep.Steps) != len(want) {
		t.Fatalf("steps = %+v", rep.Steps)
	}
	for i, w := range want {
		s := rep.Steps[i]
		if s.Segment != w.seg || s.From != w.from || s.To != w.to || s.Verdict != w.verdict || s.Binding != w.binding {
			t.Errorf("step %d = %+v, want %+v", i, s, w)
		}
		if s.Identity != "your-host-label" {
			t.Errorf("step %d identity = %q", i, s.Identity)
		}
	}
	if s := rep.Steps[0]; s.Skill != "plan-work" || !s.OnHost || !strings.Contains(s.HostCommand, "your-host-cli run your-host-label fake-agent") {
		t.Errorf("performer step = %+v", s)
	}
	if s := rep.Steps[1]; s.Skill != "remote-work" || s.Reviewer != "reviewer" {
		t.Errorf("performed-and-reviewed step = %+v", s)
	}
	if s := rep.Steps[2]; s.OnHost || s.HostCommand != "" || s.Skill != "" {
		t.Errorf("no-op step = %+v", s)
	}

	if len(r.rows) != 3 {
		t.Fatalf("want one remote_test row per edge, got %d", len(r.rows))
	}
	for i, row := range r.rows {
		var p map[string]string
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if row.Kind != ledger.KindRemoteTest || row.StoryID != "sty_sample" || p["remote"] != "edge" ||
			p["verdict"] != want[i].verdict || p["from"] != want[i].from || p["to"] != want[i].to {
			t.Errorf("row %d = %+v %v", i, row, p)
		}
	}

	if _, err := os.Stat(r.sentinel); !os.IsNotExist(err) {
		t.Errorf("a host command was executed (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(r.main, "performed.txt")); !os.IsNotExist(err) {
		t.Error("a performer wrote into the main tree")
	}
	if len(r.s.dirs) == 0 || !strings.Contains(r.s.dirs[0], "remote-test-edge-") {
		t.Errorf("performers did not run in the scratch tree: %v", r.s.dirs)
	}
	r.noScratchLeft()
	if rep.CloudRefused != 0 {
		t.Errorf("CloudRefused = %d", rep.CloudRefused)
	}
}

// AC1: a missing --base is refused, and nothing ran.
func TestRunRefusesMissingBase(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Base = "  "
	_, err := Run(context.Background(), "edge", r.remote, r.item, o)
	if err == nil || !strings.Contains(err.Error(), "never defaults to HEAD") {
		t.Fatalf("want the base refusal, got %v", err)
	}
	r.nothingRan()
	o.Base = "no-such-ref"
	if _, err := Run(context.Background(), "edge", r.remote, r.item, o); err == nil || !strings.Contains(err.Error(), "does not resolve") {
		t.Errorf("an unresolvable base must be refused, got %v", err)
	}
	r.nothingRan()
}

// AC1: [worktree] include is carried into the scratch tree before a performer
// runs there.
func TestRunCarriesInclude(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	if _, err := Run(context.Background(), "edge", r.remote, r.item, o); err != nil {
		t.Fatal(err)
	}
	if r.s.sawInclude {
		t.Fatal("the include must be absent when none is declared")
	}
	o.Include = []string{".local/wiring.json"}
	if _, err := Run(context.Background(), "edge", r.remote, r.item, o); err != nil {
		t.Fatal(err)
	}
	if !r.s.sawInclude {
		t.Error("the declared include was not carried into the scratch tree")
	}
	r.noScratchLeft()
}

// A reject stops the walk, is recorded, and still removes the scratch tree.
func TestRunStopsAtReject(t *testing.T) {
	r := newRig(t)
	r.s.decision = `{"decision":"reject","notes":"not good enough"}`
	rep, err := Run(context.Background(), "edge", r.remote, r.item, r.opts())
	var rej *RejectedError
	if !errors.As(err, &rej) || rej.From != "plan" || rej.To != "in_progress" || !strings.Contains(rej.Notes, "not good enough") {
		t.Fatalf("want a rejection at plan→in_progress, got %v", err)
	}
	if len(rep.Steps) != 2 || rep.Steps[1].Verdict != VerdictReject {
		t.Errorf("steps = %+v", rep.Steps)
	}
	if len(r.rows) != 2 {
		t.Errorf("want rows for the two edges that ran, got %d", len(r.rows))
	}
	r.noScratchLeft()
}

// AC2: a reviewer's payload carries what the performer attached and the scratch
// tree's diff; neither is written to a store.
func TestReviewerSeesArtifactAndDiff(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.StoryDocs = func(context.Context, string) []agentstep.DocState {
		return []agentstep.DocState{{Name: "plan", Type: "plan", Body: "THE-PLAN"}}
	}
	if _, err := Run(context.Background(), "edge", r.remote, r.item, o); err != nil {
		t.Fatal(err)
	}
	if got := r.s.reviewDocs["ac-evidence"]; got != "EVIDENCE-BODY" {
		t.Errorf("the performer's attached artifact did not reach the reviewer: %v", r.s.reviewDocs)
	}
	if got := r.s.reviewDocs["plan"]; got != "THE-PLAN" {
		t.Errorf("the story's own documents did not reach the reviewer: %v", r.s.reviewDocs)
	}
	if !strings.Contains(r.s.reviewDiff, "performed.txt") {
		t.Errorf("the scratch tree's diff did not reach the reviewer: %q", r.s.reviewDiff)
	}
}

// AC4: a definition that cannot run is refused with its reasons before any
// dispatch, scratch tree or ledger row.
func TestRunRefusesBeforeAnyDispatch(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, r *rig, o *Options)
		want   string
	}{
		{"unknown skill", func(t *testing.T, r *rig, _ *Options) {
			replaceIn(t, r.remote, "workflows/step.toml", `skills = ["plan-work"]`, `skills = ["ghost-skill"]`)
		}, "ghost-skill"},
		{"unresolvable binding", func(t *testing.T, r *rig, _ *Options) {
			replaceIn(t, r.remote, "workflows/step.toml", `agent = "worker"
skills = ["plan-work"]`, `agent = "nobody"
skills = ["plan-work"]`)
		}, "nobody"},
		{"host.run without {command}", func(t *testing.T, r *rig, _ *Options) {
			replaceIn(t, r.remote, "remote.toml", `"{command}"]`, `"x"]`)
		}, "{command}"},
		{"agent cli not on this machine", func(t *testing.T, r *rig, o *Options) {
			replaceIn(t, r.remote, "workflows/agents.toml", `command = "fake-agent -p {system} --tools {tools}"`,
				`command = "satelle-no-such-agent-xyz -p {system} --tools {tools}"`)
			o.NewRunner = nil // the real resolution
		}, "satelle-no-such-agent-xyz"},
		{"cloud performer", func(t *testing.T, r *rig, _ *Options) {
			replaceIn(t, r.remote, "workflows/agents.toml", `[worker]
role = "agent"
command = "fake-agent -p {system} --tools {tools}"`, `[worker]
role = "agent"
interface = "cloud"
command = "claude -p {system} --tools {tools}"`)
		}, "cloud binding"},
		{"remote_agent", func(t *testing.T, r *rig, _ *Options) {
			replaceIn(t, r.remote, "workflows/step.toml", `requires = ["planned"]`, `remote_agent = "sky"
local_tags = ["local"]
requires = ["planned"]`)
			replaceIn(t, r.remote, "workflows/agents.toml", `[reviewer]`, `[sky]
role = "agent"
interface = "cloud"
command = "claude-cloud"
tools = "Read"

[reviewer]`)
		}, "remote_agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			o := r.opts()
			tc.mutate(t, r, &o)
			rep, err := Run(context.Background(), "edge", r.remote, r.item, o)
			var refused *RefusedError
			if !errors.As(err, &refused) {
				b, _ := os.ReadFile(filepath.Join(r.remote, "remote.toml"))
				t.Fatalf("want a RefusedError, got %v\n%s", err, b)
			}
			t.Logf("reasons: %q", refused.Reasons)
			if !strings.Contains(strings.Join(refused.Reasons, "\n"), tc.want) {
				t.Errorf("reasons %q do not name %q", refused.Reasons, tc.want)
			}
			if len(rep.Steps) != 0 || rep.CloudRefused != 0 {
				t.Errorf("report = %+v", rep)
			}
			r.nothingRan()
		})
	}
}

// AC4: an agent CLI this machine lacks stays an ExecutableNotFoundError behind
// the refusal.
func TestRefusalKeepsExecutableNotFound(t *testing.T) {
	r := newRig(t)
	replaceIn(t, r.remote, "workflows/agents.toml", `command = "fake-agent -p {system} --tools {tools}"`,
		`command = "satelle-no-such-agent-xyz -p {system} --tools {tools}"`)
	o := r.opts()
	o.NewRunner = nil
	_, err := Run(context.Background(), "edge", r.remote, r.item, o)
	var enf *agentstep.ExecutableNotFoundError
	if !errors.As(err, &enf) || enf.Token != "satelle-no-such-agent-xyz" {
		t.Fatalf("want ExecutableNotFoundError behind the refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "step plan→in_progress") && !strings.Contains(err.Error(), "step backlog→plan") {
		t.Errorf("the reason must name the step: %v", err)
	}
}

// AC4: the refusing cloud launch is installed on every engine the run builds, so
// a binding the pre-flight did not foresee still never reaches a cloud: the
// dispatch is refused by name, not launched.
func TestInstalledCloudDispatchRefuses(t *testing.T) {
	r := newRig(t)
	replaceIn(t, r.remote, "workflows/agents.toml", `[worker]
role = "agent"`, `[worker]
role = "agent"
interface = "cloud"`)
	docs := loadDocs(r.remote)
	agents, err := config.LoadAgents(r.remote)
	if err != nil {
		t.Fatal(err)
	}
	eng := agentstep.New(nil, docs, r.main, "")
	eng.SetNamedAgents(agents.NamedBinding)
	w := &wiring{scratch: r.main, opts: r.opts(), newRunner: r.opts().NewRunner}
	w.apply(eng)

	_, derr := eng.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_sample", Category: "feature", Status: "backlog"}, "plan")
	if derr == nil || !strings.Contains(derr.Error(), "never launches a cloud session") || !strings.Contains(derr.Error(), `"plan"`) {
		t.Fatalf("want the installed refusal naming the step, got %v", derr)
	}
	if w.cloudRefused != 1 {
		t.Errorf("the refusal must stand in for the launch exactly once, got %d", w.cloudRefused)
	}
}
