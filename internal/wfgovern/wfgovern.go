// Package wfgovern is the leaf package for workflow selection: which workflow
// governs a work item (stamp, then category/kind priority). Split out of
// agentstep so verb and the edit-gate hooks can resolve governance without an
// import cycle (agentstep imports verb; verb must not import agentstep).
//
// DOT parsing stays in wfdot; this package only picks among authored docs.
package wfgovern

import (
	"strings"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// WorkflowStampPrefix is the tag prefix that STAMPS the governing workflow on a
// story at create (sty_3800ac23): `workflow:<name>`. Recorded once, so gating
// reads the chosen workflow rather than re-deriving it by category every time.
const WorkflowStampPrefix = "workflow:"

// StampedWorkflowName returns the workflow stamped on the item (its
// `workflow:<name>` tag), or "" when un-stamped (legacy/category-resolved).
func StampedWorkflowName(item workitem.Item) string {
	for _, t := range item.Tags {
		if strings.HasPrefix(t, WorkflowStampPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(t, WorkflowStampPrefix))
		}
	}
	return ""
}

// WorkflowCategory returns the key used to resolve an item's governing workflow.
// A story resolves by its authored category; an EXECUTION resolves by its KIND
// ("execution"), so a task-execution workflow (applies_to:["execution"]) governs
// runs without depending on a per-item category, and an execution never falls
// through to the wildcard STORY workflow (sty_ef08ce2a). A TASK header resolves
// by its kind too (sty_3c1a2a9d).
func WorkflowCategory(item workitem.Item) string {
	switch item.Kind {
	case workitem.KindExecution, workitem.KindTask:
		return string(item.Kind)
	}
	return item.Category
}

// OrderedWorkflows returns the workflows that APPLY to a story of the given
// category, ordered by selection priority (highest first) — the list satelle
// offers an agent starting a story, where the head is the active/default choice
// and the engine enforces. A workflow applies when its `applies_to` lists the
// category or the wildcard "*". Priority tiers, in order:
//
//  1. category-specific match on a PROJECT (repo) workflow,
//  2. category-specific match on a SYSTEM (embedded) workflow,
//  3. wildcard ("*") PROJECT workflow,
//  4. wildcard SYSTEM workflow.
//
// So a repo's project workflow overrides the embedded system default, and a
// category-specific workflow overrides a wildcard one. Within a tier, input
// order (name-sorted, as the doc index yields) is preserved.
func OrderedWorkflows(workflows []docindex.Doc, category string) []docindex.Doc {
	var specRepo, specSys, wildRepo, wildSys []docindex.Doc
	for _, w := range workflows {
		at := FrontmatterList(w.Body, "applies_to")
		switch {
		case category != "" && containsStr(at, category):
			if w.Embedded {
				specSys = append(specSys, w)
			} else {
				specRepo = append(specRepo, w)
			}
		case containsStr(at, "*"):
			if w.Embedded {
				wildSys = append(wildSys, w)
			} else {
				wildRepo = append(wildRepo, w)
			}
		}
	}
	out := make([]docindex.Doc, 0, len(workflows))
	out = append(out, specRepo...)
	out = append(out, specSys...)
	out = append(out, wildRepo...)
	out = append(out, wildSys...)
	return out
}

// GoverningWorkflow resolves the workflow that governs item from an already-listed
// set: the stamped `workflow:<name>` if present and still in the set, else the
// highest-priority workflow applicable to the item's category (OrderedWorkflows).
// Returns false when no workflow applies.
func GoverningWorkflow(workflows []docindex.Doc, item workitem.Item) (docindex.Doc, bool) {
	if name := StampedWorkflowName(item); name != "" {
		for _, w := range workflows {
			if w.Name == name {
				return w, true
			}
		}
	}
	if ordered := OrderedWorkflows(workflows, WorkflowCategory(item)); len(ordered) > 0 {
		return ordered[0], true
	}
	return docindex.Doc{}, false
}

// FrontmatterList parses a list-valued key from a markdown frontmatter block,
// handling both the inline flow form (`applies_to: ["*", "web"]`) and the block
// list form (`applies_to:` then `- web` lines). Returns nil when absent.
func FrontmatterList(body, key string) []string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	end := -1
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			end = j
			break
		}
	}
	if end < 0 {
		return nil
	}
	for i := 1; i < end; i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, key+":") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, key+":"))
		if strings.HasPrefix(rest, "[") { // inline flow form
			rest = strings.TrimSuffix(strings.TrimPrefix(rest, "["), "]")
			return splitTrimList(rest)
		}
		var out []string // block list form
		for j := i + 1; j < end; j++ {
			l2 := strings.TrimSpace(lines[j])
			if l2 == "" {
				continue
			}
			if strings.HasPrefix(l2, "- ") {
				out = append(out, strings.Trim(strings.TrimSpace(l2[2:]), `"'`))
				continue
			}
			break
		}
		return out
	}
	return nil
}

// splitTrimList splits a comma-separated inline list, trimming whitespace and
// surrounding quotes, dropping empties.
func splitTrimList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		v := strings.Trim(strings.TrimSpace(p), `"'`)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
