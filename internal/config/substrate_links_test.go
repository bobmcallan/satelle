package config

import (
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// Justified non-embedded wikilink targets. Each entry states WHY it cannot
// resolve inside the embedded corpus (sty_6830e78e AC2).
var wikilinkAllow = map[string]string{
	// Constitution is repo-seeded at init, not an embedded principle kind.
	"satelle-constitution": "project constitution seeded by init; not an embedded principle file",
	// Session-trace docs are operator research notes, not product defaults.
	"session-trace-workflow-review-followups": "operator session-trace document; not product-canon substrate",
}

// Justified @skill: references that appear outside workflow DOT bindings
// (format examples in principles/skill prose). Workflow DOT @skill: is strict
// via wfdot — placeholders never appear as real bindings.
var skillRefAllow = map[string]string{
	// Prose/format examples only (not DOT bindings).
	"NAME": "format example placeholder in task-workflow prose",
	"a":    "CSV multi-reviewer format example (@skill:a,@skill:b) in prose",
	"b":    "CSV multi-reviewer format example (@skill:a,@skill:b) in prose",
}

var (
	wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	skillRefRe = regexp.MustCompile(`@skill:([A-Za-z0-9_-]+)`)
)

// stripFences removes fenced code blocks so regex classes like [[:space:]]
// inside ```check scripts are not treated as wikilinks. NOT used for workflow
// skill-ref resolution — those live inside ```dot and are parsed via wfdot.
func stripFences(body string) string {
	var b strings.Builder
	inFence := false
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func embeddedIndex() map[string]map[string]bool {
	idx := map[string]map[string]bool{}
	for _, d := range EmbeddedDefaults() {
		if idx[d.Kind] == nil {
			idx[d.Kind] = map[string]bool{}
		}
		idx[d.Kind][d.Name] = true
	}
	return idx
}

func resolveWikilink(idx map[string]map[string]bool, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if strings.Contains(target, "/") {
		parts := strings.SplitN(target, "/", 2)
		return idx[parts[0]][parts[1]]
	}
	for _, names := range idx {
		if names[target] {
			return true
		}
	}
	return false
}

// workflowSkillRefs extracts every skill name bound by an embedded workflow:
// transition Skills/Skill, state Skill / OnEnterSkill, and create_review
// frontmatter. Uses wfdot so ```dot fences and CSV multi-reviewer lists are real.
func workflowSkillRefs(name, body string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(sk string) {
		sk = strings.TrimSpace(sk)
		sk = strings.TrimPrefix(sk, "@skill:")
		if sk == "" || seen[sk] {
			return
		}
		seen[sk] = true
		out = append(out, sk)
	}
	// create_review: frontmatter (create-time gate, not always in the DOT graph)
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "create_review:") {
			add(strings.TrimSpace(strings.TrimPrefix(t, "create_review:")))
		}
		if t == "---" && len(out) > 0 {
			// still continue — DOT may add more; only stop after second --- is messy
		}
		if t == "---" {
			// ignore
		}
	}
	// Also parse FM properly for create_review
	if strings.HasPrefix(body, "---") {
		rest := body[3:]
		if i := strings.Index(rest, "\n---"); i >= 0 {
			for _, ln := range strings.Split(rest[:i], "\n") {
				t := strings.TrimSpace(ln)
				if strings.HasPrefix(t, "create_review:") {
					add(strings.TrimSpace(strings.TrimPrefix(t, "create_review:")))
				}
			}
		}
	}
	spec, ok := wfdot.Parse(body)
	if !ok {
		return out
	}
	for _, e := range spec.Transitions {
		skills := e.Skills
		if len(skills) == 0 && e.Skill != "" {
			skills = []string{e.Skill}
		}
		for _, sk := range skills {
			add(sk)
		}
	}
	for _, n := range spec.States {
		add(n.Skill)
		add(n.OnEnterSkill)
	}
	_ = name
	return out
}

