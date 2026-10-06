package agentstep

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_d6e209aa AC1, at the engine: while the authored workflows dir is
// unreadable, create, amend, the name a story is stamped with, the restamp
// target, the structure guard and the gate all REFUSE — none answers from the
// embedded default's "no gate declared", and no reviewer runs.
func TestEngineRefusesAnUnreadableAuthoredProcess(t *testing.T) {
	const wfPath = "/repo/.satelle/workflows"
	unreadable := docindex.UnreadableDoc("workflows", wfPath, errors.New("not a directory"))
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{skillFound: true, skillBody: "rubric",
		extraWorkflows: []docindex.Doc{unreadable}})
	ctx := context.Background()

	wantRefusal := func(surface string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s must be refused while the workflows dir is unreadable", surface)
		}
		if !errors.Is(err, wfgovern.ErrAuthoredProcessUnreadable) {
			t.Errorf("%s: want ErrAuthoredProcessUnreadable, got %v", surface, err)
		}
		for _, want := range []string{wfPath, "embedded default route", "gates would not be the repository's gates"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal must carry %q: %v", surface, want, err)
			}
		}
	}

	_, err := g.ReviewCreate(ctx, validDraft)
	wantRefusal("create", err)
	_, err = g.ReviewAmend(ctx, amendDraft)
	wantRefusal("amend", err)
	_, err = g.WorkflowNameFor(ctx, "feature")
	wantRefusal("stamp name (create / restamp)", err)
	_, _, err = g.WorkflowStates(ctx, wfgovern.DerivedRouteName)
	wantRefusal("restamp target", err)
	_, err = g.Gate(ctx, workitem.Item{ID: "sty_1", Status: "backlog", Category: "feature"}, "in_progress")
	wantRefusal("gate", err)
	if r.got.SystemPrompt != "" {
		t.Error("no reviewer may run while the authored process is unreadable")
	}
}
