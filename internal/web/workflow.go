package web

import (
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/wfroute"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// workflowRowVM is one workflow in the Workflow panel list — the row the user
// filters and clicks to expand. Mirrors the stories/tasks row shape so the same
// filter + expand/collapse interactions apply (the tab is read-only).
type workflowRowVM struct {
	Name string
	// ExpandName is the DOC the expand fragment loads. It equals Name for an
	// authored workflow, and is the declaration of done for a derived route —
	// whose displayed Name ("done.md+step.md") names two files rather than one
	// doc, and so is not a URL the fragment handler could resolve.
	ExpandName string
	Headline   string
	Scope      string
	AppliesTo  []string
	Updated    time.Time
	Provenance string // default | edited | authored (sty_ba0eb5c6)
	Source     string
}

// workflowRoute resolves a workflow doc all the way to the route the panel
// renders, through the ONE front door (wfgovern.SpecFor): a derived route when
// the substrate carries done.md + step.md, the authored DOT until it does. The
// panel used to implement that precedence itself; a second copy of it is exactly
// the defect a single front door exists to prevent (sty_9835070d).
//
// The web layer is the one caller allowed to DEGRADE on the error: it renders a
// page, it does not gate a transition. It degrades by handling the error here —
// an empty route, which the template shows as an explicit empty state — never by
// the seam hiding it from callers that do gate.
func workflowRoute(workflows []docindex.Doc, doc docindex.Doc, category string, tags []string) wfroute.Route {
	// The panel asks about a WORKFLOW, not a story, so it synthesises the item
	// whose lifecycle it wants: the category the row claims, plus any tags the
	// caller is previewing.
	item := workitem.Item{Kind: workitem.KindStory, Category: category, Tags: tags}
	set := workflows
	if !wfgovern.RouteSourceOf(workflows).Present() {
		// No derived route: pin resolution to the row the user clicked rather than
		// re-running applies_to precedence, which would show a different workflow.
		set = []docindex.Doc{doc}
	}
	spec, name, advisors, err := wfgovern.SpecFor(set, item)
	if err != nil {
		return wfroute.Route{Workflow: doc.Name}
	}
	if wfgovern.IsRouteSource(doc.Name) {
		doc.Name = name
	}
	return wfroute.Build(spec, doc.Name, tags, advisors)
}

// workflowDetailVM backs the inline expand: the ROUTE the workflow prescribes
// (sty_085e1a5a) plus the applies_to binding and the raw definition
// (frontmatter stripped) for the read-only view. There is no diagram: the route
// — ordered steps with obligation, performer, rubrics and entry gates — is the
// artifact, and it is the same one `satelle story route` renders.
type workflowDetailVM struct {
	Name       string
	Headline   string
	Scope      string
	AppliesTo  []string
	Route      wfroute.Route
	Body       string
	Provenance string
	Source     string
}

// workflowRows builds the Workflow panel rows from the indexed workflow docs.
// prov maps "workflows\x00name" → provenance; src maps the same key → source.
//
// The two halves of a derived route are not workflows and get no row of their
// own. The ROUTE they build does get one, at the head: it is the repo's
// lifecycle, and a panel that listed nothing for a converted repo would hide the
// only lifecycle it has. The row names itself as `satelle workflow list` does
// and expands through done.md, the half the fragment resolves the route from.
func workflowRows(docs []docindex.Doc, prov, src map[string]string) []workflowRowVM {
	out := make([]workflowRowVM, 0, len(docs))
	if rs := wfgovern.RouteSourceOf(docs); rs.Present() {
		key := "workflows\x00" + wfgovern.RouteSourceDone
		row := workflowRowVM{
			Name:       wfgovern.DerivedRouteName,
			ExpandName: wfgovern.RouteSourceDone,
			Headline:   "derived route — done.md + step.md",
			AppliesTo:  wfgovern.RouteCategories(rs.Done),
			Provenance: prov[key],
			Source:     src[key],
		}
		for _, d := range docs {
			if d.Name == wfgovern.RouteSourceDone {
				row.Scope, row.Updated = workflowScope(d), d.ModTime
			}
		}
		out = append(out, row)
	}
	for _, d := range docs {
		if wfgovern.IsRouteSource(d.Name) {
			continue
		}
		key := "workflows\x00" + d.Name
		out = append(out, workflowRowVM{
			Name:       d.Name,
			ExpandName: d.Name,
			Headline:   d.Headline,
			Scope:      workflowScope(d),
			AppliesTo:  frontmatterList(d.Body, "applies_to"),
			Updated:    d.ModTime,
			Provenance: prov[key],
			Source:     src[key],
		})
	}
	return out
}

// panelCategory picks the category a workflow-panel route is derived for. The
// panel has no story, so it borrows the workflow's first non-wildcard
// applies_to; "" means "the first declaration of done", which is what a
// wildcard workflow should show.
func panelCategory(appliesTo []string) string {
	for _, a := range appliesTo {
		if a != "" && a != "*" {
			return a
		}
	}
	return ""
}

// frontmatterList parses a list-valued frontmatter key (inline `[a, b]` or a
// block `- a` list), returning nil when absent.
func frontmatterList(body, key string) []string {
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
		if strings.HasPrefix(rest, "[") {
			rest = strings.TrimSuffix(strings.TrimPrefix(rest, "["), "]")
			return splitTrimList(rest)
		}
		var out []string
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

// workflowScope returns a workflow doc's frontmatter scope, defaulting an
// embedded canonical default to "system" when it declares none.
func workflowScope(d docindex.Doc) string {
	for _, ln := range strings.Split(d.Body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "scope:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "scope:")), `"'`)
		}
	}
	if d.Embedded {
		return "system"
	}
	return ""
}

// stripDocFrontmatter returns body with any leading YAML frontmatter removed.
func stripDocFrontmatter(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return body
	}
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			return strings.TrimLeft(strings.Join(lines[j+1:], "\n"), "\n")
		}
	}
	return body
}
