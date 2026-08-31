package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/docstory"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestFiledSystemStoryIsSurfaceable (sty_88d40a60) closes the loop the whole
// story is about: the story the indexer FILES must be one the surfaces READ.
// The writer and the readers now share docstory's constants, and this is what
// keeps them honest — a drift here would fail silently, in exactly the direction
// that left the diagnosis unread for an hour.
func TestFiledSystemStoryIsSurfaceable(t *testing.T) {
	repo := tempRepo(t)
	if _, err := runRoot(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	a, err := app.Open()
	if err != nil {
		t.Fatalf("open app in %s: %v", repo, err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ctx := context.Background()
	ref := docindex.DocRef{Kind: "workflows", Name: "done"}
	id, filed, err := fileSystemStory(ctx, a, ref, `done.toml: unknown key(s): "*".park.agent`)
	if err != nil || !filed || id == "" {
		t.Fatalf("fileSystemStory = (%q, %v, %v)", id, filed, err)
	}

	it, err := a.Store.Stories.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !docstory.Qualifies(it) {
		t.Fatalf("the story reindex files must qualify for surfacing: %+v", it)
	}
	refs := docstory.Open(ctx, a.Store.Stories.List)
	got, ok := docstory.ForDoc(refs, "workflows", "done")
	if !ok || got.ID != id {
		t.Fatalf("ForDoc(workflows/done) = (%+v, %v), want %s", got, ok, id)
	}
	// The advisory a session start would render names it and the document.
	line := systemDocStoryAdvisory(refs)
	if !strings.Contains(line, id) || !strings.Contains(line, "workflows/done") {
		t.Errorf("advisory must name the story and its document:\n%s", line)
	}
}

// TestSystemDocStoryAdvisory (AC1/AC4) pins the rendered line: silent with no
// refs, naming with one, and capped so a badly broken repo cannot flood a
// session start.
func TestSystemDocStoryAdvisory(t *testing.T) {
	if got := systemDocStoryAdvisory(nil); got != "" {
		t.Errorf("no open diagnosis must add no output, got %q", got)
	}
	one := []docstory.Ref{{ID: "sty_906f59df", Kind: "workflows", Name: "done", Title: "Fix workflows structure: workflows/done"}}
	line := systemDocStoryAdvisory(one)
	for _, want := range []string{"sty_906f59df", "workflows/done", "1 open system story"} {
		if !strings.Contains(line, want) {
			t.Errorf("advisory missing %q:\n%s", want, line)
		}
	}
	if strings.Contains(strings.TrimSpace(line), "\n") {
		t.Errorf("the advisory must stay ONE line so it cannot displace principle content:\n%s", line)
	}
	many := []docstory.Ref{
		{ID: "sty_1", Kind: "workflows", Name: "done"},
		{ID: "sty_2", Kind: "workflows", Name: "step"},
		{ID: "sty_3", Kind: "skills", Name: "a"},
		{ID: "sty_4", Kind: "skills", Name: "b"},
	}
	capped := systemDocStoryAdvisory(many)
	if !strings.Contains(capped, "+1 more") || strings.Contains(capped, "sty_4") {
		t.Errorf("advisory must cap the named ids:\n%s", capped)
	}
	if !strings.Contains(capped, "4 open system stories") {
		t.Errorf("advisory must still report the full count:\n%s", capped)
	}
}

// openDocStories must be silent rather than panicking when the store is absent —
// hook context is fail-open by contract.
func TestOpenDocStoriesToleratesAnUnwiredStore(t *testing.T) {
	if refs := openDocStories(nil); refs != nil {
		t.Errorf("nil app = %+v, want nil", refs)
	}
	if refs := openDocStories(&app.App{}); refs != nil {
		t.Errorf("app with no store = %+v, want nil", refs)
	}
	_ = workitem.KindStory
}
