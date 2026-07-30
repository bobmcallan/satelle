//go:build plannerbench

package plannerbench

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentartifact"
)

const evidenceSchemaVersion = 1

type bindingEvidence struct {
	Name          string `json:"name"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Effort        string `json:"effort,omitempty"`
	Interface     string `json:"interface,omitempty"`
	ToolPolicy    string `json:"tool_policy,omitempty"`
	AgentsTOMLSHA string `json:"agents_toml_sha256"`
}

type environmentEvidence struct {
	GOOS           string            `json:"goos"`
	GOARCH         string            `json:"goarch"`
	HarnessVersion string            `json:"harness_version"`
	SatelleBinary  string            `json:"satelle_binary"`
	SatelleVersion string            `json:"satelle_version,omitempty"`
	SkillSHA       string            `json:"planner_skill_sha256"`
	WorkflowSHA    string            `json:"workflow_sha256"`
	Settings       map[string]string `json:"settings,omitempty"`
}

type textEvidence struct {
	Available  bool   `json:"available"`
	Redacted   string `json:"redacted,omitempty"`
	Provenance string `json:"provenance"`
	SHA256     string `json:"sha256,omitempty"`
}

type attachmentEvidence struct {
	OK     bool   `json:"ok"`
	Body   string `json:"body,omitempty"`
	Error  string `json:"error,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type criterionFinding struct {
	Ordinal int      `json:"ordinal"`
	ID      string   `json:"id"`
	OK      bool     `json:"ok"`
	Reasons []string `json:"reasons"`
}

type artifactScore struct {
	OK       bool               `json:"ok"`
	Findings []criterionFinding `json:"criteria"`
}

type usageEvidence struct {
	Available   bool   `json:"available"`
	TokensIn    *int   `json:"tokens_in,omitempty"`
	TokensOut   *int   `json:"tokens_out,omitempty"`
	TokensTotal *int   `json:"tokens_total,omitempty"`
	Provenance  string `json:"provenance"`
}

type diagnosticEvidence struct {
	Class         string `json:"class,omitempty"`
	Detail        string `json:"detail,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	LedgerExcerpt string `json:"ledger_excerpt,omitempty"`
}

type runRecord struct {
	SchemaVersion          int                 `json:"schema_version"`
	RunID                  string              `json:"run_id"`
	Variant                string              `json:"variant"`
	Fixture                string              `json:"fixture"`
	Run                    int                 `json:"run"`
	Binding                bindingEvidence     `json:"binding"`
	Environment            environmentEvidence `json:"environment"`
	StartedAt              time.Time           `json:"started_at"`
	FinishedAt             time.Time           `json:"finished_at"`
	WallMS                 int64               `json:"wall_ms"`
	TransitionOK           bool                `json:"transition_ok"`
	ReadOnlyPolicyFaithful bool                `json:"read_only_policy_faithful"`
	RawFinalResult         textEvidence        `json:"raw_final_result"`
	Attachment             attachmentEvidence  `json:"attachment"`
	ArtifactScore          artifactScore       `json:"artifact_score"`
	Usage                  usageEvidence       `json:"usage"`
	Diagnostics            diagnosticEvidence  `json:"diagnostics"`
	ContentHashes          map[string]string   `json:"content_hashes"`
	InfrastructureFailure  bool                `json:"infrastructure_failure"`
}

func newRunRecord(variant, fixture string, run int) runRecord {
	return runRecord{
		SchemaVersion: evidenceSchemaVersion,
		RunID:         fmt.Sprintf("%s__%s__%03d", safeName(variant), safeName(fixture), run),
		Variant:       variant, Fixture: fixture, Run: run,
		Environment: environmentEvidence{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			HarnessVersion: fmt.Sprintf("plannerbench-schema-%d", evidenceSchemaVersion),
		},
		ContentHashes: map[string]string{},
	}
}

func scorePlan(body string, acceptance []string) artifactScore {
	score := artifactScore{OK: true}
	contract := agentartifact.Contract{
		Name: "plan", Type: "plan", Required: true,
		RequiredFields: []string{"body"}, ACCoverage: true,
	}
	for i, criterion := range acceptance {
		ordinal := i + 1
		_, findings := agentartifact.ValidateAll(
			agentartifact.Artifact{Body: body}, contract,
			fmt.Sprintf("%d. %s", ordinal, criterion))
		finding := criterionFinding{Ordinal: ordinal, ID: fmt.Sprintf("AC%d", ordinal)}
		if len(findings) == 0 {
			finding.OK = true
			finding.Reasons = []string{"structural criterion heading found in attached artifact body"}
		} else {
			score.OK = false
			finding.Reasons = append([]string(nil), findings...)
		}
		score.Findings = append(score.Findings, finding)
	}
	if len(acceptance) == 0 {
		score.OK = false
	}
	return score
}

var (
	totalCostRE            = regexp.MustCompile(`(?m)^TOTAL\s+(\d+)\s+`)
	ledgerUsageAvailableRE = regexp.MustCompile(`"usage_available"\s*:\s*true`)
	ledgerTotalRE          = regexp.MustCompile(`"tokens_total"\s*:\s*(\d+)`)
	credentialRE           = regexp.MustCompile(`(?i)(bearer\s+|(?:api[_-]?key|auth[_-]?token|password)\s*[=:]\s*)[^\s"',;]+`)
)

func parseUsageEvidence(cost, ledger string) usageEvidence {
	if m := totalCostRE.FindStringSubmatch(cost); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		if n > 0 {
			return usageEvidence{Available: true, TokensTotal: intPtr(n), Provenance: "story-cost-total"}
		}
	}
	if ledgerUsageAvailableRE.MatchString(ledger) {
		n := 0
		if m := ledgerTotalRE.FindStringSubmatch(ledger); len(m) == 2 {
			n, _ = strconv.Atoi(m[1])
		}
		return usageEvidence{Available: true, TokensTotal: intPtr(n), Provenance: "ledger-agent-attempt"}
	}
	return usageEvidence{Available: false, Provenance: "transport-unreported"}
}

