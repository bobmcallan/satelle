package agentstep

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
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

// sharedVerdictPhrases are the passages of the shared reviewer-verdict
// principle: the re-presented-edge rules and the `reviewed`-required paragraph.
// An embedded reviewer skill states neither (it owns only its verdict JSON
// shape); the principle holds them and every judging dispatch is handed them.
var sharedVerdictPhrases = []string{"quotation comparison governs", "is required on every judgment"}

// embeddedPrincipleDocs is the embedded principles as a DocGetter, the way a
// repo with no override resolves them.
func embeddedPrincipleDocs() fakeDocs {
	var ps []docindex.Doc
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "principles" {
			ps = append(ps, docindex.Doc{Kind: "principles", Name: d.Name, Body: d.Body, Embedded: true})
		}
	}
	return fakeDocs{extraPrinciples: ps}
}

// TestEmbeddedReviewerSkillsQuotationComparison (AC3) walks the embedded skill
// root, which every checkout has. A skill is in the set when its frontmatter
// tags include type:reviewer, do not include type:functional-check, and the
// body has no ```check fence. The verdict rules live once, in the shared
// principle: no selected skill body restates them, yet the judging prompt each
// is composed into carries all of them.
func TestEmbeddedReviewerSkillsQuotationComparison(t *testing.T) {
	root := moduleRoot(t)
	selected := quotationReviewerPaths(t, filepath.Join(root, "internal", "config", "substrate", "skills"))
	assertOutsideReviewerSet(t, selected)

	verdict := ReviewerVerdictDoc(context.Background(), embeddedPrincipleDocs())
	if verdict == "" {
		t.Fatalf("embedded principle %s must resolve", config.ReviewerVerdictPrinciple)
	}
	assertQuotationRules(t, "principle "+config.ReviewerVerdictPrinciple, verdict)
	for _, path := range selected {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		for _, phrase := range sharedVerdictPhrases {
			if strings.Contains(strings.ToLower(body), phrase) {
				t.Errorf("%s restates %q, which belongs to %s alone", path, phrase, config.ReviewerVerdictPrinciple)
			}
		}
		if !strings.Contains(body, `"reviewed":`) {
			t.Errorf("%s must still own its verdict JSON shape, including the reviewed field", path)
		}
		// The pointer lives in the frontmatter description; a body section for it
		// would be the same run of words in every skill, which doctor reports.
		if desc := descriptionOf(body); !strings.Contains(desc, "[["+config.ReviewerVerdictPrinciple+"]]") {
			t.Errorf("%s description must point at %s", path, config.ReviewerVerdictPrinciple)
		}
		for _, heading := range []string{"## re-presented edge", "later rounds"} {
			if strings.Contains(strings.ToLower(body), heading) {
				t.Errorf("%s carries a %q section; the rules live in %s", path, heading, config.ReviewerVerdictPrinciple)
			}
		}
		seat, err := BuildSeatPayload(SeatPayloadInput{
			Charter: reviewerCharter(), Rubric: body, Judging: true, Verdict: verdict,
		})
		if err != nil {
			t.Fatal(err)
		}
		assertQuotationPhrases(t, path+" (composed judging prompt)", seat.SystemPrompt)
		if n := strings.Count(seat.SystemPrompt, sharedVerdictPhrases[0]); n != 1 {
			t.Errorf("%s: the composed judging prompt carries the re-presented-edge rules %d times, want once", path, n)
		}
	}
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

// quotationReviewerPaths returns the reviewer skills under dir.
func quotationReviewerPaths(t *testing.T, dir string) []string {
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
		if quotationReviewer(string(body)) {
			selected = append(selected, path)
		}
	}
	if len(selected) == 0 {
		t.Fatalf("no reviewer skills selected under %s", dir)
	}
	return selected
}

// selectQuotationReviewers returns the reviewer skills under dir and asserts
// each carries the quotation instruction itself: the repo's override skills
// still state it in the skill body.
func selectQuotationReviewers(t *testing.T, dir string) []string {
	t.Helper()
	selected := quotationReviewerPaths(t, dir)
	for _, path := range selected {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		assertQuotationPhrases(t, path, string(body))
	}
	return selected
}

// assertQuotationPhrases asserts body states the quotation rules and its own
// verdict JSON carries the reviewed field.
func assertQuotationPhrases(t *testing.T, path, body string) {
	t.Helper()
	assertQuotationRules(t, path, body)
	if !strings.Contains(body, `"reviewed":`) {
		t.Errorf("%s verdict JSON omits the reviewed field", path)
	}
}

// assertQuotationRules asserts body states the re-presented-edge rules and the
// reviewed-required rule.
func assertQuotationRules(t *testing.T, path, body string) {
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
}

// descriptionOf is the frontmatter description line of a skill, "" when absent.
func descriptionOf(body string) string {
	head := strings.TrimSuffix(strings.TrimSuffix(body, stripFrontmatter(body)), "\n")
	for _, line := range strings.Split(head, "\n") {
		if v, ok := strings.CutPrefix(line, "description:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
		if strings.HasPrefix(sec.rel, embeddedSkillsRel) {
			// The embedded skills delegate the quotation rules to the shared
			// reviewer-verdict principle, which the judging dispatch carries.
			if !strings.Contains(norm, "[["+config.ReviewerVerdictPrinciple+"]]") {
				t.Errorf("%s later-round section must point at %s:\n%s", sec.rel, config.ReviewerVerdictPrinciple, raw)
			}
		} else {
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
