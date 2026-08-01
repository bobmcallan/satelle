package web

import (
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfroute"
)

// workflowRowVM is one workflow in the Workflow panel list — the row the user
// filters and clicks to expand. Mirrors the stories/tasks row shape so the same
// filter + expand/collapse interactions apply (the tab is read-only).
type workflowRowVM struct {
	Name       string
	Headline   string
	Scope      string
	AppliesTo  []string
	Updated    time.Time
	Provenance string // default | edited | authored (sty_ba0eb5c6)
	Source     string
}

// routeSourceDone and routeSourceStep name the two authored bodies a DERIVED
// route is built from: a declaration of done and a step catalogue, indexed under
// the `workflows` kind like any other authored workflow doc. When both are
// present the panel derives the route from them; otherwise it falls back to the
// authored DOT, which is how this story lands before the workflows convert
// (sty_9835070d) and after wfdot.Parse retires (sty_d953c5d8).
const (
	routeSourceDone = "done"
	routeSourceStep = "step"
)

// routeSource carries the two authored bodies, empty when the substrate holds no
// derived route yet.
type routeSource struct {
	Done string
	Step string
}

func (rs routeSource) ok() bool { return rs.Done != "" && rs.Step != "" }

// routeSourceOf picks the declaration of done and the step catalogue out of the
// indexed workflow docs.
func routeSourceOf(docs []docindex.Doc) routeSource {
	var rs routeSource
	for _, d := range docs {
		switch d.Name {
		case routeSourceDone:
			rs.Done = d.Body
		case routeSourceStep:
			rs.Step = d.Body
		}
	}
	return rs
}

// isRouteSourceDoc reports whether a workflow doc is one half of a derived
// route rather than a workflow in its own right — it has no lifecycle of its
// own and must not be resolved as one.
func isRouteSourceDoc(name string) bool {
	return name == routeSourceDone || name == routeSourceStep
}

// workflowSpec resolves a workflow's lifecycle WITHOUT parsing anything here:
// both branches are calls into internal/wfdot, the one package that owns a
// workflow front door. A derived route (done.md + step.md) wins when the
// substrate carries one; an authored DOT body is the fallback. ok is false when
// neither yields a lifecycle, and the caller then shows an empty route rather
// than a wrong one.
func workflowSpec(body string, rs routeSource, category string, tags []string) (wfdot.Spec, []wfroute.Advisor, bool) {
	if rs.ok() {
		lists, err := wfdot.ParseDone(rs.Done)
		if err != nil {
			return wfdot.Spec{}, nil, false
		}
		cat, err := wfdot.ParseSteps(rs.Step)
		if err != nil {
			return wfdot.Spec{}, nil, false
		}
		for _, l := range lists {
			if category != "" && l.Category != category {
				continue
			}
			spec, err := wfdot.BuildRoute(l, cat, tags)
			if err != nil {
				return wfdot.Spec{}, nil, false
			}
			return spec, wfroute.AdvisorsFrom(l, cat), true
		}
		return wfdot.Spec{}, nil, false
	}
	// An authored DOT names no advisor: entry dispatch is retired, so the graph
	// has no attribute that could carry one (sty_05a5e203).
	spec, ok := wfdot.Parse(body)
	return spec, nil, ok
}

// workflowRoute resolves a workflow doc all the way to the route the panel
// renders. Empty (len(Steps)==0) when no lifecycle resolves.
func workflowRoute(name, body string, rs routeSource, category string, tags []string) wfroute.Route {
	spec, advisors, ok := workflowSpec(body, rs, category, tags)
	if !ok {
		return wfroute.Route{Workflow: name}
	}
	return wfroute.Build(spec, name, tags, advisors)
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
// The two halves of a derived route are not workflows and get no row.
func workflowRows(docs []docindex.Doc, prov, src map[string]string) []workflowRowVM {
	out := make([]workflowRowVM, 0, len(docs))
	for _, d := range docs {
		if isRouteSourceDoc(d.Name) {
			continue
		}
		key := "workflows\x00" + d.Name
		out = append(out, workflowRowVM{
			Name:       d.Name,
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
