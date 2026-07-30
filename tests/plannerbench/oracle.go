//go:build plannerbench

package plannerbench

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// The oracle is INDEPENDENT of the transition validator. The previous harness
// scored a plan with agentartifact.ValidateAll — the same function the gate
// runs — so the score was true exactly when the run committed and carried no
// quality signal at all. This oracle never imports agentartifact. It scores
// substance against the seeded tree: real files, really-declared symbols, and a
// really-existing test. The literal string "AC<n>" contributes NOTHING.

// oracleCriterion is one criterion's verified per-criterion finding.
type oracleCriterion struct {
	Ordinal       int      `json:"ordinal"`
	Covered       bool     `json:"covered"`
	FilesHit      []string `json:"files_hit,omitempty"`
	FilesMissed   []string `json:"files_missed,omitempty"`
	SymbolsHit    []string `json:"symbols_hit,omitempty"`
	SymbolsMissed []string `json:"symbols_missed,omitempty"`
	TestNamed     string   `json:"test_named,omitempty"`
	Evidence      string   `json:"evidence"`
}

// deterministicScore is the primary quality signal — available with no
// credentials, so a credential-less run still yields a real score.
type deterministicScore struct {
	OK          bool              `json:"ok"`
	Covered     int               `json:"covered"`
	Total       int               `json:"total"`
	Fraction    float64           `json:"fraction"`
	Criteria    []oracleCriterion `json:"criteria"`
	LabelOnly   bool              `json:"label_only"`
	LabelsFound int               `json:"acceptance_labels_found"`
}

// judgeScore is the OPTIONAL second oracle. It is recorded beside the
// deterministic score with its own binding identity and never merged into it, so
// a judge outage cannot silently change a sample's score.
type judgeScore struct {
	Available bool             `json:"available"`
	BindingID string           `json:"binding_id,omitempty"`
	Model     string           `json:"model,omitempty"`
	Covered   int              `json:"covered,omitempty"`
	Total     int              `json:"total,omitempty"`
	Criteria  []judgeCriterion `json:"criteria,omitempty"`
	Error     string           `json:"error,omitempty"`
	Usage     map[string]int   `json:"usage,omitempty"`
	Elapsed   int64            `json:"elapsed_ms,omitempty"`
	Reason    string           `json:"unavailable_reason,omitempty"`
}

type judgeCriterion struct {
	Ordinal  int    `json:"ordinal"`
	Covered  bool   `json:"covered"`
	Evidence string `json:"evidence,omitempty"`
	Missing  string `json:"missing,omitempty"`
}

// artifactScore replaces the schema-1 validator echo. A committed run whose plan
// misses the seams scores low; a refused run whose body was recoverable still
// gets scored, with the body's provenance recorded.
type artifactScore struct {
	Scored         bool               `json:"scored"`
	BodyProvenance string             `json:"body_provenance,omitempty"`
	Deterministic  deterministicScore `json:"deterministic"`
	Judge          judgeScore         `json:"judge"`
	Unscorable     string             `json:"unscorable_reason,omitempty"`
}

var (
	// acLabelRE finds acceptance-criterion LABELS only so the oracle can report
	// a plan that is all labels and no substance. A label never scores.
	acLabelRE = regexp.MustCompile(`(?im)\bAC\s*#?\s*\d+\b`)
	// testIdentRE finds Go test function names mentioned in prose.
	testIdentRE = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9_]*`)
	testFileRE  = regexp.MustCompile(`[\w./-]*_test\.go`)
	// sectionRE splits a markdown plan on headings. Headings are BOUNDARIES for
	// per-criterion attribution, never evidence of coverage.
	sectionRE = regexp.MustCompile(`(?m)^#{1,6}\s`)
)

// treeIndex is the ground truth the oracle checks claims against: which paths
// exist in the seeded tree and which identifiers are actually declared in it.
type treeIndex struct {
	files     map[string]bool
	symbols   map[string]bool
	testFiles map[string]bool
	testFuncs map[string]bool
}

// indexTree parses every Go file in the seeded tree and collects its declared
// top-level identifiers. A plan that names a symbol the tree does not declare
// has not reached the seam, whatever it labelled the section.
func indexTree(f fixture) treeIndex {
	idx := treeIndex{
		files: map[string]bool{}, symbols: map[string]bool{},
		testFiles: map[string]bool{}, testFuncs: map[string]bool{},
	}
	fset := token.NewFileSet()
	for path, body := range f.treeFiles {
		idx.files[path] = true
		if strings.HasSuffix(path, "_test.go") {
			idx.testFiles[path] = true
		}
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, body, parser.SkipObjectResolution)
		if err != nil {
			continue // an unparseable fixture file is caught by fixtures_test
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				idx.symbols[d.Name.Name] = true
				if strings.HasPrefix(d.Name.Name, "Test") {
					idx.testFuncs[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						idx.symbols[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, name := range s.Names {
							idx.symbols[name.Name] = true
						}
					}
				}
			}
		}
	}
	return idx
}

