package wfgovern

import (
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// AC7 (sty_81bb0dde): a repo whose route source is still MARKDOWN must fail
// loudly, naming the conversion — never silently resolve to no route, and never
// to the binary's embedded default.
//
// Both silent outcomes are worse than the refusal. "No route" is treated by
// callers as a fresh repo and lets a transition through ungated; falling back to
// the shipped default runs someone else's lifecycle on a repo that authored its
// own. The refusal is the only honest answer, and it has to carry the remedy
// because the operator who hits it is mid-upgrade with no other signal.

func mdDoc(name, ext string) docindex.Doc {
	return docindex.Doc{Kind: "workflows", Name: name, Path: "/repo/.satelle/workflows/" + name + ext,
		Body: "---\nname: " + name + "\n---\n\n## *\n- raised\n"}
}

func embeddedHalf(name string) docindex.Doc {
	return docindex.Doc{Kind: "workflows", Name: name, Embedded: true,
		Path: "embedded:workflows/" + name + ".toml",
		Body: "[meta]\nname = \"" + name + "\"\n\n[\"*\"]\nobligations = [\"raised\"]\n",
		Ext:  ".toml",
	}
}

func TestLegacyMarkdownRouteIsRefusedByName(t *testing.T) {
	item := workitem.Item{Kind: workitem.KindStory, Category: "feature"}

	for _, tc := range []struct {
		name string
		docs []docindex.Doc
		want []string
	}{
		{
			name: "both halves markdown",
			docs: []docindex.Doc{mdDoc("done", ".md"), mdDoc("step", ".md")},
			want: []string{"done.md", "step.md", "done.toml", "step.toml", "workflow-convert"},
		},
		{
			// Half a route is not a route. Applying the converted half alone would
			// drop every gate the other half declared, so this must refuse too.
			name: "half converted",
			docs: []docindex.Doc{
				{Kind: "workflows", Name: "done", Path: "/repo/.satelle/workflows/done.toml",
					Body: "[meta]\nname = \"done\"\n\n[\"*\"]\nobligations = [\"raised\"]\n"},
				mdDoc("step", ".md"),
			},
			want: []string{"step.md", "step.toml", "workflow-convert"},
		},
		{
			// The refusal must BEAT the embedded overlay. A repo with done.md on
			// disk has a route it intends; resolving to the SHIPPED default because
			// its own file no longer parses would run someone else's lifecycle
			// without saying so.
			name: "markdown on disk beside the embedded default",
			docs: []docindex.Doc{
				mdDoc("done", ".md"), mdDoc("step", ".md"),
				embeddedHalf("done"), embeddedHalf("step"),
			},
			want: []string{"done.md", "step.md", "done.toml", "step.toml"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := SpecFor(tc.docs, item)
			if err == nil {
				t.Fatal("a markdown route source must be refused, not resolved")
			}
			if !errors.Is(err, ErrLegacyMarkdownRoute) {
				t.Errorf("error must be ErrLegacyMarkdownRoute, got %v", err)
			}
			// Distinct from ErrNoWorkflow ON PURPOSE: callers treat that one as a
			// fresh repo and let the transition through, so collapsing the two would
			// advance a story past every gate the repo authored.
			if errors.Is(err, ErrNoWorkflow) {
				t.Error("must not collapse into ErrNoWorkflow — that reads as 'fresh repo, let it through'")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal must name %q, got: %v", want, err)
				}
			}
		})
	}
}

// The binary ships its defaults as TOML by construction, so flagging one would
// mean the binary refusing itself — which bricks every repo that has authored no
// route at all. Both the Embedded flag and the `embedded:` path scheme are
// checked, because relying on the flag alone did exactly that during development.
func TestEmbeddedRouteSourceIsNeverFlaggedAsLegacy(t *testing.T) {
	docs := []docindex.Doc{embeddedHalf("done"), embeddedHalf("step")}
	if stale := LegacyMarkdownRoute(docs); len(stale) != 0 {
		t.Fatalf("the binary must not refuse its own defaults, got %v", stale)
	}
	// Path scheme alone is enough, even with the flag unset.
	unflagged := []docindex.Doc{
		{Kind: "workflows", Name: "done", Path: "embedded:workflows/done.md"},
		{Kind: "workflows", Name: "step", Path: "embedded:workflows/step.md"},
	}
	if stale := LegacyMarkdownRoute(unflagged); len(stale) != 0 {
		t.Fatalf("an embedded: path must not be flagged, got %v", stale)
	}
}

// A CONVERTED repo passes straight through — the refusal must not fire on the
// state it exists to produce.
func TestConvertedRouteSourceIsNotRefused(t *testing.T) {
	docs := []docindex.Doc{
		{Kind: "workflows", Name: "done", Path: "/repo/.satelle/workflows/done.toml",
			Body: "[meta]\nname = \"done\"\n\n[\"*\"]\nobligations = [\"raised\"]\n"},
		{Kind: "workflows", Name: "step", Path: "/repo/.satelle/workflows/step.toml",
			Body: "[meta]\nname = \"step\"\n\n[raised]\nstatus = \"backlog\"\nstart = true\nterminal = true\n"},
	}
	if stale := LegacyMarkdownRoute(docs); len(stale) != 0 {
		t.Fatalf("a converted repo must not be flagged, got %v", stale)
	}
	_, name, _, err := SpecFor(docs, workitem.Item{Kind: workitem.KindStory, Category: "feature"})
	if err != nil {
		t.Fatalf("a converted route must resolve: %v", err)
	}
	if name != DerivedRouteName {
		t.Errorf("governing lifecycle = %q, want %q", name, DerivedRouteName)
	}
}
