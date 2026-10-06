package agentstep

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/placement"
	"github.com/bobmcallan/satelle/internal/placement/placementtest"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Remote placement (sty_dde8b6a4): a parallel epic child's step is performed by
// the step's remote_agent cloud binding; every other child is performed by the
// step's own agent. The container, its schedule and the signed-in signal are
// real (placementtest); the cloud provider and the local agent are fakes.

const (
	placedLocalAgent  = "worker"
	placedRemoteAgent = "cloudy"
)

// placedStep is the step's raw lines: its remote placement declaration.
const placedStep = `remote_agent = "cloudy"` + "\n" + `local_tags = ["lane:trunk"]`

func placedWF(extra string) string {
	return spineWF("", "", "",
		"plan|"+placedLocalAgent+"|cloud-step||||"+extra,
		"in_progress|executor",
		"done")
}

// placedRig is an engine over a cloud fixture whose local agent is a fake runner.
type placedRig struct {
	cloudEngine
	fix   cloudFix
	cloud *fakeCloud
	local *fakeRunner
	db    *store.DB
	epic  workitem.Item
}

func newPlacedRig(t *testing.T, schedule, stepExtra string) *placedRig {
	t.Helper()
	r := &placedRig{db: nil}
	r.fix = newCloudFix(t)
	r.cloud = &fakeCloud{}
	r.cloud.act = func(branch, nonce string) {
		r.fix.session(t, branch, tipMessage(cloudEvidenceBody, nonce), map[string]string{"b.txt": "b\n"})
	}
	installCloud(t, agentcli.HarnessClaude, r.cloud)
	docs := fakeDocs{workflow: placedWF(stepExtra), extraSkills: []docindex.Doc{
		{Kind: "skills", Name: "cloud-step", Body: cloudStepSkill},
		{Kind: "skills", Name: cloudPerformerSkill, Body: cloudPerformerBody},
	}}
	r.cloudEngine = newCloudEngine(t, r.fix, docs, cloudBinding("claude -p {system}", cloudDocName, cloudTestShortLimit))
	r.local = &fakeRunner{out: "did the work"}
	r.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		switch name {
		case placedRemoteAgent:
			return cloudBinding("claude -p {system}", cloudDocName, cloudTestShortLimit), true
		case placedLocalAgent:
			return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Grep,Glob,Bash(satelle:*)"}, true
		}
		return config.AgentBinding{}, false
	})
	r.newRunner = func(string, string) (agentcli.Runner, error) { return r.local, nil }
	r.db = placementtest.Wire(t, schedule)
	r.epic = placementtest.Epic(t, r.db)
	return r
}

// ranLocal reports whether the step's own agent was dispatched.
func (r *placedRig) ranLocal() bool { return r.local.got.SystemPrompt != "" }

func (r *placedRig) dispatchItem(item workitem.Item) (string, error) {
	res, err := r.DispatchExecutor(context.Background(), item, "plan")
	return res.Agent, err
}

func (r *placedRig) telemetry(kind string) []telemetryRec {
	var out []telemetryRec
	for _, rec := range *r.tele {
		if rec.kind == kind {
			out = append(out, rec)
		}
	}
	return out
}

