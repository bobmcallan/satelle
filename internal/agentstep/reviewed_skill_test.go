package agentstep

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/structure"
)

const (
	embeddedSkillsRel = "internal/config/substrate/skills/"
	overrideSkillsRel = ".satelle/skills/"
)

// later is one later-round section the quotation rule must be stated in.
type later struct {
	rel     string
	heading string
	bold    bool
	forbid  []string
}

var laterSections = []later{
	{".satelle/skills/satelle-story-plan-review.md", "## 6. Later rounds", false, []string{
		"verify every prior blocking finding",
		"a criterion or plan section that changed",
	}},
	{"internal/config/substrate/skills/satelle-story-plan-review.md", "## 4. Later rounds", false, []string{
		"verify the prior findings",
		"acceptance criterion that changed (open definition_edits.path)",
	}},
	{".satelle/skills/satelle-story-architecture-review.md", "", true, []string{
		"verify each prior finding",
		"acceptance criterion that changed (open definition_edits.path)",
	}},
	{".satelle/skills/satelle-story-integration-coverage-review.md", "", true, []string{
		"verify each prior finding",
		"acceptance criterion that changed (open definition_edits.path)",
	}},
	{".satelle/skills/satelle-story-intent-review.md", "## Later rounds", false, []string{
		"changed definition field",
		"verify every prior finding",
	}},
	{"internal/config/substrate/skills/satelle-story-intent-review.md", "## Later rounds", false, []string{
		"changed definition field",
		"verify every prior finding",
	}},
}

// The plan descriptions and the architecture and coverage headings sit
// outside the later-round body. The same normaliser has to see them, or
// "verify prior findings" and "then verify" stay in the skill and the test
// still passes.
var leftoverSkills = []struct {
	rel     string
	phrases []string
}{
	{".satelle/skills/satelle-story-plan-review.md", []string{
		"later rounds verify prior findings",
		"verify prior findings",
		"number your findings so the next round can answer each one",
	}},
	{"internal/config/substrate/skills/satelle-story-plan-review.md", []string{
		"later rounds verify prior findings",
		"verify prior findings",
		"number your findings so the next round can answer each one",
	}},
	{".satelle/skills/satelle-story-architecture-review.md", []string{
		"every blocker first, then verify",
		"then verify",
		"verify prior findings",
	}},
	{".satelle/skills/satelle-story-integration-coverage-review.md", []string{
		"every blocker first, then verify",
		"then verify",
		"verify prior findings",
	}},
}

// TestEmbeddedReviewerSkillsQuotationComparison (AC7) walks the embedded skill
// root, which every checkout has. A skill is in the set when its frontmatter
// tags include type:reviewer, do not include type:functional-check, and the
// body has no ```check fence.
func TestEmbeddedReviewerSkillsQuotationComparison(t *testing.T) {
	root := moduleRoot(t)
	selected := selectQuotationReviewers(t, filepath.Join(root, "internal", "config", "substrate", "skills"))
	assertOutsideReviewerSet(t, selected)
	for _, name := range []string{"satelle-story-scope-review.md", "satelle-story-done-review.md"} {
		var hits []string
		for _, path := range selected {
			if filepath.Base(path) == name {
				hits = append(hits, path)
			}
		}
		if len(hits) != 1 {
			t.Errorf("%s must be selected once under the embedded skills root, got %v", name, hits)
		}
	}
	assertLaterRounds(t, root, embeddedSkillsRel)
}

// TestOverrideReviewerSkillsQuotationComparison (AC7) runs the same checks
// over the repo's live override skills. .satelle/ is gitignored operator
// substrate, so a clean checkout and CI do not have it and the test skips.
func TestOverrideReviewerSkillsQuotationComparison(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, ".satelle", "skills")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skip(".satelle/skills is gitignored operator substrate; not present in this checkout")
	} else if err != nil {
		t.Fatal(err)
	}
	selected := selectQuotationReviewers(t, dir)
	assertOutsideReviewerSet(t, selected)
	for _, name := range []string{"satelle-story-deploy-review.md", "satelle-story-integration-review.md"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "quotation comparison governs") {
			t.Errorf("functional check %s must be unchanged", name)
		}
		for _, path := range selected {
			if filepath.Base(path) == name {
				t.Errorf("functional check %s must not be selected", path)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "satelle-integration-review.md")); err != nil {
		t.Fatal(err)
	}
	var sawIntegration bool
	for _, path := range selected {
		if filepath.Base(path) == "satelle-integration-review.md" {
			sawIntegration = true
		}
	}
	if !sawIntegration {
		t.Error("satelle-integration-review is type:reviewer only and must carry the instruction")
	}
	for _, name := range []string{"satelle-story-scope-review.md", "satelle-story-done-review.md"} {
		for _, path := range selected {
			if filepath.Base(path) == name {
				t.Errorf("%s must be edited only under the embedded skills root, got %s", name, path)
			}
		}
	}
	assertLaterRounds(t, root, overrideSkillsRel)
}

// selectQuotationReviewers returns the reviewer skills under dir and asserts
// each carries the quotation instruction.
func selectQuotationReviewers(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := reviewerSkillMarkdown(dir)
	if err != nil {
		t.Fatal(err)
	}
	var selected []string
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !quotationReviewer(string(body)) {
			continue
		}
		selected = append(selected, path)
		assertQuotationPhrases(t, path, string(body))
	}
	if len(selected) == 0 {
		t.Fatalf("no reviewer skills selected under %s", dir)
	}
	return selected
}

