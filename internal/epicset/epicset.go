// Package epicset is the one owner of container membership (sty_9f4f8e12).
//
// An epic is a THEME, not a parent link: the set is every story carrying
// epic:<theme>. The parent is the single story in that set whose category is
// epic-parent, and it carries the same tag. Every other story in the set is a
// child and keeps its own work category. parent_id is NOT membership for an
// epic, and a story with no epic: tag is in no epic's set. A container that is
// not an epic-parent keeps its parent_id links.
//
// Every surface that reports or checks a container's members — the close gate's
// payload, the commit-time guard, the web story view, the CLI route view, the
// workspace aggregate — calls this package, so they cannot disagree about who is
// in the set. The package is mechanism only: it enumerates a set, it never
// judges whether the set may close.
package epicset

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/workitem"
)

const (
	// ParentCategory is the category of an epic's one parent story.
	ParentCategory = "epic-parent"
	// TagPrefix prefixes the theme tag that makes a story a member of an epic.
	TagPrefix = "epic:"
)

// ErrUndetermined marks an epic whose set cannot be fixed: the parent has no
// (or more than one) epic:<theme> tag, or more than one epic-parent carries the
// tag. A close over such a set must refuse; the wrapped message says why.
var ErrUndetermined = errors.New("epic set undetermined")

// Lister is the slice of the work-item store the resolver reads.
type Lister interface {
	List(ctx context.Context, f workitem.ListFilter) ([]workitem.Item, error)
}

// Set is one epic's membership: the parent and every other story carrying the
// theme tag.
type Set struct {
	Theme    string
	Parent   workitem.Item
	Children []workitem.Item
}

// IDs returns the child ids in set order.
func (s Set) IDs() []string {
	ids := make([]string, 0, len(s.Children))
	for _, c := range s.Children {
		ids = append(ids, c.ID)
	}
	return ids
}

// IsEpicParent reports whether item is an epic's parent container.
func IsEpicParent(item workitem.Item) bool {
	return strings.EqualFold(strings.TrimSpace(item.Category), ParentCategory)
}

// Themes returns the epic: tags an item carries, tag prefix included.
func Themes(item workitem.Item) []string {
	var out []string
	for _, t := range item.Tags {
		if strings.HasPrefix(t, TagPrefix) && len(t) > len(TagPrefix) {
			out = append(out, t)
		}
	}
	return out
}

// themeOf fixes the one epic:<theme> tag of an epic-parent.
func themeOf(parent workitem.Item) (string, error) {
	themes := Themes(parent)
	switch len(themes) {
	case 1:
		return themes[0], nil
	case 0:
		return "", fmt.Errorf("%w: epic-parent %s has no %s<theme> tag", ErrUndetermined, parent.ID, TagPrefix)
	default:
		return "", fmt.Errorf("%w: epic-parent %s carries %d epic tags (%s) — exactly one names its set",
			ErrUndetermined, parent.ID, len(themes), strings.Join(themes, ", "))
	}
}

func hasTag(item workitem.Item, tag string) bool {
	for _, t := range item.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// FromItems fixes parent's epic set from items — the pure core, no store. items
// need not be pre-filtered; only stories carrying the theme tag count.
func FromItems(items []workitem.Item, parent workitem.Item) (Set, error) {
	tag, err := themeOf(parent)
	if err != nil {
		return Set{}, err
	}
	parents := []string{parent.ID}
	var children []workitem.Item
	for _, it := range items {
		if it.ID == parent.ID || it.Kind != workitem.KindStory || !hasTag(it, tag) {
			continue
		}
		if IsEpicParent(it) {
			parents = append(parents, it.ID)
			continue
		}
		children = append(children, it)
	}
	if len(parents) > 1 {
		return Set{}, fmt.Errorf("%w: %d epic-parents carry %s: %s",
			ErrUndetermined, len(parents), tag, strings.Join(parents, ", "))
	}
	return Set{Theme: tag, Parent: parent, Children: children}, nil
}

// Resolve fixes parent's epic set by reading the store now — the re-read a
// close must make rather than trusting an earlier snapshot.
func Resolve(ctx context.Context, l Lister, parent workitem.Item) (Set, error) {
	tag, err := themeOf(parent)
	if err != nil {
		return Set{}, err
	}
	items, err := l.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Tag: tag, Limit: 2000})
	if err != nil {
		return Set{}, fmt.Errorf("epicset: list %s: %w", tag, err)
	}
	return FromItems(items, parent)
}

// MembersFromItems returns the stories belonging to container among items: the
// epic set for an epic-parent, the parent_id links for any other container.
func MembersFromItems(items []workitem.Item, container workitem.Item) ([]workitem.Item, error) {
	if IsEpicParent(container) {
		set, err := FromItems(items, container)
		return set.Children, err
	}
	var out []workitem.Item
	for _, it := range items {
		if it.ParentID == container.ID {
			out = append(out, it)
		}
	}
	return out, nil
}

// Members is MembersFromItems read from the store.
func Members(ctx context.Context, l Lister, container workitem.Item) ([]workitem.Item, error) {
	if IsEpicParent(container) {
		set, err := Resolve(ctx, l, container)
		return set.Children, err
	}
	return l.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, ParentID: container.ID, Limit: 2000})
}

// Ref is one member as every surface reports it: id and status.
type Ref struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Refs reduces members to the id+status every surface shows.
func Refs(members []workitem.Item) []Ref {
	out := make([]Ref, 0, len(members))
	for _, m := range members {
		out = append(out, Ref{ID: m.ID, Status: m.Status})
	}
	return out
}

// ParentOf returns the epic-parent for theme (an epic:<theme> tag). found is
// false when no story in the set is an epic-parent; more than one is
// ErrUndetermined.
func ParentOf(ctx context.Context, l Lister, theme string) (parent workitem.Item, found bool, err error) {
	items, err := l.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Tag: theme, Limit: 2000})
	if err != nil {
		return workitem.Item{}, false, fmt.Errorf("epicset: list %s: %w", theme, err)
	}
	var ids []string
	for _, it := range items {
		if IsEpicParent(it) && hasTag(it, theme) {
			parent = it
			ids = append(ids, it.ID)
		}
	}
	switch len(ids) {
	case 0:
		return workitem.Item{}, false, nil
	case 1:
		return parent, true, nil
	}
	return workitem.Item{}, false, fmt.Errorf("%w: %d epic-parents carry %s: %s", ErrUndetermined, len(ids), theme, strings.Join(ids, ", "))
}
