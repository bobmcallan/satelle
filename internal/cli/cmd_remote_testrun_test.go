package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotetest"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// remoteAgents are the fake agents behind `satelle remote test`: the real engine
// factory builds the engine, only the agent CLIs it starts are replaced.
type remoteAgents struct {
	mu       sync.Mutex
	performs int
	reviews  int
}

type fakePerformer struct{ a *remoteAgents }

func (p fakePerformer) Name() string    { return "fake-performer" }
func (p fakePerformer) Command() string { return "fake-agent -p {system}" }
func (p fakePerformer) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	p.a.mu.Lock()
	p.a.performs++
	p.a.mu.Unlock()
	if err := os.WriteFile(filepath.Join(req.Dir, "performed.txt"), []byte("hi\n"), 0o644); err != nil {
		return nil, err
	}
	return []byte(`{"artifact":{"body":"EV"}}`), nil
}

type fakeReviewer struct{ a *remoteAgents }

func (r fakeReviewer) Name() string    { return "fake-reviewer" }
func (r fakeReviewer) Command() string { return "fake-reviewer -p {system}" }
func (r fakeReviewer) Run(context.Context, agentcli.Request) ([]byte, error) {
	r.a.mu.Lock()
	r.a.reviews++
	r.a.mu.Unlock()
	return []byte(`{"decision":"accept"}`), nil
}

func installRemoteAgents(t *testing.T) *remoteAgents {
	t.Helper()
	a := &remoteAgents{}
	remoteTestNewRunner = func(string, string) (agentcli.Runner, error) { return fakePerformer{a}, nil }
	remoteTestReviewerRunner = fakeReviewer{a}
	t.Cleanup(func() { remoteTestNewRunner, remoteTestReviewerRunner = nil, nil })
	return a
}

// remoteTestRig is a committed repo with a sample story and the fixture remote
// "edge". The worker runs as a claude harness whose gate wiring the repo declares
// (.claude/settings.json, gitignored and listed in [worktree] include).
type remoteTestRig struct {
	t        *testing.T
	repo     string
	story    string
	sentinel string
	remote   string
}

func newRemoteTestRig(t *testing.T) *remoteTestRig {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".claude/\n"), 0o644))
	must(os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"),
		[]byte("[review]\ngate_create = false\n\n[worktree]\ninclude = [\".claude/settings.json\"]\n"), 0o644))
	gitInitCommit(t, repo)
	must(os.MkdirAll(filepath.Join(repo, ".claude"), 0o755))
	must(os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte("{}"), 0o644))

	hostDir, err := os.MkdirTemp("", "host")
	must(err)
	t.Cleanup(func() { _ = os.RemoveAll(hostDir) })
	r := &remoteTestRig{t: t, repo: repo, sentinel: filepath.Join(hostDir, "host-ran")}
	r.remote = remotedef.Dir(filepath.Join(repo, ".satelle"), "edge")
	testutil.WriteRemoteFixture(t, r.remote, r.sentinel)
	// A claude harness, so the dispatch guard asks for the wiring declared for it.
	r.rewriteAgents(func(s string) string {
		s = strings.Replace(s, testutil.RemoteFixtureWorkerCommand, "claude -p {system} --tools {tools}", 1)
		return strings.Replace(s, `tools = "read_file,grep,list_dir"`, `tools = "Read,Grep,Bash(satelle:*)"`, 1)
	})

	out, err := runRoot(t, "story", "create", "--title", "sample story", "--body", "goal",
		"--acceptance", "1. ok", "--category", "chore")
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	r.story = idOf(t, out)
	return r
}

func (r *remoteTestRig) rewriteAgents(f func(string) string) {
	r.t.Helper()
	path := filepath.Join(r.remote, "workflows", "agents.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(f(string(b))), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *remoteTestRig) test(args ...string) (string, error) {
	r.t.Helper()
	return runRoot(r.t, append([]string{"remote", "test", "edge", "--story", r.story}, args...)...)
}

// remoteTestRows are the remote_test ledger rows of the sample story.
func (r *remoteTestRig) rows() []ledger.Entry {
	r.t.Helper()
	out, err := runRoot(r.t, "ledger", "list", "--story", r.story, "--kind", ledger.KindRemoteTest)
	if err != nil {
		r.t.Fatalf("ledger list: %v\n%s", err, out)
	}
	var rows []ledger.Entry
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		r.t.Fatalf("ledger list is not an entry array: %v\n%s", err, out)
	}
	return rows
}

