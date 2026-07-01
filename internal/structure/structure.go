// Package structure holds satelle's DETERMINISTIC structural-conformance checks
// for authored substrate — the spine that defines what a valid skill, workflow,
// principle, or story draft IS. These checks are CODE, not an LLM rubric: a
// swappable agent/harness can never change what "valid" means, and they never
// flake. They replace the retired LLM structure reviewers (satelle-skill-review,
// satelle-workflow-review, satelle-principle-review, and the create-time
// satelle-story-review).
//
// repo-agnostic note: these checks judge STRUCTURE only. They never judge whether
// project substrate is "opinionated" — a project repo's substrate is MEANT to be
// opinionated. The satelle-repo-agnostic guard applies only to satelle's OWN
// embedded scope:system substrate and is a satelle-repo dev/CI concern, not a
// runtime gate shipped to every repo.
package structure

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// kebab matches a lower-kebab-case slug (the universal artifact-name shape).
var kebab = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// numberedAC matches a numbered acceptance-criterion line ("1. …" or "2) …").
var numberedAC = regexp.MustCompile(`(?m)^\s*\d+[.)]\s+\S`)

// deprecatedActorKeyword matches the retired performer keyword in either grammar:
// the DOT node attribute `actor=` or the inline-state map key `actor:` (sty_7db2ed7d).
var deprecatedActorKeyword = regexp.MustCompile(`\bactor\s*[=:]`)

// beforeArrow / afterArrow capture the identifier token on either side of a
// lifecycle arrow ("→" or "->") in a workflow description — the tokens a reader
// reads as the lifecycle states.
var beforeArrow = regexp.MustCompile(`([A-Za-z][\w]*)\s*(?:→|->)`)
var afterArrow = regexp.MustCompile(`(?:→|->)\s*([A-Za-z][\w]*)`)