func intPtr(v int) *int { return &v }

func redactEvidence(s, home string) string {
	s = credentialRE.ReplaceAllString(s, "$1[REDACTED]")
	if home != "" {
		s = strings.ReplaceAll(s, home, "$HOME")
	}
	return s
}

func textRecord(s, provenance, home string) textEvidence {
	if strings.TrimSpace(s) == "" {
		return textEvidence{Available: false, Provenance: provenance}
	}
	s = redactEvidence(s, home)
	return textEvidence{
		Available: true, Redacted: s, Provenance: provenance, SHA256: digest(s),
	}
}

func parseAttachedBody(out string) (string, error) {
	var doc struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return "", fmt.Errorf("decode story document: %w", err)
	}
	if strings.TrimSpace(doc.Body) == "" {
		return "", fmt.Errorf("story document body is empty")
	}
	return doc.Body, nil
}

func writeRunEvidence(outDir string, record runRecord) error {
	runDir := filepath.Join(outDir, "runs")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runDir, record.RunID+".json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if record.RawFinalResult.Available {
		if err := os.WriteFile(filepath.Join(runDir, record.RunID+".raw.txt"),
			[]byte(record.RawFinalResult.Redacted), 0o644); err != nil {
			return err
		}
	}
	if record.Attachment.OK {
		if err := os.WriteFile(filepath.Join(runDir, record.RunID+".artifact.md"),
			[]byte(record.Attachment.Body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeAggregateEvidence(outDir string, records []runRecord) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "results.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	var md strings.Builder
	md.WriteString("# Planner benchmark evidence\n\n")
	md.WriteString("| Variant | Fixture | Run | Wall ms | Usage | Artifact | Policy | Diagnostic |\n")
	md.WriteString("|---|---|---:|---:|---:|---|---|---|\n")
	for _, record := range records {
		usage := "n/a"
		if record.Usage.Available && record.Usage.TokensTotal != nil {
			usage = strconv.Itoa(*record.Usage.TokensTotal)
		}
		fmt.Fprintf(&md, "| %s | %s | %d | %d | %s | %t | %t | %s |\n",
			record.Variant, record.Fixture, record.Run, record.WallMS, usage,
			record.ArtifactScore.OK, record.ReadOnlyPolicyFaithful, record.Diagnostics.Class)
	}
	return os.WriteFile(filepath.Join(outDir, "results.md"), []byte(md.String()), 0o644)
}

func evidenceProblems(records []runRecord, variants, fixtures []string, minimum int) []string {
	var problems []string
	for _, record := range records {
		if record.InfrastructureFailure {
			problems = append(problems, fmt.Sprintf("%s: %s: %s",
				record.RunID, record.Diagnostics.Class, record.Diagnostics.Detail))
		}
	}
	for _, variant := range variants {
		for _, fixture := range fixtures {
			count := 0
			for _, record := range records {
				if record.Variant == variant && record.Fixture == fixture &&
					record.TransitionOK && !record.InfrastructureFailure {
					count++
				}
			}
			if count < minimum {
				problems = append(problems, fmt.Sprintf(
					"under-sampled cell %s/%s: got %d comparable samples, want %d",
					variant, fixture, count, minimum))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum)
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
