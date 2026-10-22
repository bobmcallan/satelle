package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// citationRunner is the reviewer subprocess: it accepts, and keeps the
// citations material the work item named while the scratch directory still exists.
type citationRunner struct {
	citations []byte
	payload   string
}

func (*citationRunner) Name() string    { return "citation-runner" }
func (*citationRunner) Command() string { return "citation-runner -p {system}" }
func (r *citationRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	r.payload = req.Payload
	var top struct {
		Citations *struct {
			Path string `json:"path"`
		} `json:"citations"`
	}
	if json.Unmarshal([]byte(req.Payload), &top) == nil && top.Citations != nil {
		r.citations, _ = os.ReadFile(top.Citations.Path)
	}
	return []byte(`{"decision":"accept","notes":"ok"}`), nil
}

// sty_d0c9245a AC1: the readiness edge runs before a story has an engagement
// baseline. With the resolver the app wires (verb.StoryGateTree) the plan's
// citations still resolve to the lines they name, in the tree the gate runs in.
func TestPlanReviewCitationsResolveForAStoryWithNoBaseline(t *testing.T) {
	e := closeGuardEnv(t)
	e.write("internal/x/svc.go", "package x\n\nfunc One() {}\n\nfunc Two() {}\n")
	id := e.story("never engaged") // created, never set to a performing status

	meta := func(name string) string {
		return "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"citation fixture\"\n\n"
	}
	step := strings.Replace(closeRouteStep, "[coded]", "[planned]\nstatus = \"plan\"\nreviewers = [\"plan-cite-review\"]\nrequires = [\"raised\"]\n\n[coded]", 1)
	step = strings.Replace(step, "agent = \"executor\"\nrequires = [\"raised\"]", "agent = \"executor\"\nrequires = [\"planned\"]", 1)
	done := strings.Replace(closeRouteDone, `"coded"`, `"planned", "coded"`, 1)
	docs := memDocs{docs: []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: meta("done") + done},
		{Kind: "workflows", Name: "step", Body: meta("step") + step},
		{Kind: "skills", Name: "plan-cite-review", Body: "---\nname: plan-cite-review\ntype: skill\ndescription: cites\npayload_citations: plan\n---\n\n" +
			"Judge the plan.\n\nReturn JSON {\"decision\": \"accept\"|\"reject\", \"notes\": \"…\"}.\n"},
	}}

	r := &citationRunner{}
	g := agentstep.New(r, docs, e.repo, "")
	g.SetDocsResolver(func(context.Context, string) []agentstep.DocState {
		return []agentstep.DocState{{Name: "plan", Type: "plan", Body: "Owner `internal/x/svc.go:5` and gone/away.go:1."}}
	})
	g.SetCitationTreeResolver(verb.StoryGateTree)

	dec, err := g.Gate(context.Background(), workitem.Item{ID: id, Status: "backlog"}, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Reviewers) == 0 {
		t.Fatalf("the edge must be judged by the fixture rubric: %+v", dec)
	}
	var got agentstep.CitationsState
	if err := json.Unmarshal(r.citations, &got); err != nil || len(got.Citations) != 2 {
		t.Fatalf("citations material = %q (%v), want two entries; payload:\n%s", r.citations, err, r.payload)
	}
	if c := got.Citations[0]; c.Unresolved || !strings.Contains(c.Excerpt, "5: func Two() {}") || strings.Contains(c.Reason, "judged tree unavailable") {
		t.Errorf("internal/x/svc.go:5 must resolve with no baseline: %+v", c)
	}
	if c := got.Citations[1]; !c.Unresolved || c.Reason != "file not found" {
		t.Errorf("gone/away.go:1 must be listed as unresolved: %+v", c)
	}
}
