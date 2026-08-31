// Package docstory is the read-back half of the indexer's auto-raise: when an
// authored document fails its deterministic structure check, `satelle reindex`
// files a high-priority system story naming the exact file and fault and tags it
// to that document. Until now nothing read that tag back, so the diagnosis sat
// in backlog while the operator hunted the symptom (sty_88d40a60).
//
// This package holds the WIRE FORMAT of that relationship — the `doc:` tag, the
// marker tag, the priority — and the one predicate that says which stories
// qualify to be surfaced. Both the writer (reindex) and the readers (session
// start, gate refusals) go through it, so the two halves cannot drift apart:
// a drift would fail silently, in exactly the direction this package exists to
// fix.
//
// Mechanism only. It renders nothing and decides no verdict — it answers "is
// there an open story about this document, and what is its id".
//
// Leaf by construction: it depends on workitem for the item shape and on nothing
// else, so agentstep and the CLI can both use it without gaining a store
// dependency (callers inject a list function).
package docstory

import (
	"context"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/workitem"
)

const (
	// MarkerTag marks a story the indexer raised about a failing document, as
	// opposed to one a person wrote. Written by reindex, read here.
	MarkerTag = "type:system"
	// Priority is the priority such a story is filed at. A broken governing
	// document stops work, so it is filed high and only high qualifies —
	// surfacing every system story would be the noise this bound exists to
	// prevent.
	Priority = "high"
	// tagPrefix opens the document-relationship tag: doc:<kind>/<name>.
	tagPrefix = "doc:"
)

// Tag is the dedup key tying a system story to the document it tracks.
//
// It is a WIRE FORMAT: stories already filed in live repos carry it, and reindex
// dedups against it. Changing the string would re-file a story for every
// document already tracked, so it changes only with a migration.
func Tag(kind, name string) string { return tagPrefix + kind + "/" + name }

// Ref is one open story tracking a failing document.
type Ref struct {
	ID    string // story id, the thing a surface must name
	Kind  string // document kind, e.g. "workflows"
	Name  string // document name, e.g. "done"
	Title string
}

// Doc renders the document this story tracks, as the tag spells it.
func (r Ref) Doc() string { return r.Kind + "/" + r.Name }

// Lister is the store read this package needs, injected so no consumer gains a
// store dependency it does not otherwise have.
type Lister func(context.Context, workitem.ListFilter) ([]workitem.Item, error)

// Qualifies reports whether it is an open story about a failing document — the
// ONE definition of what may be surfaced, stated here so every surface bounds
// itself identically and a repo with no such story gains no output at all.
//
// All five must hold:
//
//   - it is a story (a task carries no lifecycle to be blocked by),
//   - its status is non-terminal (a done or cancelled diagnosis is history),
//   - its priority is Priority (high) — the level reindex files these at,
//   - it carries MarkerTag, so it is the indexer's own record, and
//   - it carries a doc: tag, the DOCUMENT RELATIONSHIP. A system story about
//     nothing in particular is not surfaceable: without the document there is
//     no refusal to attach it to and no file for the operator to open.
func Qualifies(it workitem.Item) bool {
	if it.Kind != workitem.KindStory {
		return false
	}
	if it.Status == workitem.StatusDone || it.Status == "cancelled" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(it.Priority), Priority) {
		return false
	}
	marker, doc := false, false
	for _, t := range it.Tags {
		switch {
		case t == MarkerTag:
			marker = true
		case strings.HasPrefix(t, tagPrefix) && docRefOf(t) != (Ref{}):
			doc = true
		}
	}
	return marker && doc
}

// docRefOf parses kind/name out of a doc: tag. The zero Ref means the tag is not
// a well-formed document relationship.
func docRefOf(tag string) Ref {
	rest, ok := strings.CutPrefix(tag, tagPrefix)
	if !ok {
		return Ref{}
	}
	kind, name, ok := strings.Cut(rest, "/")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(name) == "" {
		return Ref{}
	}
	return Ref{Kind: kind, Name: name}
}

// Open returns every qualifying story in stable id order, so a surface that caps
// what it names renders the same ids on every run rather than reshuffling with
// the store's update order. A nil lister or a list error yields no refs and no
// error: every caller is an advisory surface that must stay silent rather than
// fail, and the gate that must fail closed does so on its own grounds.
func Open(ctx context.Context, list Lister) []Ref {
	if list == nil {
		return nil
	}
	items, err := list(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: 2000})
	if err != nil {
		return nil
	}
	var out []Ref
	for _, it := range items {
		if !Qualifies(it) {
			continue
		}
		for _, t := range it.Tags {
			ref := docRefOf(t)
			if ref == (Ref{}) {
				continue
			}
			ref.ID, ref.Title = it.ID, it.Title
			out = append(out, ref)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ForDoc returns the open story tracking one document, if any. Used by a refusal
// that names the document it could not parse, so the operator is handed the
// diagnosis instead of being left to find it.
func ForDoc(refs []Ref, kind, name string) (Ref, bool) {
	for _, r := range refs {
		if r.Kind == kind && r.Name == name {
			return r, true
		}
	}
	return Ref{}, false
}

// IDForDoc is ForDoc's id, or "" — the shape a resolver injected into a
// store-free package hands back.
func IDForDoc(refs []Ref, kind, name string) string {
	if r, ok := ForDoc(refs, kind, name); ok {
		return r.ID
	}
	return ""
}