// arrowChainTokens returns the deduped state tokens adjacent to a lifecycle arrow
// in s — used to check a description's stated lifecycle against the DOT's nodes.
func arrowChainTokens(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, re := range []*regexp.Regexp{beforeArrow, afterArrow} {
		for _, m := range re.FindAllStringSubmatch(s, -1) {
			if t := m[1]; !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// Doc returns the structural problems with an authored doc of the given kind
// (skills | workflows | principles), empty when conformant. resolveSkill reports
// whether a referenced skill resolves in the substrate (embedded ∪ project); it is
// used only for workflow executor-skill actionability and may be nil to skip it.
func Doc(kind, name, body string, resolveSkill func(skill string) bool) []string {
	switch kind {
	case "skills":
		return checkSkill(name, body)
	case "workflows":
		return checkWorkflow(name, body, resolveSkill)
	case "principles":
		return checkPrinciple(name, body)
	default:
		return nil // free-form documents have no structure check (OKF covers them)
	}
}

// Checked reports whether a doc kind has a deterministic structure check (the
// authored substrate kinds; free-form documents are covered by OKF instead).
func Checked(kind string) bool {
	switch kind {
	case "skills", "workflows", "principles":
		return true
	default:
		return false
	}
}

// Story returns the structural problems with a draft work item, empty when
// conformant: a specific title, a goal body that is not a title restatement, and
// at least one numbered acceptance criterion. This is the deterministic
// replacement for the create-time satelle-story-review rubric.
func Story(title, body, acceptance string) []string {
	var p []string
	if strings.TrimSpace(title) == "" {
		p = append(p, "title is empty")
	}
	switch {
	case strings.TrimSpace(body) == "":
		p = append(p, "body (the goal / what done looks like) is empty")
	case strings.EqualFold(strings.TrimSpace(body), strings.TrimSpace(title)):
		p = append(p, "body just restates the title — state the goal / outcome")
	}
	if !numberedAC.MatchString(acceptance) {
		p = append(p, "acceptance_criteria needs at least one numbered, testable item (e.g. \"1. …\")")
	}
	return p
}

// checkSkill: frontmatter (name == slug, type: skill, description), a usable
// definition (a rubric body OR a self-contained check), and a kebab name.
func checkSkill(name, body string) []string {
	fm, rest, ok := splitFM(body)
	if !ok {
		return []string{"missing YAML frontmatter"}
	}
	var p []string
	p = append(p, requireName(fm, name)...)
	if fmScalar(fm, "type") != "skill" {
		p = append(p, `frontmatter must have "type: skill" (OKF)`)
	}
	if fmScalar(fm, "description") == "" {
		p = append(p, "frontmatter missing a non-empty description")
	}
	// A usable definition: a rubric (prose body) OR a self-contained check (a
	// fenced ```check block, or a `check:` frontmatter scalar). See the
	// satelle-reviewer-self-contained principle.
	if !hasProse(rest) && !hasCheckBlock(rest) && !fmHas(fm, "check") {
		p = append(p, "no usable definition — provide a rubric body or a self-contained check")
	}
	return p
}

// CheckTask validates a task work-definition FILE (.satelle/tasks/tsk_*.md).
// Tasks are authored substrate (sty_c1f9e74c) — the file is the source of truth —
// so, like the other authored kinds, their on-disk form carries a deterministic
// STRUCTURAL contract: frontmatter with an id, kind: task, and a status, plus a
// title. The richer work-definition contract (a body declaring the ACTION and how
// success is VERIFIED) is judged by the validate-before GATE, not this structural
// check. Exported because tasks are ingested into the workitem store (not the doc
// index), so `satelle task validate` checks them on a dedicated pass rather than Doc().
func CheckTask(body string) []string {
	fm, rest, ok := splitFM(body)
	if !ok {
		return []string{"missing YAML frontmatter"}
	}
	var p []string
	if fmScalar(fm, "id") == "" {
		p = append(p, "frontmatter missing id")
	}
	if fmScalar(fm, "kind") != "task" {
		p = append(p, `frontmatter must have "kind: task"`)
	}
	if fmScalar(fm, "status") == "" {
		p = append(p, "frontmatter missing status")
	}
	if !strings.Contains(rest, "# ") {
		p = append(p, "body missing a `# Title` heading")
	}
	return p
}

// checkPrinciple: frontmatter (name == slug, type: principle, description, tags)
// and a substantive (non-stub) body.
func checkPrinciple(name, body string) []string {
	fm, rest, ok := splitFM(body)
	if !ok {
		return []string{"missing YAML frontmatter"}
	}
	var p []string
	p = append(p, requireName(fm, name)...)
	if fmScalar(fm, "type") != "principle" {
		p = append(p, `frontmatter must have "type: principle" (OKF)`)
	}
	if fmScalar(fm, "description") == "" {
		p = append(p, "frontmatter missing a non-empty description")
	}
	if !fmHas(fm, "tags") {
		p = append(p, "frontmatter missing tags")
	}
	if !hasProse(rest) {
		p = append(p, "body is a stub — state the guidance and its rationale, not just a heading")
	}
	return p
}

// checkWorkflow: frontmatter (name == slug, type: workflow, description,
// applies_to, scope), a parseable DOT lifecycle, a sound graph (connected /
// terminal / spine / backlog-start), and resolvable executor-path skills.
func checkWorkflow(name, body string, resolveSkill func(skill string) bool) []string {
	fm, _, ok := splitFM(body)
	if !ok {
		return []string{"missing YAML frontmatter"}
	}
	var p []string
	p = append(p, requireName(fm, name)...)
	if fmScalar(fm, "type") != "workflow" {
		p = append(p, `frontmatter must have "type: workflow" (OKF)`)
	}
	if fmScalar(fm, "description") == "" {
		p = append(p, "frontmatter missing a non-empty description")
	}
	if !fmHas(fm, "applies_to") {
		p = append(p, "frontmatter missing applies_to (the story categories it governs; [\"*\"] is the wildcard)")
	}
	if fmScalar(fm, "scope") == "" {
		p = append(p, "frontmatter missing scope")
	}
	// Enforce the actor→agent rename (sty_7db2ed7d): the performer keyword is
	// `agent` now; the legacy `actor=` (DOT) / `actor:` (inline state) no longer
	// parses, so flag it explicitly rather than letting a node silently lose its
	// performer.
	if deprecatedActorKeyword.MatchString(body) {
		p = append(p, `deprecated "actor" performer keyword — use "agent" (actor=/actor: no longer parses)`)
	}
	spec, parsed := wfdot.Parse(body)
	if !parsed {
		p = append(p, "no parseable DOT lifecycle (a fenced ```dot digraph)")
		return p
	}
	p = append(p, wfdot.Validate(spec)...)
	// Drift guard (sty_ca9f675f): the description's lifecycle arrow-chain must name
	// only states that exist in the DOT. Hand-maintained prose drifts from the graph
	// — a renamed node left in the description silently lies about the lifecycle —
	// so flag any arrow-adjacent token that is not a node. The DOT is the authority;
	// the description is checked against it.
	if desc := fmScalar(fm, "description"); desc != "" {
		nodeSet := map[string]bool{}
		for _, st := range spec.States {
			nodeSet[st.Name] = true
		}
		for _, tok := range arrowChainTokens(desc) {
			if !nodeSet[tok] {
				p = append(p, fmt.Sprintf("description's lifecycle arrow-chain names %q, which is not a node in the DOT (prose/DOT drift)", tok))
			}
		}
	}
	// backlog is the initial state — every satelle work item is created at
	// backlog, so a workflow that begins elsewhere desyncs status and the
	// progress lights. Checked only when a start is determinable.
	if start := spec.Start(); start != "" && start != "backlog" {
		p = append(p, fmt.Sprintf(`initial state is %q — the lifecycle must start at "backlog"`, start))
	}
	// Executor-path actionability: every EXECUTOR-step skill on a path to done
	// must resolve, else the story cannot be driven to its terminal state (the
	// wasted-work trap, sty_09ef53d6). Reviewer GATES degrade to advisory when
	// absent by design, so they are not hard-required here.
	if resolveSkill != nil {
		for _, sk := range spec.ExecutorPathToDoneSkills() {
			if !resolveSkill(sk) {
				p = append(p, fmt.Sprintf("executor-step skill %q does not resolve in the substrate", sk))
			}
		}
	}
	return p
}

// requireName checks the frontmatter name is present, kebab-case, and matches the
// doc's slug (filename).
func requireName(fm []string, slug string) []string {
	n := fmScalar(fm, "name")
	switch {
	case n == "":
		return []string{"frontmatter missing name"}
	case !kebab.MatchString(n):
		return []string{fmt.Sprintf("name %q is not lower-kebab-case", n)}
	case n != slug:
		return []string{fmt.Sprintf("frontmatter name %q does not match the file slug %q", n, slug)}
	}
	return nil
}

// --- minimal frontmatter helpers (self-contained; mirror docindex/okf.go) ---

// splitFM splits a markdown body into its YAML frontmatter lines and the
// remaining body. ok is false when there is no terminated leading frontmatter.
func splitFM(body string) (fm []string, rest string, ok bool) {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, body, false
	}
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			return lines[1:j], strings.Join(lines[j+1:], "\n"), true
		}
	}
	return nil, body, false
}

// fmScalar returns the unquoted top-level scalar for key, or "".
func fmScalar(fm []string, key string) string {
	pre := key + ":"
	for _, ln := range fm {
		t := strings.TrimSpace(ln)
		if t == pre || strings.HasPrefix(t, pre+" ") {
			return unquote(strings.TrimSpace(strings.TrimPrefix(t, pre)))
		}
	}
	return ""
}

// fmHas reports whether key is present in the frontmatter (scalar or list).
func fmHas(fm []string, key string) bool {
	pre := key + ":"
	for _, ln := range fm {
		t := strings.TrimSpace(ln)
		if t == pre || strings.HasPrefix(t, pre+" ") {
			return true
		}
	}
	return false
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// hasProse reports whether the body carries substantive prose — at least one
// non-blank line that is not a markdown heading or a code fence.
func hasProse(body string) bool {
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") {
			continue
		}
		return true
	}
	return false
}

// hasCheckBlock reports whether the body carries a fenced ```check script block.
func hasCheckBlock(body string) bool {
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "```check") {
			return true
		}
	}
	return false
}