// AC2: signed in, a parallel child with none of the local tags is performed by
// the remote_agent through the cloud path, and the cloud_dispatch row says so.
func TestPlacementRemoteChildRunsInTheCloud(t *testing.T) {
	r := newPlacedRig(t, "parallel", placedStep)
	placementtest.SignIn(t)
	item := placementtest.Child(t, r.db, r.epic)

	res, err := r.DispatchExecutor(context.Background(), item, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if res.Agent != placedRemoteAgent || res.Cloud == nil || res.Cloud.Placement != placement.Remote {
		t.Fatalf("result = %+v, want agent %s with a remote-placed cloud record", res, placedRemoteAgent)
	}
	if r.cloud.calls != 1 || r.ranLocal() {
		t.Fatalf("cloud calls = %d, local ran = %v; want the cloud session only", r.cloud.calls, r.ranLocal())
	}
	rows := r.telemetry("cloud_dispatch")
	if len(rows) != 1 || rows[0].data["placement"] != "remote" || rows[0].data["outcome"] != "collected" {
		t.Fatalf("cloud_dispatch rows = %+v, want one collected row with placement remote", rows)
	}
}

// AC3: the step's own agent performs, unchanged, and the cloud runner is never
// called, for a local-tag child, a sequential epic, a story that is not an epic
// child, and a step with no remote_agent.
func TestPlacementKeepsTheStepsOwnAgent(t *testing.T) {
	cases := []struct {
		name     string
		schedule string
		stepArgs string
		child    func(t *testing.T, r *placedRig) workitem.Item
	}{
		{"local tag", "parallel", placedStep, func(t *testing.T, r *placedRig) workitem.Item {
			return placementtest.Child(t, r.db, r.epic, "lane:trunk")
		}},
		{"sequential epic", "sequential", placedStep, func(t *testing.T, r *placedRig) workitem.Item {
			return placementtest.Child(t, r.db, r.epic)
		}},
		{"not an epic child", "parallel", placedStep, func(t *testing.T, r *placedRig) workitem.Item {
			return placementtest.Orphan(t, r.db)
		}},
		{"no remote_agent", "parallel", "", func(t *testing.T, r *placedRig) workitem.Item {
			return placementtest.Child(t, r.db, r.epic)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newPlacedRig(t, tc.schedule, tc.stepArgs)
			placementtest.SignIn(t)
			agent, err := r.dispatchItem(tc.child(t, r))
			if err != nil {
				t.Fatal(err)
			}
			if agent != placedLocalAgent || !r.ranLocal() {
				t.Fatalf("agent = %q, local ran = %v; want %s to perform", agent, r.ranLocal(), placedLocalAgent)
			}
			if r.cloud.calls != 0 {
				t.Fatalf("the cloud runner was called %d times", r.cloud.calls)
			}
		})
	}
}

// AC4: a local-only session — the actor resolver yields a git email, the
// assignee resolver nothing — performs the step with its own agent and ledgers
// why.
func TestPlacementNotSignedInUsesTheStepsOwnAgent(t *testing.T) {
	r := newPlacedRig(t, "parallel", placedStep)
	placementtest.LocalOnly(t, "dev@example.com")
	item := placementtest.Child(t, r.db, r.epic)

	agent, err := r.dispatchItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if agent != placedLocalAgent || !r.ranLocal() || r.cloud.calls != 0 {
		t.Fatalf("agent %q, local ran %v, cloud calls %d; want %s locally", agent, r.ranLocal(), r.cloud.calls, placedLocalAgent)
	}
	rows := r.telemetry("placement")
	if len(rows) != 1 || rows[0].data["placement"] != "placement: remote declared, local used — not signed in" {
		t.Fatalf("placement rows = %+v, want the not-signed-in note", rows)
	}
}

// AC5: a remote child whose cloud session fails stays where it was — the
// dispatch errors, the base agent is not dispatched, and the failure with the
// session URL is on the ledger.
func TestPlacementRemoteFailureLeavesTheChildAndLedgersTheSession(t *testing.T) {
	r := newPlacedRig(t, "parallel", placedStep)
	placementtest.SignIn(t)
	item := placementtest.Child(t, r.db, r.epic)
	r.cloud.act = func(string, string) {} // the session never pushes its branch

	_, err := r.dispatchItem(item)
	if err == nil || !strings.Contains(err.Error(), cloudSessionURL) {
		t.Fatalf("err = %v, want the failure naming the session %s", err, cloudSessionURL)
	}
	if r.ranLocal() {
		t.Fatal("the base agent was dispatched after the remote child failed")
	}
	rows := r.telemetry("cloud_dispatch")
	if len(rows) != 1 || rows[0].data["outcome"] != "timeout" || rows[0].data["url"] != cloudSessionURL || rows[0].data["placement"] != "remote" {
		t.Fatalf("cloud_dispatch rows = %+v, want one timeout row with the URL and placement remote", rows)
	}
	if got, _ := r.db.Stories.Get(context.Background(), item.ID); got.Status != "backlog" {
		t.Fatalf("status = %q; a failed dispatch must leave the child at its from-state", got.Status)
	}
}

// AC5: a launch that fails outright also dispatches nothing locally.
func TestPlacementRemoteLaunchFailureDoesNotFallBackToLocal(t *testing.T) {
	r := newPlacedRig(t, "parallel", placedStep)
	placementtest.SignIn(t)
	r.cloud.err = errors.New("boom")

	if _, err := r.dispatchItem(placementtest.Child(t, r.db, r.epic)); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the launcher's error", err)
	}
	if r.ranLocal() {
		t.Fatal("the base agent was dispatched after the remote launch failed")
	}
}

// AC7: a remote-placed child whose branch is not pushed is refused before
// launch, naming the child, the placement and the push command; nothing is
// pushed.
func TestPlacementRemoteRefusesUnpushedBranch(t *testing.T) {
	r := newPlacedRig(t, "parallel", placedStep)
	placementtest.SignIn(t)
	if err := os.WriteFile(filepath.Join(r.fix.work, "a.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, r.fix.work, "commit", "-q", "-am", "unpushed")
	before := gitT(t, r.fix.remote, "rev-parse", "main")
	item := placementtest.Child(t, r.db, r.epic)

	_, err := r.dispatchItem(item)
	if err == nil {
		t.Fatal("an unpushed remote child must be refused")
	}
	for _, want := range []string{item.ID, "placed remote", "not pushed", "git push -u origin main"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q must contain %q", err, want)
		}
	}
	if r.cloud.calls != 0 || r.ranLocal() {
		t.Fatalf("cloud calls = %d, local ran = %v; nothing may run", r.cloud.calls, r.ranLocal())
	}
	if after := gitT(t, r.fix.remote, "rev-parse", "main"); after != before {
		t.Fatal("the dispatch pushed for the operator")
	}
}
