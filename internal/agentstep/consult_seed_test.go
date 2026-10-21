package agentstep

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_83620b39: the rework relay's consult seat is told what the coder's grant
// can run and which later gates own the evidence it cannot produce.

func TestConsultSeedNamesCoderGrantAndLaterGates(t *testing.T) {
	g, _ := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	item := workitem.Item{ID: "sty_cs", Status: "in_progress"}
	tools := "Read,Edit,Bash(go test:*)"

	seed, err := g.ConsultSeed(context.Background(), item, "reviewer-consult", ReworkContext{
		CoderBinding: "coder", CoderTools: tools,
		LaterGates: []LaterGate{{Step: "done", Skill: "integration-review"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Story  workitem.Item `json:"story"`
		Rework *struct {
			CoderBinding string `json:"coder_binding"`
			CoderTools   string `json:"coder_tools"`
			LaterGates   []struct {
				Step  string `json:"step"`
				Skill string `json:"skill"`
			} `json:"later_gates"`
		} `json:"rework"`
	}
	if err := json.Unmarshal([]byte(seed), &got); err != nil {
		t.Fatal(err)
	}
	if got.Story.ID != "sty_cs" {
		t.Errorf("the seed must still carry the story payload, got %+v", got.Story)
	}
	if got.Rework == nil {
		t.Fatalf("seed has no rework object: %s", seed)
	}
	if got.Rework.CoderBinding != "coder" || got.Rework.CoderTools != tools {
		t.Errorf("rework = %+v, want the coder binding and its grant verbatim", got.Rework)
	}
	if len(got.Rework.LaterGates) != 1 || got.Rework.LaterGates[0].Step != "done" || got.Rework.LaterGates[0].Skill != "integration-review" {
		t.Errorf("later_gates = %+v", got.Rework.LaterGates)
	}

	plain, err := g.SessionSeed(context.Background(), item, "reviewer-consult")
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(plain), &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["rework"]; ok {
		t.Error("SessionSeed must not carry a rework object")
	}
}

func TestConsultSeedWithNoLaterGatesKeepsAnEmptyList(t *testing.T) {
	g, _ := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	seed, err := g.ConsultSeed(context.Background(), workitem.Item{ID: "sty_cs", Status: "in_progress"}, "c", ReworkContext{CoderBinding: "coder", CoderTools: "Read"})
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		Rework map[string]json.RawMessage `json:"rework"`
	}
	if err := json.Unmarshal([]byte(seed), &top); err != nil {
		t.Fatal(err)
	}
	if string(top.Rework["later_gates"]) != "[]" {
		t.Errorf("later_gates = %s, want [] so the consultant sees there is none", top.Rework["later_gates"])
	}
}
