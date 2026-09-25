package wfgovern

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

func init() {
	EmbeddedRoute = func(name string) string {
		for _, d := range config.EmbeddedDefaults() {
			if d.Kind == "workflows" && d.Name == name {
				return d.Body
			}
		}
		return ""
	}
}

// authoredDocs is the list the doc index yields: the repo's own half where it
// authored one, and the shipped default for a half it did not.
func authoredDocs(done, step string) []docindex.Doc {
	ds := embeddedDocs()
	if done != "" {
		ds[0] = docindex.Doc{Kind: "workflows", Name: RouteSourceDone, Body: done}
	}
	if step != "" {
		ds[1] = docindex.Doc{Kind: "workflows", Name: RouteSourceStep, Body: step}
	}
	return ds
}

func embeddedDocs() []docindex.Doc {
	return []docindex.Doc{
		{Kind: "workflows", Name: RouteSourceDone, Body: embeddedRouteBody(RouteSourceDone), Embedded: true},
		{Kind: "workflows", Name: RouteSourceStep, Body: embeddedRouteBody(RouteSourceStep), Embedded: true},
	}
}

func stepNames(c wfdot.Catalogue) []string {
	var out []string
	for _, s := range c.Steps {
		out = append(out, s.Name)
	}
	return out
}

func TestRouteMerge_OneCategoryKeepsBaseline(t *testing.T) {
	over := "[docs]\nobligations = [\"raised\", \"authored\"]\n"
	rs := RouteSourceOf(authoredDocs(over, ""))
	base := RouteSourceOf(embeddedDocs())

	have := strings.Join(RouteCategories(rs.Done), ",")
	for _, want := range RouteCategories(base.Done) {
		if !strings.Contains(","+have+",", ","+want+",") {
			t.Fatalf("category %q lost after one-category override; have %s", want, have)
		}
	}
	for _, cat := range []string{"*", "execution", "epic-parent"} {
		got, err := RouteSpecFor(rs, cat, nil)
		if err != nil {
			t.Fatalf("%s: %v", cat, err)
		}
		want, err := RouteSpecFor(base, cat, nil)
		if err != nil {
			t.Fatalf("baseline %s: %v", cat, err)
		}
		if !reflect.DeepEqual(got.Spec, want.Spec) || !reflect.DeepEqual(got.Catalogue.Gates, want.Catalogue.Gates) {
			t.Errorf("category %q changed by an override of docs", cat)
		}
	}
	got, err := RouteSpecFor(rs, "docs", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(got.List.Obligations, mustList(t, base, "docs").Obligations) {
		t.Errorf("docs kept the baseline obligations; the repo table must replace it")
	}
}

func mustList(t *testing.T, rs RouteSource, cat string) wfdot.List {
	t.Helper()
	d, err := RouteSpecFor(rs, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d.List
}

func TestRouteMerge_OneStepKeepsBaseline(t *testing.T) {
	over := "[coded]\nstatus = \"in_progress\"\nagent = \"coder\"\nrequires = [\"raised\"]\n"
	rs := RouteSourceOf(authoredDocs("", over))
	base := RouteSourceOf(embeddedDocs())

	got, err := RouteSpecFor(rs, "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := RouteSpecFor(base, "*", nil)
	if !reflect.DeepEqual(stepNames(got.Catalogue), stepNames(want.Catalogue)) {
		t.Fatalf("step set changed: %v vs %v", stepNames(got.Catalogue), stepNames(want.Catalogue))
	}
	for _, s := range got.Catalogue.Steps {
		if s.Name == "coded" && s.Agent != "coder" {
			t.Errorf("coded agent = %q, want the repo's coder", s.Agent)
		}
		if s.Name == "closed" {
			for _, w := range want.Catalogue.Steps {
				if w.Name == "closed" && !reflect.DeepEqual(s, w) {
					t.Errorf("closed changed by a coded override")
				}
			}
		}
	}
	if !reflect.DeepEqual(got.Catalogue.Gates, want.Catalogue.Gates) {
		t.Errorf("baseline gates lost when the repo named none")
	}
}

func TestRouteMerge_GateBySkill(t *testing.T) {
	base := "[[gate]]\nskill = \"a\"\nmandatory = true\n\n[[gate]]\nskill = \"b\"\n"
	over := "[[gate]]\nskill = \"b\"\nagent = \"x\"\n\n[[gate]]\nskill = \"c\"\n"
	out := mergeRouteHalf(base, over)
	cat, err := wfdot.ParseSteps(out + "\n[raised]\nstatus = \"backlog\"\nstart = true\n")
	if err != nil {
		t.Fatal(err)
	}
	var skills []string
	for _, g := range cat.Gates {
		skills = append(skills, g.Skill)
		if g.Skill == "b" && g.Agent != "x" {
			t.Errorf("b not replaced: %+v", g)
		}
	}
	if strings.Join(skills, ",") != "b,c" {
		t.Errorf("gates = %v, want b,c (a repo gate list replaces the baseline list)", skills)
	}
}

func TestRouteMerge_EmptyRepoKeepsBaselineBytes(t *testing.T) {
	const base = "[raised]\nstatus = \"backlog\"\n"
	if got := mergeRouteHalf(base, "  \n"); got != base {
		t.Errorf("got %q, want the baseline unchanged", got)
	}
}

func TestRouteMerge_NoRepoFileIsShippedRoute(t *testing.T) {
	rs := RouteSourceOf(embeddedDocs())
	if !rs.Embedded {
		t.Fatal("no repo file must stay Embedded")
	}
	if rs.Done != embeddedRouteBody(RouteSourceDone) || rs.Step != embeddedRouteBody(RouteSourceStep) {
		t.Fatal("no merge may run when the repo authored nothing")
	}
	if _, err := RouteSpecFor(rs, "*", nil); err != nil {
		t.Fatal(err)
	}
}

func TestRouteMerge_BrokenRepoBodyReturnedAsWritten(t *testing.T) {
	const bad = "[coded\n"
	if got := mergeRouteHalf("[raised]\nstatus = \"backlog\"\n", bad); got != bad {
		t.Errorf("a broken repo body must reach the parser as written, got %q", got)
	}
}
