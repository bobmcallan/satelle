package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_e2b78a8d: a functional check's verdict carries what the check printed,
// never the script that printed it. These fixtures run the real engine over the
// embedded route and real check scripts; the delivery assertion lives in
// gateverdict_test.go, the one place allowed to build a gate-handle verdict.

const checkScriptSentinel = "SCRIPT-BODY-SENTINEL-9f3a"

// checkDocs serves the embedded route halves and resolves every skill to a
// functional check whose script carries the sentinel.
type checkDocs struct{}

func (checkDocs) workflows() []docindex.Doc {
	var out []docindex.Doc
	for _, e := range config.EmbeddedDefaults() {
		if e.Kind == "workflows" {
			out = append(out, docindex.Doc{Kind: e.Kind, Name: e.Name, Body: e.Body})
		}
	}
	return out
}

func (c checkDocs) Get(_ context.Context, kind, name string) (docindex.Doc, error) {
	switch kind {
	case "workflows":
		for _, d := range c.workflows() {
			if d.Name == name {
				return d, nil
			}
		}
	case "skills":
		body := "---\nname: " + name + "\ntype: skill\ndescription: functional check\n---\n# " + name +
			"\n\n```check\n# " + checkScriptSentinel + "\necho result-line-from-check\n```\n"
		return docindex.Doc{Kind: kind, Name: name, Body: body}, nil
	}
	return docindex.Doc{}, docindex.ErrNotFound
}

func (c checkDocs) List(_ context.Context, kind string) ([]docindex.Doc, error) {
	if kind == "workflows" {
		return c.workflows(), nil
	}
	return nil, nil
}

type noRunner struct{ agentcli.Runner }

// acceptLinesFromChecks runs an edge carrying at least two functional checks and
// returns the accept line the verb layer would record for each.
func acceptLinesFromChecks(t *testing.T) string {
	t.Helper()
	g := agentstep.New(noRunner{}, checkDocs{}, t.TempDir(), "")
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_chk", Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Reviewers) < 2 {
		t.Fatalf("want an edge carrying at least two functional checks, got %+v", dec)
	}
	var lines []string
	for _, rv := range dec.Reviewers {
		lines = append(lines, fmt.Sprintf("accepted in_progress→done by %s: decision=accept notes=%s", rv.Skill, rv.Notes))
	}
	return strings.Join(lines, "\n") + "\n"
}
