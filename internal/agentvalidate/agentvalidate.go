// Package agentvalidate is the store-free, deterministic check of the agents
// layer (.satelle/agents.toml) and each workflow's agent= node bindings.
//
// It is the SINGLE authority three callers share (sty_93eec36d):
//   - `satelle agent validate` (standalone, on-demand)
//   - `satelle init` deployment validation
//   - story engagement (leaving the workflow entry state for a non-cancel target)
//
// It deliberately does NOT re-implement structure.Doc (performing-node rubrics)
// or agentstep.WorkflowConsistency (unresolved gate skills / ambiguous
// applies_to) — those stay owned by their existing checks. This package adds
// only: every binding's command/timeout/env resolves, each agent's resolved
// grant is inspectable, every agent=<name> node has a matching binding, and
// orphaned named bindings are flagged.
package agentvalidate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// Grant is one agent's resolved, inspectable capability surface — what validate
// surfaces so a preset's baked grant is visible without knowing the expansion.
// Env VALUES are never included (secrets); key names may appear in Notes.
type Grant struct {
	Name              string
	Backend           string // in-loop | isolated:claude | isolated:grok | isolated:<binary> | codex (unmapped)
	Tools             string
	Model             string
	Timeout           string
	ReadOnly          bool
	InjectsPrinciples bool
	Notes             string // non-secret notes (e.g. env key names, command ceiling hints)
}

// Report is the structured result of Validate.
// Problems are hard failures (non-zero exit / engage refuse).
// Warnings are advisory (e.g. orphaned named bindings that may still be used by
// non-workflow verbs like `story retrospect`) — printed, not failed.
type Report struct {
	Problems []string
	Warnings []string
	Grants   []Grant
}

// OK reports whether the report carries no hard problems.
func (r Report) OK() bool { return len(r.Problems) == 0 }

// Validate checks every agents.toml binding and each workflow's agent= node
// allocations. vars is the [vars] KV used to resolve ${VAR} in binding env/
// settings (may be nil). workflows may be empty (agent-only check).
func Validate(agents config.AgentsConfig, vars map[string]string, workflows []docindex.Doc) Report {
	var r Report

	// Env/settings resolution once — fail-fast naming section+key, never values.
	if _, err := config.ResolveAgentEnvs(agents, vars); err != nil {
		r.Problems = append(r.Problems, err.Error())
	}

	// Built-in roles first, then named agents in sorted order.
	type named struct {
		name string
		b    config.AgentBinding
	}
	sections := []named{
		{"executor", agents.ExecutorBinding()},
		{"reviewer", agents.ReviewerBinding()},
	}
	for _, name := range sortedNames(agents.Agents) {
		b, _ := agents.NamedBinding(name)
		sections = append(sections, named{name, b})
	}

	for _, sec := range sections {
		g, problems := checkBinding(sec.name, sec.b)
		r.Grants = append(r.Grants, g)
		r.Problems = append(r.Problems, problems...)
	}

	// Workflow node → binding + orphan named bindings.
	usedNamed := map[string]bool{}
	for _, doc := range workflows {
		spec, ok := wfdot.Parse(doc.Body)
		if !ok {
			continue // structure.Doc / workflow validate owns unparseable bodies
		}
		for _, st := range spec.States {
			if st.Agent == "" || st.Agent == "executor" || st.Agent == "reviewer" {
				continue
			}
			usedNamed[st.Agent] = true
			if _, ok := agents.NamedBinding(st.Agent); !ok {
				r.Problems = append(r.Problems, fmt.Sprintf(
					"workflow %q node %q allocates agent=%s with no [%s] binding in agents.toml",
					doc.Name, st.Name, st.Agent, st.Agent))
			}
		}
	}
	for _, name := range sortedNames(agents.Agents) {
		if !usedNamed[name] {
			// Advisory only: a binding may serve a non-workflow verb (e.g.
			// [retrospective] for `satelle story retrospect`) without an agent=
			// node. The satelle-workflow-drift skill judges semantics; validate
			// surfaces the orphan without blocking engage/init.
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"agents.toml [%s] is orphaned — no workflow node allocates agent=%s (ok if used by a non-workflow verb)",
				name, name))
		}
	}
	return r
}

// checkBinding validates one binding and builds its Grant.
func checkBinding(section string, b config.AgentBinding) (Grant, []string) {
	var problems []string
	cmd := b.CommandTemplate()
	if cmd == "" {
		// Should not happen after *Binding resolvers, but be defensive.
		if section == "executor" {
			cmd = config.DefaultExecutorCommand
		} else {
			cmd = config.DefaultReviewerCommand
		}
	}

	g := Grant{
		Name:              section,
		Tools:             b.Tools,
		Model:             b.Model,
		Timeout:           b.Timeout,
		InjectsPrinciples: b.InjectsPrinciples(),
	}
	if len(b.Env) > 0 {
		keys := make([]string, 0, len(b.Env))
		for k := range b.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		g.Notes = "env keys: " + strings.Join(keys, ",")
	}

	fields := strings.Fields(cmd)
	lower0 := ""
	if len(fields) > 0 {
		lower0 = strings.ToLower(fields[0])
	}

	switch {
	case len(fields) == 0 || lower0 == "in-loop":
		g.Backend = "in-loop"
		g.ReadOnly = false
		if g.Notes == "" {
			g.Notes = "full session grant (driving agent)"
		} else {
			g.Notes += "; full session grant (driving agent)"
		}
	case len(fields) == 1 && lower0 == agentcli.CLICodex:
		// Bare codex preset is selectable but Run is a stub — flag it.
		g.Backend = "codex (unmapped)"
		g.ReadOnly = false
		problems = append(problems, fmt.Sprintf(
			"agents.toml [%s] command: the codex preset is not yet mapped — use a full command template, or the claude/grok preset",
			section))
	default:
		runner, err := agentcli.RunnerFromCommand(cmd)
		if err != nil {
			problems = append(problems, fmt.Sprintf("agents.toml [%s] command: %v", section, err))
			g.Backend = "invalid"
			break
		}
		if runner == nil {
			g.Backend = "in-loop"
			break
		}
		// Isolated runner — classify backend + read-only ceiling.
		name := runner.Name()
		g.Backend = "isolated:" + name
		resolved := runner.Command()
		switch strings.ToLower(name) {
		case agentcli.CLIClaude:
			g.ReadOnly = strings.Contains(resolved, "--disallowedTools")
			if b.Tools == "" {
				g.Tools = config.DefaultReviewerTools
			}
		case agentcli.CLIGrok:
			// Grok preset bakes read-only tools + --deny mutators.
			g.ReadOnly = strings.Contains(resolved, "--deny") || strings.Contains(resolved, "read_file")
			if b.Tools == "" && strings.Contains(resolved, "read_file") {
				g.Tools = "read_file,grep,list_dir"
			}
		default:
			// Full template: surface the command so the ceiling is visible.
			g.ReadOnly = strings.Contains(resolved, "--disallowedTools") ||
				strings.Contains(resolved, "--deny") ||
				(strings.Contains(resolved, "Read") && !strings.Contains(resolved, "Write"))
		}
		if g.Notes == "" {
			g.Notes = "command: " + resolved
		} else {
			g.Notes += "; command: " + resolved
		}
	}

	if _, err := b.TimeoutDuration(0); err != nil {
		problems = append(problems, fmt.Sprintf("agents.toml [%s] timeout: %v", section, err))
	}
	return g, problems
}

func sortedNames(m map[string]config.AgentBinding) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