// scratchGone fails when a run left a worktree, branch or scratch directory.
func (r *remoteTestRig) scratchGone() {
	r.t.Helper()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", r.repo}, args...)...).CombinedOutput()
		if err != nil {
			r.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if wt := git("worktree", "list", "--porcelain"); strings.Count(wt, "worktree ") != 1 {
		r.t.Errorf("scratch worktree left behind:\n%s", wt)
	}
	if br := git("branch", "--list", "remote-test/*"); br != "" {
		r.t.Errorf("scratch branch left behind: %s", br)
	}
}

// AC1/AC3: every edge is printed with its from→to, skill, binding, identity and
// verdict; one remote_test row per edge; the story and its docs are untouched;
// no host command ran; the scratch tree is gone.
func TestRemoteTestPrintsEveryEdge(t *testing.T) {
	r := newRemoteTestRig(t)
	agents := installRemoteAgents(t)
	storyBefore, err := runRoot(t, "story", "get", r.story)
	if err != nil {
		t.Fatal(err)
	}
	docsBefore, _ := runRoot(t, "story", "docs", r.story)

	out, err := r.test("--base", "HEAD")
	if err != nil {
		t.Fatalf("remote test: %v\n%s", err, out)
	}
	for _, want := range []string{
		"backlog→plan  skill=plan-work  binding=worker  identity=your-host-label  verdict=performed",
		"plan→in_progress  skill=remote-work  binding=worker  identity=your-host-label  verdict=accept",
		"reviewed by reviewer",
		"in_progress→done  skill=-  binding=-  identity=your-host-label  verdict=no-op",
		"(not run)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if agents.performs != 2 || agents.reviews != 1 {
		t.Errorf("agents ran performs=%d reviews=%d, want 2 and 1", agents.performs, agents.reviews)
	}

	rows := r.rows()
	if len(rows) != 3 {
		t.Fatalf("want one remote_test row per edge, got %d", len(rows))
	}
	for i, verdict := range []string{"performed", "accept", "no-op"} {
		var p map[string]string
		if err := json.Unmarshal(rows[i].Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p["remote"] != "edge" || p["verdict"] != verdict {
			t.Errorf("row %d payload = %v, want remote edge verdict %s", i, p, verdict)
		}
	}

	storyAfter, _ := runRoot(t, "story", "get", r.story)
	if storyAfter != storyBefore {
		t.Errorf("the story changed:\nbefore: %s\nafter: %s", storyBefore, storyAfter)
	}
	if docsAfter, _ := runRoot(t, "story", "docs", r.story); docsAfter != docsBefore {
		t.Errorf("the story's documents changed:\nbefore: %s\nafter: %s", docsBefore, docsAfter)
	}
	if _, err := os.Stat(r.sentinel); !os.IsNotExist(err) {
		t.Errorf("a host command ran (err=%v)", err)
	}
	r.scratchGone()
}

// AC3: --json carries the same report, with the binding from the remote's own
// agents.toml.
func TestRemoteTestJSON(t *testing.T) {
	r := newRemoteTestRig(t)
	installRemoteAgents(t)
	out, err := r.test("--base", "HEAD", "--json")
	if err != nil {
		t.Fatalf("remote test --json: %v\n%s", err, out)
	}
	var rep remotetest.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not a report: %v\n%s", err, out)
	}
	if rep.Remote != "edge" || rep.Story != r.story || rep.Identity != "your-host-label" || len(rep.Steps) != 3 {
		t.Fatalf("report = %+v", rep)
	}
	if s := rep.Steps[1]; s.Binding != "worker" || s.Reviewer != "reviewer" || s.Verdict != "accept" {
		t.Errorf("step 1 = %+v", s)
	}
	bound, err := config.LoadAgents(r.remote)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bound.NamedBinding(rep.Steps[1].Binding); !ok {
		t.Errorf("binding %q is not in the remote's agents.toml", rep.Steps[1].Binding)
	}
}

// AC1: --base is required, as it is for `satelle story worktree`.
func TestRemoteTestRequiresBase(t *testing.T) {
	r := newRemoteTestRig(t)
	agents := installRemoteAgents(t)
	out, err := r.test()
	if err == nil || !strings.Contains(out+err.Error(), "base") {
		t.Fatalf("a missing --base must be refused naming it, got err=%v\n%s", err, out)
	}
	if agents.performs != 0 || len(r.rows()) != 0 {
		t.Error("a refused run must dispatch nothing and write nothing")
	}
	r.scratchGone()
}

// AC2: the engine is built through the real CLI grant path, so the wiring guard
// holds a performer in the scratch tree to the gate wiring the main tree
// declares: carried by [worktree] include it is not refused; without the include
// the step is rejected with the guard's own reason.
func TestRemoteTestHoldsPerformerToGateWiring(t *testing.T) {
	t.Run("wiring carried", func(t *testing.T) {
		r := newRemoteTestRig(t)
		agents := installRemoteAgents(t)
		out, err := r.test("--base", "HEAD")
		if err != nil {
			t.Fatalf("a performer with its wiring carried must not be refused: %v\n%s", err, out)
		}
		if strings.Contains(out, "lacks gate wiring") || agents.performs != 2 {
			t.Errorf("performs=%d\n%s", agents.performs, out)
		}
	})
	t.Run("include missing", func(t *testing.T) {
		r := newRemoteTestRig(t)
		agents := installRemoteAgents(t)
		if err := os.WriteFile(filepath.Join(r.repo, ".satelle", "satelle.toml"),
			[]byte("[review]\ngate_create = false\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := r.test("--base", "HEAD")
		if err == nil {
			t.Fatalf("a performer without its wiring must be refused:\n%s", out)
		}
		for _, want := range []string{"backlog→plan", "verdict=reject", "lacks gate wiring", ".claude/settings.json"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
		if agents.performs != 0 {
			t.Error("the performer started despite the refusal")
		}
		if rows := r.rows(); len(rows) != 1 {
			t.Errorf("the refused step is one row, got %d", len(rows))
		}
		r.scratchGone()
	})
}

// AC4: a definition that cannot run exits non-zero with the reason, before any
// dispatch, scratch tree or row.
func TestRemoteTestRefusesUnrunnableDefinition(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(r *remoteTestRig)
		want   string
	}{
		{"host.run without {command}", func(r *remoteTestRig) {
			b, _ := os.ReadFile(filepath.Join(r.remote, "remote.toml"))
			s := strings.Replace(string(b), `"{command}"]`, `"x"]`, 1)
			if err := os.WriteFile(filepath.Join(r.remote, "remote.toml"), []byte(s), 0o644); err != nil {
				r.t.Fatal(err)
			}
		}, "host.run: has no {command} placeholder"},
		{"cloud binding", func(r *remoteTestRig) {
			r.rewriteAgents(func(s string) string {
				return strings.Replace(s, "[worker]\nrole = \"agent\"", "[worker]\nrole = \"agent\"\ninterface = \"cloud\"", 1)
			})
		}, "cloud binding"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRemoteTestRig(t)
			agents := installRemoteAgents(t)
			tc.mutate(r)
			out, err := r.test("--base", "HEAD")
			if err == nil {
				t.Fatalf("an unrunnable definition must exit non-zero:\n%s", out)
			}
			if !strings.Contains(out, "FAIL  remote edge:") || !strings.Contains(out, tc.want) {
				t.Errorf("output must give the reason %q:\n%s", tc.want, out)
			}
			if agents.performs+agents.reviews != 0 || len(r.rows()) != 0 {
				t.Error("nothing may run or be recorded before the refusal")
			}
			r.scratchGone()
		})
	}
}