func assertQuotationPhrases(t *testing.T, path, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, phrase := range []string{
		"quotation comparison governs",
		"re-issue",
		"does not clear a standing rejection",
		"reviewed_truncated",
		"do not cite an older",
		// A judgment records the quotation. The differ branch alone
		// never stores one, so a later round has nothing to cite.
		"the first verdict",
		"sets `reviewed`",
		"re-judge the words this verdict would rest on and set `reviewed`",
		"the same `reviewed` string",
	} {
		if !strings.Contains(lower, phrase) {
			t.Errorf("%s missing %q", path, phrase)
		}
	}
	if !strings.Contains(body, `"reviewed":`) {
		t.Errorf("%s verdict JSON omits the reviewed field", path)
	}
}

func assertOutsideReviewerSet(t *testing.T, selected []string) {
	t.Helper()
	for _, name := range []string{
		"plan.md", "commit.md", "satelle-lessons.md", "satelle-step-summary.md",
		"satelle-workflow-advisor.md", "README.md",
	} {
		for _, path := range selected {
			if filepath.Base(path) == name {
				t.Errorf("%s is outside the reviewer set", path)
			}
		}
	}
}

// assertLaterRounds checks the later-round sections and leftovers whose rel
// path starts with prefix.
func assertLaterRounds(t *testing.T, root, prefix string) {
	t.Helper()
	for _, sec := range laterSections {
		if !strings.HasPrefix(sec.rel, prefix) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, sec.rel))
		if err != nil {
			t.Fatal(err)
		}
		raw, ok := laterRoundSection(string(body), sec.heading, sec.bold)
		if !ok {
			t.Errorf("%s: later-round section not found", sec.rel)
			continue
		}
		norm := normaliseLaterRound(raw)
		folded := strings.ToLower(norm)
		if !strings.Contains(folded, "quotation comparison governs") {
			t.Errorf("%s later-round section does not state the quotation rule:\n%s", sec.rel, raw)
		}
		for _, phrase := range []string{
			"the first verdict",
			"sets `reviewed`",
			"the same `reviewed` string",
		} {
			if !strings.Contains(folded, phrase) {
				t.Errorf("%s later-round section missing %q:\n%s", sec.rel, phrase, raw)
			}
		}
		for _, phrase := range sec.forbid {
			if strings.Contains(folded, strings.ToLower(phrase)) {
				t.Errorf("%s still contains %q", sec.rel, phrase)
			}
		}
		if strings.Contains(norm, "definition_edits") {
			t.Errorf("%s later-round section still contains definition_edits:\n%s", sec.rel, norm)
		}
		if strings.Contains(norm, "updated_at") || strings.Contains(folded, "since that accept") || strings.Contains(folded, "last title") {
			t.Errorf("%s uses a timestamp as the test that the words are unchanged:\n%s", sec.rel, norm)
		}
		if isoDateTime.MatchString(norm) {
			t.Errorf("%s later-round section contains an ISO-8601 datetime:\n%s", sec.rel, norm)
		}
	}
	for _, item := range leftoverSkills {
		if !strings.HasPrefix(item.rel, prefix) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, item.rel))
		if err != nil {
			t.Fatal(err)
		}
		norm := strings.ToLower(normaliseLaterRound(string(body)))
		for _, phrase := range item.phrases {
			if strings.Contains(norm, phrase) {
				t.Errorf("%s still tells a later round to verify prior findings: %q", item.rel, phrase)
			}
		}
	}
}

var isoDateTime = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}`)

// quotationReviewer is the tag rule: type:reviewer, not type:functional-check,
// and no ```check fence. It does not treat "has no check" as the definition.
func quotationReviewer(body string) bool {
	tags := frontmatterList(body, "tags")
	var reviewer, functional bool
	for _, tag := range tags {
		switch tag {
		case "type:reviewer":
			reviewer = true
		case "type:functional-check":
			functional = true
		}
	}
	if !reviewer || functional {
		return false
	}
	return structure.CheckFence(body) == ""
}

func reviewerSkillMarkdown(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "README.md" || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

func laterRoundSection(body, heading string, bold bool) (string, bool) {
	if bold {
		// The paragraph opens "**Later rounds.**" — the period sits inside
		// the emphasis, so the mark is the opening words, not a closed "**".
		const mark = "**Later rounds."
		i := strings.Index(body, mark)
		if i < 0 {
			return "", false
		}
		rest := body[i:]
		if j := strings.Index(rest, "\n\n"); j >= 0 {
			rest = rest[:j]
		}
		return rest, true
	}
	lines := strings.Split(body, "\n")
	start := -1
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), heading) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	var b []string
	b = append(b, lines[start])
	for _, ln := range lines[start+1:] {
		if strings.HasPrefix(ln, "## ") {
			break
		}
		b = append(b, ln)
	}
	return strings.Join(b, "\n"), true
}

// normaliseLaterRound strips markdown emphasis and collapses whitespace.
// An underscore that sits inside an identifier is kept, so the same text can
// still be checked for the token definition_edits.
func normaliseLaterRound(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '_' {
			b.WriteByte(s[i])
			continue
		}
		prev := i > 0 && isIdentByte(s[i-1])
		next := i+1 < len(s) && isIdentByte(s[i+1])
		if prev && next {
			b.WriteByte('_')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