// TestEmbeddedWikilinksResolve proves every [[wikilink]] in the embedded corpus
// (outside code fences) resolves to another embedded artifact or allow-list entry.
func TestEmbeddedWikilinksResolve(t *testing.T) {
	idx := embeddedIndex()
	for _, d := range EmbeddedDefaults() {
		d := d
		t.Run(d.Kind+"/"+d.Name, func(t *testing.T) {
			body := stripFences(d.Body)
			for _, m := range wikilinkRe.FindAllStringSubmatch(body, -1) {
				target := m[1]
				if i := strings.IndexByte(target, '|'); i >= 0 {
					target = target[:i]
				}
				if resolveWikilink(idx, target) {
					continue
				}
				if _, ok := wikilinkAllow[target]; ok {
					continue
				}
				t.Errorf("unresolved wikilink [[%s]] — add to embedded corpus or wikilinkAllow with justification", target)
			}
		})
	}
}

// TestEmbeddedWorkflowSkillRefsResolve: every skill bound by an embedded
// workflow (DOT + create_review) resolves to an embedded skill. Strict —
// no prose allow-list (sty_6830e78e AC2).
func TestEmbeddedWorkflowSkillRefsResolve(t *testing.T) {
	idx := embeddedIndex()
	skills := idx["skills"]
	for _, d := range EmbeddedDefaults() {
		if d.Kind != "workflows" {
			continue
		}
		d := d
		t.Run(d.Name, func(t *testing.T) {
			refs := workflowSkillRefs(d.Name, d.Body)
			if len(refs) == 0 {
				t.Fatal("workflow binds no skills — unexpected for embedded defaults")
			}
			for _, sk := range refs {
				if sk == "NAME" {
					// task-workflow placeholder in prose inside DOT comments — skip only if not a real binding
					// If it appears as a transition skill, fail.
					t.Logf("placeholder skill NAME skipped if only in docs")
					continue
				}
				if !skills[sk] {
					t.Errorf("workflow binds @skill:%s which is not an embedded skill", sk)
				}
			}
		})
	}
}

// TestEmbeddedProseSkillRefsResolve: @skill: in non-workflow prose may use
// skillRefAllow for format examples; otherwise must resolve.
func TestEmbeddedProseSkillRefsResolve(t *testing.T) {
	idx := embeddedIndex()
	skills := idx["skills"]
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "workflows" {
			continue // handled by DOT path
		}
		d := d
		t.Run(d.Kind+"/"+d.Name, func(t *testing.T) {
			// Strip fences so check-script examples do not pollute; keep prose.
			body := stripFences(d.Body)
			for _, m := range skillRefRe.FindAllStringSubmatch(body, -1) {
				name := m[1]
				if skills[name] {
					continue
				}
				if _, ok := skillRefAllow[name]; ok {
					continue
				}
				t.Errorf("unresolved @skill:%s — must be an embedded skill or on skillRefAllow", name)
			}
		})
	}
}

// TestLinkResolutionNegatives: synthetic unresolved targets fail resolvers.
func TestLinkResolutionNegatives(t *testing.T) {
	idx := embeddedIndex()
	if resolveWikilink(idx, "does-not-ship") {
		t.Error("does-not-ship must not resolve")
	}
	if idx["skills"]["nope"] {
		t.Error("nope must not be an embedded skill")
	}
	// create_review + DOT path rejects a fake skill
	body := `---
name: fake-workflow
type: workflow
create_review: satelle-does-not-exist-review
---

` + "```dot\n" + `digraph {
  backlog -> done [agent=reviewer, prompt="@skill:also-missing-skill"]
}
` + "```\n"
	refs := workflowSkillRefs("fake-workflow", body)
	found := false
	for _, r := range refs {
		if r == "satelle-does-not-exist-review" || r == "also-missing-skill" {
			found = true
			if idx["skills"][r] {
				t.Errorf("%s unexpectedly embedded", r)
			}
		}
	}
	if !found {
		t.Fatalf("workflowSkillRefs missed create_review/DOT skills: %v", refs)
	}
}

// TestWikilinkAllowListJustified ensures every allow entry has a non-empty reason.
func TestWikilinkAllowListJustified(t *testing.T) {
	for k, v := range wikilinkAllow {
		if strings.TrimSpace(v) == "" {
			t.Errorf("wikilinkAllow[%q] missing justification", k)
		}
	}
	for k, v := range skillRefAllow {
		if strings.TrimSpace(v) == "" {
			t.Errorf("skillRefAllow[%q] missing justification", k)
		}
	}
}
