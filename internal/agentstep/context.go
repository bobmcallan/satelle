package agentstep

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Read-only context sources (sty_12499ecc). A workflow step names the sources
// (agents.toml [context.<name>]) its agents may query; satelle hands them to every
// agent it dispatches for an edge entering that step — the performer, the route
// reviewers and the gate-table reviewers — and to no other. This file is the
// mechanism: resolve what a step offers, list it in the briefing, and filter what
// a reviewer reports it consulted. What a reviewer must DO with a source is a
// judging rule, and it lives in the satelle-reviewer-verdict principle, not here.

// ContextOffer is what one dispatch is offered: the resolved sources and the
// requirement references of the story it serves.
type ContextOffer struct {
	Servers    []agentcli.ContextServer
	References []config.Reference
}

// servers is the sources of an offer, nil-safe.
func (o *ContextOffer) servers() []agentcli.ContextServer {
	if o == nil {
		return nil
	}
	return o.Servers
}

// names lists the offered source names in declared order.
func (o *ContextOffer) names() []string {
	if o == nil || len(o.Servers) == 0 {
		return nil
	}
	return agentcli.ContextSourceNames(o.Servers)
}

// SetContextSources wires how a declared context source is looked up by name.
func (g *Engine) SetContextSources(fn func(name string) (agentcli.ContextServer, bool)) {
	g.contextSource = fn
}

// SetReferences wires how a story's requirement references are read off its tags.
func (g *Engine) SetReferences(fn func(tags []string) []config.Reference) { g.references = fn }

// contextOffer resolves what the dispatches for an edge entering toStatus are
// offered: the sources the governing route's step declares, and the story's
// references. A step that declares none — or an engine with no sources wired —
// offers nothing (nil), so every other dispatch is exactly what it was.
func (g *Engine) contextOffer(ctx context.Context, item workitem.Item, toStatus string) (*ContextOffer, error) {
	if g.contextSource == nil {
		return nil, nil
	}
	spec, _, err := g.activeSpec(ctx, item)
	if err != nil {
		if ungoverned(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, st := range spec.States {
		if st.Name == toStatus {
			names = st.Context
			break
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	offer := &ContextOffer{}
	for _, n := range names {
		srv, ok := g.contextSource(n)
		if !ok {
			return nil, fmt.Errorf("step %q declares context source %q but .satelle/workflows/agents.toml defines no [context.%s] — declare it, or drop it from the step", toStatus, n, n)
		}
		offer.Servers = append(offer.Servers, srv)
	}
	if g.references != nil {
		offer.References = g.references(item.Tags)
	}
	return offer, nil
}

// contextBriefing is the mechanism-only block a dispatch's prompt carries when it
// is offered sources: each source with its allowed tools, and the story's
// references. It gives no instruction; the rule for using them is the
// satelle-reviewer-verdict principle's. "" when nothing is offered.
func contextBriefing(o *ContextOffer) string {
	if o == nil || len(o.Servers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Context sources\n\n")
	b.WriteString("Read-only sources offered to this dispatch, each with the tools allowed on it:\n\n")
	for _, s := range o.Servers {
		fmt.Fprintf(&b, "- `%s` — tools: %s\n", s.Name, backticked(s.Tools))
	}
	if len(o.References) > 0 {
		b.WriteString("\nThe story's references:\n\n")
		for _, r := range o.References {
			fmt.Fprintf(&b, "- `%s` — system `%s`, key `%s`\n", r.Tag, r.System, r.Key)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func backticked(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = "`" + s + "`"
	}
	return strings.Join(q, ", ")
}

// filterConsulted keeps the reported lookups that went through an offered source
// and counts the rest, which are dropped: a reviewer can only have consulted what
// it was offered. Duplicates collapse.
func filterConsulted(reported []verb.ConsultedRef, offered []string) (kept []verb.ConsultedRef, dropped int) {
	ok := make(map[string]bool, len(offered))
	for _, n := range offered {
		ok[n] = true
	}
	seen := map[verb.ConsultedRef]bool{}
	for _, c := range reported {
		c.Source, c.Ref = strings.TrimSpace(c.Source), strings.TrimSpace(c.Ref)
		if !ok[c.Source] {
			dropped++
			continue
		}
		if !seen[c] {
			seen[c] = true
			kept = append(kept, c)
		}
	}
	return kept, dropped
}

// stampConsulted filters a decision's reported lookups to the offered sources.
func stampConsulted(d *verb.GateDecision, offered []string) {
	d.Consulted, d.ConsultedDropped = filterConsulted(d.Consulted, offered)
}
