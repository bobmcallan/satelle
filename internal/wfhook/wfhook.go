// Package wfhook parses a workflow's LIFECYCLE HOOK declarations — the
// operations that fire OUTSIDE the status graph (story creation today) and so
// cannot be expressed as a DOT node or edge (sty_ede16f51).
//
// Before this package, create review resolved a skill from `create_review:`
// frontmatter and then ran it against an EMPTY agent selector, which the engine
// silently resolved to `[reviewer]`. Nothing in the substrate could inspect or
// change that allocation and nothing validated it. A hook now declares BOTH the
// skill and the logical agent, so the choice is readable in the workflow file
// and checkable before it runs.
//
// The grammar is ONE generic declaration, not a per-operation shape:
//
//	hooks:
//	  - operation: create_review
//	    skill: satelle-story-create-review
//	    agent: strict-reviewer          # optional; defaults to reviewer
//
// The pre-existing scalar shorthand stays a first-class, documented form:
//
//	create_review: satelle-story-create-review
//
// Go's ONLY per-operation knowledge is the operations table below — which
// operations exist and which yield a verdict. No provider, model, effort,
// command, or tool grant is named here or anywhere on the hook path: execution
// configuration comes from the agents.toml section the hook names, resolved by
// the existing binding path. Adding a future lifecycle operation is a name in
// that table plus its call site — not a new parser, resolver, or validator.
//
// Near-leaf so agentstep, agentvalidate, structure and cli can all import it
// without a cycle. Its ONE dependency is docindex, itself a leaf, for reading
// frontmatter — and that is deliberate, not a slip. A workflow's frontmatter is
// now either a markdown `---` block or a TOML `[meta]` table (sty_81bb0dde), and
// a private scan here that only understood `---` would find no `create_review`
// in a TOML route source: the story-CREATE gate would stop firing, silently, on
// the engine path. One reader for one question, or the two drift apart.
package wfhook

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/docindex"
)

// OpCreateReview is the content/alignment review that runs when a work item is
// CREATED — the one lifecycle hook that exists today.
const OpCreateReview = "create_review"

// DefaultAgent is the logical agent a hook resolves to when it declares none:
// the repo's `[reviewer]` binding. It is a DOCUMENTED default with provenance,
// not the invisible empty-selector fallback it replaces.
const DefaultAgent = "reviewer"

// Hook sources — how the declaration was written, so a display surface can say
// where an allocation came from rather than presenting a default as a choice.
const (
	// SourceHooks is an explicit entry in the `hooks:` block.
	SourceHooks = "hooks"
	// SourceShorthand is the scalar `<operation>: <skill>` form.
	SourceShorthand = "shorthand"
)

// hookKey is the frontmatter key holding the block list.
const hookKey = "hooks"

// operations is Go's complete per-operation knowledge: the set of lifecycle
// operations and whether each yields an accept/reject VERDICT (as opposed to a
// future advisory or side-effecting hook). Verdict operations carry the extra
// requirements a gate does — role=reviewer, an isolated binding — which is why
// the distinction lives here rather than being inferred at each call site.
var operations = map[string]bool{
	OpCreateReview: true,
}

