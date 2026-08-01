// Markdown front-end for the route constructor: done.md and step.md in, a Spec
// out. Hand-rolled with the same idiom the package already uses for DOT and
// frontmatter — a `##` heading opens a record, `key: value` lines fill it, CSV
// for lists. No YAML or TOML dependency is added for two small authored files.
//
// The grammar is deliberately thin. Anything it cannot express is a construction
// error naming the offending line, never a silent default: a route that quietly
// loses a gate is the one failure mode this representation must not have.
package wfdot

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseDone reads a declaration of done. One `## <category>` section per
// category; each section is a markdown list of obligations in order, plus
// optional directives:
//
//	## feature
//	- raised
//	- planned
//	- coded
//	+ surface:ui design-reviewed
//	park: blocked @satelle-story-blocked-review
//	cancel: cancelled @satelle-story-cancel-review
//	recover: in_progress from integration, release
//
// A `+ <tag> <obligation>` line appends an obligation when the story carries the
// tag — the "tags append obligations" rule, kept as a first-class line rather
// than a side table so the whole declaration reads top to bottom.
func ParseDone(body string) ([]List, error) {
	var out []List
	var cur *List
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "<!--") {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			out = append(out, List{Category: strings.TrimSpace(line[3:])})
			cur = &out[len(out)-1]
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue // document title or prose heading
		}
		if cur == nil {
			continue // preamble before the first category
		}
		switch {
		case strings.HasPrefix(line, "- "):
			cur.Obligations = append(cur.Obligations, strings.TrimSpace(line[2:]))
		case strings.HasPrefix(line, "+ "):
			tag, obligation, ok := strings.Cut(strings.TrimSpace(line[2:]), " ")
			if !ok {
				return nil, fmt.Errorf("done.md line %d: `+ <tag> <obligation>` expected, got %q", i+1, line)
			}
			if cur.TagObligations == nil {
				cur.TagObligations = map[string][]string{}
			}
			cur.TagObligations[tag] = append(cur.TagObligations[tag], strings.TrimSpace(obligation))
		default:
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				return nil, fmt.Errorf("done.md line %d: expected `- obligation`, `+ tag obligation`, or `key: value`, got %q", i+1, line)
			}
			if err := applyDoneKey(cur, strings.TrimSpace(key), strings.TrimSpace(value), i+1); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// applyDoneKey sets one role-state declaration on a category. The `name @gate`
// form keeps a role state and the reviewer that admits it on one line — they are
// a pair, and splitting them across keys invites declaring one without the other.
func applyDoneKey(l *List, key, value string, line int) error {
	switch key {
	case "park":
		name, gate, advisor, advisorSkill := parseRoleValue(value)
		l.Park, l.ParkGate = name, gate
		l.ParkOnEnterAgent, l.ParkOnEnterSkill = advisor, advisorSkill
	case "cancel":
		name, gate, _, _ := parseRoleValue(value)
		l.Cancel, l.CancelGate = name, gate
	case "recover":
		target, from, ok := strings.Cut(value, " from ")
		l.Recover = strings.TrimSpace(target)
		if ok {
			l.RecoverFrom = splitCSV(from)
		}
	default:
		return fmt.Errorf("done.md line %d: unknown key %q (want park, cancel, recover)", line, key)
	}
	return nil
}

// parseRoleValue reads `name @gate-skill` with an optional `advise <agent>
// @skill` suffix naming the one-shot entry advisor.
func parseRoleValue(v string) (name, gate, advisor, advisorSkill string) {
	rest := v
	if head, tail, ok := strings.Cut(v, " advise "); ok {
		rest = head
		advisor, advisorSkill = parseNameSkill(tail)
	}
	name, gate = parseNameSkill(rest)
	return name, gate, advisor, advisorSkill
}

func parseNameSkill(v string) (name, skill string) {
	v = strings.TrimSpace(v)
	if head, tail, ok := strings.Cut(v, "@"); ok {
		return strings.TrimSpace(head), strings.TrimSpace(tail)
	}
	return v, ""
}

// ParseSteps reads a step catalogue. One `## <name>` section per step and one
// `## gate <skill>` section per always-on gate:
//
//	## in_progress
//	agent: executor
//	skills: code
//	reviewers: satelle-code-ac-review, satelle-story-scope-review
//	provides: coded
//	requires: planned
//
//	## gate satelle-build-unit-check
//	on: integration
func ParseSteps(body string) (Catalogue, error) {
	var cat Catalogue
	var step *Step
	var gate *RouteGate
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "<!--") {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			name := strings.TrimSpace(line[3:])
			step, gate = nil, nil
			if skill, ok := strings.CutPrefix(name, "gate "); ok {
				cat.Gates = append(cat.Gates, RouteGate{Skill: strings.TrimSpace(skill)})
				gate = &cat.Gates[len(cat.Gates)-1]
				continue
			}
			cat.Steps = append(cat.Steps, Step{Name: name})
			step = &cat.Steps[len(cat.Steps)-1]
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if step == nil && gate == nil {
			continue // preamble before the first section
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Catalogue{}, fmt.Errorf("step.md line %d: expected `key: value`, got %q", i+1, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		var err error
		if gate != nil {
			err = applyGateKey(gate, key, value, i+1)
		} else {
			err = applyStepKey(step, key, value, i+1)
		}
		if err != nil {
			return Catalogue{}, err
		}
	}
	return cat, nil
}

func applyStepKey(s *Step, key, value string, line int) error {
	switch key {
	case "agent":
		s.Agent = value
	case "skills":
		s.Skills = splitCSV(value)
	case "reviewers":
		s.Reviewers = splitCSV(value)
	case "reviewer_agent":
		s.ReviewerAgent = value
	case "parallel":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("step.md line %d: parallel must be a number, got %q", line, value)
		}
		s.Parallel, s.ParallelSet = n, true
	case "provides":
		s.Provides = value
	case "requires":
		s.Requires = splitCSV(value)
	case "applies_to":
		s.AppliesTo = splitCSV(value)
	case "on_enter":
		s.OnEnterAgent, s.OnEnterSkill = parseNameSkill(value)
	case "start":
		s.Start = value == "true"
	case "terminal":
		s.Terminal = value == "true"
	default:
		return fmt.Errorf("step.md line %d: unknown step key %q", line, key)
	}
	return nil
}

func applyGateKey(g *RouteGate, key, value string, line int) error {
	switch key {
	case "agent":
		g.Agent = value
	case "on":
		g.On = splitCSV(value)
	case "applies_to":
		g.AppliesTo = splitCSV(value)
	case "mandatory":
		g.Mandatory = value == "true"
	default:
		return fmt.Errorf("step.md line %d: unknown gate key %q", line, key)
	}
	return nil
}

// ParseRoute is the single entry point: the two authored bodies plus a category
// and the story's tags, in; a Spec, out. An unknown category is an error rather
// than an empty route — silently producing a workflow with no gates is the worst
// available failure.
func ParseRoute(doneBody, stepBody, category string, tags []string) (Spec, error) {
	lists, err := ParseDone(doneBody)
	if err != nil {
		return Spec{}, err
	}
	cat, err := ParseSteps(stepBody)
	if err != nil {
		return Spec{}, err
	}
	for _, l := range lists {
		if l.Category == category {
			return BuildRoute(l, cat, tags)
		}
	}
	var known []string
	for _, l := range lists {
		known = append(known, l.Category)
	}
	return Spec{}, fmt.Errorf("no declaration of done for category %q (have: %s)",
		category, strings.Join(known, ", "))
}
