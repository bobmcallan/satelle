package epicset

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

func story(id, category, parent string, tags ...string) workitem.Item {
	return workitem.Item{ID: id, Kind: workitem.KindStory, Status: "backlog", Category: category, ParentID: parent, Tags: tags}
}

func ids(items []workitem.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AC1: the set is every story carrying epic:<theme>; parent_id is not
// membership; a child keeps its own work category; a story with no epic: tag is
// in no set.
func TestFromItemsSetIsTheTag(t *testing.T) {
	parent := story("p", "epic-parent", "", "epic:t")
	items := []workitem.Item{
		parent,
		story("tagOnly", "fix", "", "epic:t"),
		story("both", "substrate", "p", "epic:t", "sprint:x"),
		story("linkOnly", "fix", "p"),
		story("other", "fix", "", "epic:u"),
		story("none", "docs", ""),
	}
	set, err := FromItems(items, parent)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tagOnly", "both"}; !equal(set.IDs(), want) {
		t.Errorf("children = %v, want %v (parent_id is not membership; no tag is no set)", set.IDs(), want)
	}
	if set.Theme != "epic:t" {
		t.Errorf("theme = %q", set.Theme)
	}
	for _, c := range set.Children {
		if c.Category == ParentCategory {
			t.Errorf("a child keeps its work category, got %q on %s", c.Category, c.ID)
		}
	}
}

// AC5: fails closed — no tag, two tags, or two epic-parents on one tag.
func TestFromItemsUndetermined(t *testing.T) {
	cases := map[string]struct {
		parent workitem.Item
		items  []workitem.Item
		want   string
	}{
		"no tag":    {story("p", "epic-parent", ""), nil, "no epic:<theme> tag"},
		"two tags":  {story("p", "epic-parent", "", "epic:a", "epic:b"), nil, "2 epic tags"},
		"two roots": {story("p", "epic-parent", "", "epic:t"), []workitem.Item{story("q", "epic-parent", "", "epic:t")}, "2 epic-parents carry epic:t: p, q"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := FromItems(append([]workitem.Item{c.parent}, c.items...), c.parent)
			if !errors.Is(err, ErrUndetermined) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want ErrUndetermined containing %q", err, c.want)
			}
		})
	}
}

// The parent being closed appearing in the listing is not a second epic-parent.
func TestFromItemsParentInListingIsDetermined(t *testing.T) {
	parent := story("p", "epic-parent", "", "epic:t")
	if _, err := FromItems([]workitem.Item{parent, parent}, parent); err != nil {
		t.Errorf("the closing parent listed once or twice is one parent: %v", err)
	}
}

// A non-epic container keeps its parent_id links; the epic: tag set is not its
// membership.
func TestMembersNonEpicContainerKeepsParentLinks(t *testing.T) {
	container := story("c", "parent", "")
	items := []workitem.Item{
		container,
		story("kid", "fix", "c"),
		story("tagged", "fix", "", "epic:t"),
	}
	got, err := MembersFromItems(items, container)
	if err != nil || !equal(ids(got), []string{"kid"}) {
		t.Errorf("members = %v err=%v, want [kid]", ids(got), err)
	}
}

type fakeLister struct{ items []workitem.Item }

func (f fakeLister) List(_ context.Context, fl workitem.ListFilter) ([]workitem.Item, error) {
	var out []workitem.Item
	for _, it := range f.items {
		if fl.Tag != "" {
			found := false
			for _, tg := range it.Tags {
				found = found || tg == fl.Tag
			}
			if !found {
				continue
			}
		}
		if fl.ParentID != "" && it.ParentID != fl.ParentID {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func TestResolveReadsTheListerEachCall(t *testing.T) {
	parent := story("p", "epic-parent", "", "epic:t")
	l := fakeLister{items: []workitem.Item{parent, story("a", "fix", "", "epic:t")}}
	set, err := Resolve(context.Background(), l, parent)
	if err != nil || !equal(set.IDs(), []string{"a"}) {
		t.Fatalf("set = %v err=%v", set.IDs(), err)
	}
	l.items = append(l.items, story("late", "fix", "", "epic:t"))
	set, _ = Resolve(context.Background(), l, parent)
	if !equal(set.IDs(), []string{"a", "late"}) {
		t.Errorf("a re-read must see the member filed since: %v", set.IDs())
	}
	members, err := Members(context.Background(), l, story("c", "parent", ""))
	if err != nil || len(members) != 0 {
		t.Errorf("non-epic container with no parent_id links: %v err=%v", ids(members), err)
	}
}

func TestParentOf(t *testing.T) {
	l := fakeLister{items: []workitem.Item{story("p", "epic-parent", "", "epic:t"), story("a", "fix", "", "epic:t")}}
	got, found, err := ParentOf(context.Background(), l, "epic:t")
	if err != nil || !found || got.ID != "p" {
		t.Errorf("parent = %v found=%v err=%v", got.ID, found, err)
	}
	if _, found, _ := ParentOf(context.Background(), l, "epic:none"); found {
		t.Error("a theme with no epic-parent has no container")
	}
	l.items = append(l.items, story("q", "epic-parent", "", "epic:t"))
	if _, _, err := ParentOf(context.Background(), l, "epic:t"); !errors.Is(err, ErrUndetermined) {
		t.Errorf("two epic-parents: err = %v, want ErrUndetermined", err)
	}
}