// Operations returns every known lifecycle operation, sorted.
func Operations() []string {
	out := make([]string, 0, len(operations))
	for op := range operations {
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}

// Known reports whether op is a lifecycle operation this binary understands.
func Known(op string) bool {
	_, ok := operations[strings.TrimSpace(op)]
	return ok
}

// IsVerdict reports whether op yields an accept/reject verdict. False for an
// unknown operation — an unrecognised declaration never silently acquires gate
// authority.
func IsVerdict(op string) bool {
	return operations[strings.TrimSpace(op)]
}

// Hook is one declared lifecycle-adjacent operation: which operation, which
// skill judges it, and which logical agent runs that skill.
type Hook struct {
	// Operation is the lifecycle operation (OpCreateReview, …). An unrecognised
	// value is CARRIED, not dropped, so validation can report it by name.
	Operation string
	// Skill is the rubric the operation runs. Resolution against the substrate
	// is the caller's job — this package does no doc lookup.
	Skill string
	// Agent is the agents.toml section that runs the skill. Never empty after
	// Parse: an omitted agent is filled with DefaultAgent, with Source recording
	// that it was defaulted rather than chosen.
	Agent string
	// AgentDeclared reports whether the workflow named the agent explicitly.
	// False means Agent is DefaultAgent by default, which a display surface
	// should say out loud.
	AgentDeclared bool
	// Verdict reports whether this operation yields an accept/reject verdict.
	Verdict bool
	// Source is SourceHooks or SourceShorthand.
	Source string
}

// Describe renders the allocation's provenance in one short phrase, e.g.
// `agent=reviewer (default, from create_review shorthand)`.
func (h Hook) Describe() string {
	switch {
	case h.AgentDeclared:
		return fmt.Sprintf("agent=%s (declared in %s)", h.Agent, h.Source)
	case h.Source == SourceShorthand:
		return fmt.Sprintf("agent=%s (default, from %s shorthand)", h.Agent, h.Operation)
	default:
		return fmt.Sprintf("agent=%s (default)", h.Agent)
	}
}

// Parse returns every lifecycle hook a workflow body declares, in operation
// order, along with any declaration problems. Problems are reported rather than
// returned as an error: a malformed hook is a substrate defect for a validator
// to surface, and dropping the whole set would hide the rest of the file.
//
// When an operation is declared BOTH ways, the explicit `hooks:` entry wins and
// the duplicate is reported — an ambiguous declaration must not resolve silently.
func Parse(body string) ([]Hook, []string) {
	fm, ok := frontmatter(body)
	if !ok {
		return nil, nil
	}
	hooks, problems := parseHookTables(body)
	if hooks == nil && problems == nil {
		hooks, problems = parseHookBlock(fm)
	}

	byOp := make(map[string]bool, len(hooks))
	for _, h := range hooks {
		byOp[h.Operation] = true
	}
	// Scalar shorthand for any known operation the block did not already claim.
	for _, op := range Operations() {
		skill := scalar(fm, op)
		if skill == "" {
			continue
		}
		if byOp[op] {
			problems = append(problems, fmt.Sprintf(
				"declares %s both as a hooks: entry and as the %s: shorthand — the hooks: entry wins; remove the shorthand", op, op))
			continue
		}
		hooks = append(hooks, Hook{
			Operation: op,
			Skill:     skill,
			Agent:     DefaultAgent,
			Verdict:   IsVerdict(op),
			Source:    SourceShorthand,
		})
	}
	sort.Slice(hooks, func(i, j int) bool { return hooks[i].Operation < hooks[j].Operation })
	return hooks, problems
}

// For returns the hook governing one operation, and whether it is declared.
func For(body, operation string) (Hook, bool) {
	hooks, _ := Parse(body)
	for _, h := range hooks {
		if h.Operation == operation {
			return h, true
		}
	}
	return Hook{}, false
}

// parseHookTables reads the TOML spelling of the block list — `[[meta.hooks]]`,
// one table per hook (sty_81bb0dde):
//
//	[[meta.hooks]]
//	operation = "create_review"
//	skill     = "satelle-story-create-review"
//	agent     = "strict-reviewer"           # optional
//
// It is the SAME grammar as the markdown block, in the format the file is
// actually written in — not a second grammar. Both spellings land on the same
// validation below, so a rule stated once applies to both.
//
// Returns (nil, nil) when the body is not TOML or declares no hooks table, which
// is how Parse knows to fall through to the markdown form.
func parseHookTables(body string) ([]Hook, []string) {
	entries, ok := docindex.MetaTables(body, hookKey)
	if !ok {
		return nil, nil
	}
	hooks := make([]Hook, 0, len(entries))
	var problems []string
	for _, e := range entries {
		h := Hook{Agent: DefaultAgent, Source: SourceHooks}
		for _, k := range sortedKeys(e) {
			switch k {
			case "operation":
				h.Operation = e[k]
			case "skill":
				h.Skill = e[k]
			case "agent":
				if e[k] != "" {
					h.Agent, h.AgentDeclared = e[k], true
				}
			default:
				problems = append(problems, fmt.Sprintf(
					"hooks: unknown key %q — a hook declares operation, skill, and optionally agent", k))
			}
		}
		hooks = append(hooks, h)
	}
	kept, more := validateHooks(hooks)
	return kept, append(problems, more...)
}

// sortedKeys keeps problem order deterministic — map iteration must never reach
// a reported message.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parseHookBlock reads the `hooks:` block list out of frontmatter lines.
//
// The repo carries no YAML dependency and must not gain one for this, so the
// block is read directly: a `hooks:` key at column zero, then indented entries
// each opened by `- ` and continued by `key: value` lines.
func parseHookBlock(fm []string) ([]Hook, []string) {
	start := -1
	for i, ln := range fm {
		if strings.TrimSpace(ln) == hookKey+":" || strings.HasPrefix(ln, hookKey+":") && strings.TrimSpace(strings.TrimPrefix(ln, hookKey+":")) == "" {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, nil
	}
	var hooks []Hook
	var problems []string
	var cur *Hook
	flush := func() {
		if cur == nil {
			return
		}
		hooks = append(hooks, *cur)
		cur = nil
	}
	for i := start + 1; i < len(fm); i++ {
		raw := fm[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if raw == trimmed { // dedented back to a sibling frontmatter key
			break
		}
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			flush()
			cur = &Hook{Agent: DefaultAgent, Source: SourceHooks}
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if trimmed == "" {
				continue
			}
		}
		if cur == nil {
			problems = append(problems, fmt.Sprintf("hooks: entry %q appears before any `- ` item", trimmed))
			continue
		}
		key, val, found := strings.Cut(trimmed, ":")
		if !found {
			problems = append(problems, fmt.Sprintf("hooks: line %q is not a key: value pair", trimmed))
			continue
		}
		val = unquote(val)
		switch strings.TrimSpace(key) {
		case "operation":
			cur.Operation = val
		case "skill":
			cur.Skill = val
		case "agent":
			if val != "" {
				cur.Agent = val
				cur.AgentDeclared = true
			}
		default:
			problems = append(problems, fmt.Sprintf(
				"hooks: unknown key %q — a hook declares operation, skill, and optionally agent", strings.TrimSpace(key)))
		}
	}
	flush()

	kept, more := validateHooks(hooks)
	return kept, append(problems, more...)
}

// validateHooks applies the declaration rules every spelling shares — an
// operation and a skill are required, an operation is declared once, and an
// unknown operation is CARRIED so validation can report it by name rather than
// the declaration vanishing without a trace. Shared deliberately: a rule stated
// once applies to the markdown block and the TOML tables alike, and the two
// cannot drift.
func validateHooks(hooks []Hook) ([]Hook, []string) {
	var problems []string
	seen := map[string]bool{}
	kept := hooks[:0]
	for _, h := range hooks {
		switch {
		case h.Operation == "":
			problems = append(problems, "hooks: an entry declares no operation")
			continue
		case h.Skill == "":
			problems = append(problems, fmt.Sprintf("hooks: %s declares no skill", h.Operation))
			continue
		case seen[h.Operation]:
			problems = append(problems, fmt.Sprintf("hooks: %s is declared more than once", h.Operation))
			continue
		}
		seen[h.Operation] = true
		if !Known(h.Operation) {
			problems = append(problems, fmt.Sprintf(
				"hooks: unknown operation %q — known operations: %s", h.Operation, strings.Join(Operations(), ", ")))
		}
		h.Verdict = IsVerdict(h.Operation)
		kept = append(kept, h)
	}
	return kept, problems
}

// frontmatter returns a workflow's frontmatter as `key: value` lines, from
// EITHER a markdown `---` block or a TOML `[meta]` table (sty_81bb0dde).
//
// This delegation is load-bearing, not tidiness. The route source is TOML, and
// `create_review` lives in its frontmatter — a reader that only understood `---`
// would find nothing, return no hooks, and the story-CREATE gate would stop
// firing with nothing anywhere reporting it. Silent, and on the engine path.
func frontmatter(body string) ([]string, bool) {
	return docindex.Frontmatter(body)
}

// scalar reads a top-level `key: value` from frontmatter lines. Indented lines
// are skipped so a nested key can never be mistaken for a top-level one.
func scalar(fm []string, key string) string {
	for _, ln := range fm {
		if ln != strings.TrimLeft(ln, " \t") {
			continue
		}
		rest, found := strings.CutPrefix(ln, key+":")
		if !found {
			continue
		}
		return unquote(rest)
	}
	return ""
}

// unquote trims whitespace, surrounding quotes, and a trailing `# comment`.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " #"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return strings.Trim(s, `"'`)
}