// scoreArtifact is the deterministic oracle. It takes the plan body, the fixture
// (for its expected seams) and the tree index (for ground truth).
func scoreArtifact(body string, f fixture, idx treeIndex) deterministicScore {
	score := deterministicScore{Total: len(f.Acceptance)}
	score.LabelsFound = len(acLabelRE.FindAllString(body, -1))
	sections := splitSections(body)
	for i := range f.Acceptance {
		ordinal := i + 1
		seam, ok := f.seamFor(ordinal)
		if !ok {
			score.Criteria = append(score.Criteria, oracleCriterion{
				Ordinal: ordinal, Evidence: "fixture declares no expected seam for this criterion",
			})
			continue
		}
		score.Criteria = append(score.Criteria, scoreCriterion(ordinal, seam, sections, idx))
	}
	for _, c := range score.Criteria {
		if c.Covered {
			score.Covered++
		}
	}
	if score.Total > 0 {
		score.Fraction = float64(score.Covered) / float64(score.Total)
	}
	score.OK = score.Total > 0 && score.Covered == score.Total
	// A plan that labels every criterion but reaches no seam is the exact
	// degeneracy the schema-1 validator echo could not see.
	score.LabelOnly = score.LabelsFound >= score.Total && score.Covered == 0 && score.Total > 0
	return score
}

func scoreCriterion(ordinal int, seam expectedSeam, sections []string, idx treeIndex) oracleCriterion {
	finding := oracleCriterion{Ordinal: ordinal}
	whole := strings.Join(sections, "\n")
	for _, want := range seam.Files {
		if idx.files[want] && strings.Contains(whole, want) {
			finding.FilesHit = append(finding.FilesHit, want)
		} else {
			finding.FilesMissed = append(finding.FilesMissed, want)
		}
	}
	for _, want := range seam.Symbols {
		if idx.symbols[want] && mentionsSymbol(whole, want) {
			finding.SymbolsHit = append(finding.SymbolsHit, want)
		} else {
			finding.SymbolsMissed = append(finding.SymbolsMissed, want)
		}
	}
	// The test signal is attributed per criterion: the named test must appear in
	// the same section as one of this criterion's file or symbol hits.
	finding.TestNamed = namedTestNear(sections, finding, idx)
	finding.Covered = len(finding.FilesHit) > 0 && len(finding.SymbolsHit) > 0 && finding.TestNamed != ""
	finding.Evidence = criterionEvidence(finding)
	return finding
}

// mentionsSymbol requires a word-boundary match so "Write" does not score on
// "Written" or on prose that merely uses the English word.
func mentionsSymbol(body, symbol string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(symbol) + `\b`)
	return re.MatchString(body)
}

// namedTestNear returns the test the plan named for this criterion, verified to
// exist in the seeded tree, or "" when the plan named none. Proximity is the
// markdown section: a test named in the same section as a seam hit is evidence
// for THAT criterion, not for the plan generally.
func namedTestNear(sections []string, finding oracleCriterion, idx treeIndex) string {
	if len(finding.FilesHit) == 0 && len(finding.SymbolsHit) == 0 {
		return ""
	}
	for _, section := range sections {
		if !anchoredIn(section, finding) {
			continue
		}
		for _, path := range testFileRE.FindAllString(section, -1) {
			if idx.testFiles[strings.TrimPrefix(path, "./")] {
				return path
			}
		}
		for _, ident := range testIdentRE.FindAllString(section, -1) {
			if idx.testFuncs[ident] {
				return ident
			}
		}
		// A plan proposing a NEW test names a _test.go path that the tree does
		// not yet have. That is legitimate substance: the path must still sit in
		// a directory the tree really has.
		for _, path := range testFileRE.FindAllString(section, -1) {
			if idx.files[strings.TrimSuffix(path, "_test.go")+".go"] || dirExists(idx, path) {
				return path
			}
		}
	}
	return ""
}

