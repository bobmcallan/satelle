package web

import (
	"net/http"
	"strings"

	"github.com/bobmcallan/satelle/internal/docindex"
)

// workflowRowVM is one workflow in the Workflow panel list — the row the user
// filters and clicks to expand. Mirrors the stories/tasks row shape so the same
// filter + expand/collapse interactions apply (the tab is read-only).
type workflowRowVM struct {
	Name      string
	Headline  string
	Scope     string
	AppliesTo []string
}

// wfState is a workflow state node. Terminal is true when no transition leaves it.
type wfState struct {
	Name     string
	Actor    string
	Terminal bool
}

// wfTransition is a directed edge between states; Skill is its reviewer gate
// (empty = ungated/advisory).
type wfTransition struct {
	From  string
	To    string
	Skill string
}

// wfSpec is the parsed lifecycle of a workflow: its states and gated transitions.
type wfSpec struct {
	States      []wfState
	Transitions []wfTransition
}

// workflowDetailVM backs the inline expand: the parsed diagram + the applies_to
// binding + the raw definition (frontmatter stripped) for the read-only view.
type workflowDetailVM struct {
	Name      string
	Headline  string
	Scope     string
	AppliesTo []string
	Spec      wfSpec
	Body      string
}

// workflowRows builds the Workflow panel rows from the indexed workflow docs.
func workflowRows(docs []docindex.Doc) []workflowRowVM {
	out := make([]workflowRowVM, 0, len(docs))
	for _, d := range docs {
		out = append(out, workflowRowVM{
			Name:      d.Name,
			Headline:  d.Headline,
			Scope:     workflowScope(d),
			AppliesTo: frontmatterList(d.Body, "applies_to"),
		})
	}
	return out
}

// workflowFragment renders one workflow's diagram inline (the expand target).
func workflowFragment() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		doc, err := fetchOne[docindex.Doc](r.Context(), "doc-get", map[string]any{"kind": "workflows", "name": name})
		if err != nil || doc.Name == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		render(w, "workflowDetail", workflowDetailVM{
			Name:      doc.Name,
			Headline:  doc.Headline,
			Scope:     workflowScope(doc),
			AppliesTo: frontmatterList(doc.Body, "applies_to"),
			Spec:      parseWorkflow(doc.Body),
			Body:      strings.TrimSpace(stripDocFrontmatter(doc.Body)),
		})
	}
}

// parseWorkflow extracts the states and transitions from a workflow markdown
// body. States come from the `states:` YAML block (a bare name or an inline
// `{name:…, actor:…}` map); transitions are every `- {from:…, to:…}` line
// anywhere in the body (so the guardrails block is ignored). Terminal states are
// those no transition leaves. When there is no states block, states are derived
// from the transition endpoints.
func parseWorkflow(body string) wfSpec {
	lines := strings.Split(body, "\n")
	var spec wfSpec

	for i, raw := range lines {
		if strings.TrimSpace(raw) != "states:" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			if !strings.HasPrefix(t, "- ") {
				break // end of the states block (e.g. `transitions:`)
			}
			spec.States = append(spec.States, parseState(strings.TrimSpace(t[2:])))
		}
		break
	}

	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if !strings.HasPrefix(t, "- {") || !strings.Contains(t, "from:") || !strings.Contains(t, "to:") {
			continue
		}
		spec.Transitions = append(spec.Transitions, wfTransition{
			From:  inlineField(t, "from"),
			To:    inlineField(t, "to"),
			Skill: inlineField(t, "reviewer_skill"),
		})
	}

	if len(spec.States) == 0 {
		seen := map[string]bool{}
		add := func(n string) {
			if n != "" && !seen[n] {
				seen[n] = true
				spec.States = append(spec.States, wfState{Name: n})
			}
		}
		for _, tr := range spec.Transitions {
			add(tr.From)
			add(tr.To)
		}
	}

	froms := map[string]bool{}
	for _, tr := range spec.Transitions {
		froms[tr.From] = true
	}
	for i := range spec.States {
		spec.States[i].Terminal = !froms[spec.States[i].Name]
	}
	return spec
}

// parseState parses one `states:` list item — a bare name or `{name:…, actor:…}`.
func parseState(item string) wfState {
	if strings.HasPrefix(item, "{") {
		return wfState{Name: inlineField(item, "name"), Actor: inlineField(item, "actor")}
	}
	return wfState{Name: strings.Trim(item, `"'`)}
}

// inlineField extracts key's value from a YAML inline-map line, trimming quotes;
// the value runs to the next comma or closing brace. (Same shape the reviewer
// uses to read transition lines.)
func inlineField(line, key string) string {
	i := strings.Index(line, key+":")
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(line[i+len(key)+1:], " ")
	if end := strings.IndexAny(rest, ",}"); end >= 0 {
		rest = rest[:end]
	}
	return strings.Trim(strings.TrimSpace(rest), `"`)
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