// anchoredIn reports whether a section actually discusses this criterion's seam.
// A file path matches as a substring, but a SYMBOL must match on word
// boundaries: "Write" appears inside "TestExecuteWritesOnce", and treating that
// as a seam mention would let a test named anywhere in the plan cover any
// criterion — exactly the per-plan attribution this function exists to prevent.
func anchoredIn(section string, finding oracleCriterion) bool {
	for _, path := range finding.FilesHit {
		if strings.Contains(section, path) {
			return true
		}
	}
	for _, symbol := range finding.SymbolsHit {
		if mentionsSymbol(section, symbol) {
			return true
		}
	}
	return false
}

func dirExists(idx treeIndex, path string) bool {
	dir := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		dir = path[:i+1]
	} else {
		return false
	}
	for existing := range idx.files {
		if strings.HasPrefix(existing, dir) {
			return true
		}
	}
	return false
}

func criterionEvidence(f oracleCriterion) string {
	if f.Covered {
		return fmt.Sprintf("reached %s via %s, proven by %s",
			strings.Join(f.FilesHit, "+"), strings.Join(f.SymbolsHit, "+"), f.TestNamed)
	}
	var missing []string
	if len(f.FilesHit) == 0 {
		missing = append(missing, "named no expected file ("+strings.Join(f.FilesMissed, ", ")+")")
	}
	if len(f.SymbolsHit) == 0 {
		missing = append(missing, "named no declared symbol ("+strings.Join(f.SymbolsMissed, ", ")+")")
	}
	if f.TestNamed == "" {
		missing = append(missing, "named no test in the same section as a seam hit")
	}
	return strings.Join(missing, "; ")
}

func splitSections(body string) []string {
	idx := sectionRE.FindAllStringIndex(body, -1)
	if len(idx) == 0 {
		return []string{body}
	}
	var sections []string
	if idx[0][0] > 0 {
		sections = append(sections, body[:idx[0][0]])
	}
	for i, loc := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		sections = append(sections, body[loc[0]:end])
	}
	return sections
}

// runJudge asks an independent binding for a per-criterion verdict. It is
// optional by design: the deterministic oracle is the primary score, and the
// judge must never be the binding under test (enforced by the caller).
func runJudge(ctx context.Context, j judgeBinding, f fixture, body string, timeout time.Duration) judgeScore {
	score := judgeScore{BindingID: j.ID, Model: j.Model}
	runner, err := agentcli.RunnerFromBinding(j.Interface, j.Command)
	if err != nil || runner == nil {
		score.Reason = "judge binding has no runnable command"
		if err != nil {
			score.Reason = err.Error()
		}
		return score
	}
	payload, _ := json.Marshal(map[string]any{
		"acceptance_criteria": f.acceptanceLines(),
		"expected_seams":      f.Seams,
		"plan":                body,
	})
	system := "You judge whether an implementation plan covers each numbered acceptance criterion with SUBSTANCE " +
		"— named files, named symbols, named tests — not with a restated label. " +
		`Return exactly {"criteria":[{"ordinal":N,"covered":true|false,"evidence":"…","missing":"…"}]} and no narration.`
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, runErr := runner.Run(ctx, agentcli.Request{
		SystemPrompt: system, Payload: string(payload),
		AllowedTools: j.Tools, Model: j.Model, Effort: j.Effort,
	})
	score.Elapsed = time.Since(start).Milliseconds()
	if runErr != nil {
		score.Error = runErr.Error()
		return score
	}
	inner, usage := agentcli.UnwrapUsage(out)
	if usage.Available {
		score.Usage = map[string]int{
			"tokens_in": usage.InputTokens, "tokens_out": usage.OutputTokens,
			"tokens_total": usage.TotalTokens,
		}
	}
	var decoded struct {
		Criteria []judgeCriterion `json:"criteria"`
	}
	if err := json.Unmarshal([]byte(extractJSONObject(string(inner))), &decoded); err != nil {
		score.Error = "judge output is not the declared verdict object: " + err.Error()
		return score
	}
	sort.Slice(decoded.Criteria, func(i, k int) bool {
		return decoded.Criteria[i].Ordinal < decoded.Criteria[k].Ordinal
	})
	score.Criteria = decoded.Criteria
	score.Total = len(f.Acceptance)
	for _, c := range decoded.Criteria {
		if c.Covered {
			score.Covered++
		}
	}
	score.Available = len(decoded.Criteria) > 0
	if !score.Available {
		score.Reason = "judge returned no per-criterion verdicts"
	}
	return score
}

// extractJSONObject pulls the outermost {...} out of a reply that may carry
// fences or stray prose around it.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return s
	}
	return s[start : end+1]
}
