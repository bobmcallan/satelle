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
	"strings"
	"time"
)

type environmentEvidence struct {
	GOOS           string            `json:"goos"`
	GOARCH         string            `json:"goarch"`
	SatelleBinary  string            `json:"satelle_binary"`
	SatelleVersion string            `json:"satelle_version,omitempty"`
	SkillSHA       string            `json:"planner_skill_sha256"`
	WorkflowSHA    string            `json:"workflow_sha256"`
	StudySHA       string            `json:"study_sha256"`
	ShippedGrant   shippedGrant      `json:"shipped_planner_grant"`
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

// timingEvidence separates the three durations a transport comparison needs.
// wall_ms alone cannot distinguish a slow spawn from a slow model.
type timingEvidence struct {
	WallMS     int64  `json:"wall_ms"`
	StartupMS  *int64 `json:"startup_ms,omitempty"`
	TTFEMS     *int64 `json:"ttfe_ms,omitempty"`
	TTFESource string `json:"ttfe_source,omitempty"`
}

// toolEvidence counts the tool calls the transport emitted. Available is false
// when the transport emits no tool events at all — not zero, which would read as
// "the agent used no tools".
type toolEvidence struct {
	Available bool   `json:"available"`
	Calls     int    `json:"calls,omitempty"`
	Source    string `json:"source,omitempty"`
}

// policyEvidence records the read-only outcome and the shipped-grant fidelity of
// the binding that produced this sample (AC11).
type policyEvidence struct {
	ReadOnlyFaithful bool            `json:"read_only_faithful"`
	MirrorsShipped   bool            `json:"mirrors_shipped_grant"`
	Divergence       *toolDivergence `json:"tool_policy_divergence,omitempty"`
}

// runRecord is one sample. Schema 2 replaces the flat variant/fixture pair with
// the full sampleDims set, the independent oracle score, attempt-aggregated
// usage, structured diagnostics, and topology accounting.
type runRecord struct {
	SchemaVersion         int                 `json:"schema_version"`
	RunID                 string              `json:"run_id"`
	Dims                  sampleDims          `json:"dims"`
	Environment           environmentEvidence `json:"environment"`
	StartedAt             time.Time           `json:"started_at"`
	FinishedAt            time.Time           `json:"finished_at"`
	Timing                timingEvidence      `json:"timing"`
	Tools                 toolEvidence        `json:"tools"`
	Attempts              int                 `json:"attempts"`
	TransitionOK          bool                `json:"transition_ok"`
	Policy                policyEvidence      `json:"policy"`
	Accounting            topologyAccounting  `json:"topology_accounting"`
	RawFinalResult        textEvidence        `json:"raw_final_result"`
	Attachment            attachmentEvidence  `json:"attachment"`
	Score                 artifactScore       `json:"artifact_score"`
	Usage                 usageEvidence       `json:"usage"`
	Diagnostics           diagnosticEvidence  `json:"diagnostics"`
	ContentHashes         map[string]string   `json:"content_hashes"`
	InfrastructureFailure bool                `json:"infrastructure_failure"`
}

func newRunRecord(dims sampleDims) runRecord {
	return runRecord{
		SchemaVersion: evidenceSchemaVersion,
		RunID: fmt.Sprintf("%s__%s__%03d",
			safeName(dims.BindingID), safeName(dims.Fixture), dims.Run),
		Dims:          dims,
		Environment:   environmentEvidence{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		ContentHashes: map[string]string{},
	}
}

// comparable reports whether a sample may enter a comparison. A sample is
// comparable when the transport did not decide its outcome AND the oracle
// produced a score — a REFUSED-but-scored run is comparable, so a refusal no
// longer silently shrinks a cell.
func (r runRecord) comparable() bool {
	return !r.InfrastructureFailure && r.Score.Scored && len(r.Dims.validate()) == 0
}

var credentialRE = regexp.MustCompile(
	`(?i)(bearer\s+|(?:api[_-]?key|auth[_-]?token|password)\s*[=:]\s*)[^\s"',;]+`)

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
	return textEvidence{Available: true, Redacted: s, Provenance: provenance, SHA256: digest(s)}
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

// writeRunEvidence lands the durable per-sample files. The aggregate is written
// only afterwards, so an interruption cannot erase a completed sample.
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
	return os.WriteFile(filepath.Join(outDir, "results.json"), append(raw, '\n'), 0o644)
}

// readRunEvidence loads durable per-sample records so the report can be
// regenerated without spending a token (make planner-report).
func readRunEvidence(outDir string) ([]runRecord, error) {
	runDir := filepath.Join(outDir, "runs")
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return nil, err
	}
	var records []runRecord
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(runDir, name))
		if err != nil {
			return nil, err
		}
		var record runRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].RunID < records[j].RunID })
	return records, nil
}

// evidenceProblems reports what makes the STUDY invalid, as distinct from what a
// sample merely measured. A low artifact score is data; an infrastructure
// failure or an under-sampled cell is a defect in the run.
func evidenceProblems(records []runRecord, minimum int) []string {
	var problems []string
	counts := map[string]int{}
	for _, record := range records {
		if findings := record.Dims.validate(); len(findings) > 0 {
			problems = append(problems, fmt.Sprintf("%s: %s", record.RunID, strings.Join(findings, "; ")))
			continue
		}
		if record.SchemaVersion != evidenceSchemaVersion {
			problems = append(problems, fmt.Sprintf("%s: schema %d is not comparable with %d",
				record.RunID, record.SchemaVersion, evidenceSchemaVersion))
			continue
		}
		if record.InfrastructureFailure {
			problems = append(problems, fmt.Sprintf("%s: %s: %s",
				record.RunID, record.Diagnostics.Class, record.Diagnostics.Signal))
			continue
		}
		if record.comparable() {
			counts[record.Dims.cellKey()]++
		}
	}
	for _, key := range sortedCountKeys(counts) {
		if counts[key] < minimum {
			problems = append(problems, fmt.Sprintf(
				"under-sampled cell %s: got %d comparable samples, want %d", key, counts[key], minimum))
		}
	}
	sort.Strings(problems)
	return problems
}

func sortedCountKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

func bounded(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// productDigest hashes the repo's product tree, skipping the substrate and VCS
// dirs. With a SEEDED fixture tree this is a real read-only fidelity check; over
// the previous empty repo it could not fail.
func productDigest(root string) (string, error) {
	var entries []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch rel {
			case ".satelle", ".git", ".claude", ".grok", ".codex":
				return filepath.SkipDir
			}
			return nil
		}
		if rel == ".gitignore" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		entries = append(entries, fmt.Sprintf("%s:%x", rel, sum))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return fmt.Sprintf("%x", sum), nil
}

// findExecutorOutput recovers a run's final agent output from the executor log.
// It is the fallback body source that lets a REFUSED run still be scored.
func findExecutorOutput(root, storyID string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "executor.log" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, "\t"+storyID+"\t") {
				continue
			}
			if _, output, ok := strings.Cut(line, " — output: "); ok {
				found = strings.ReplaceAll(output, `\n`, "\n")
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(found) == "" {
		return "", fmt.Errorf("executor final output unavailable")
	}
	return found, nil
}

// recoverArtifactBody extracts a plan body from a raw agent final response, so a
// run whose attachment failed can still be scored by the oracle.
func recoverArtifactBody(raw string) (string, bool) {
	var envelope struct {
		Artifact struct {
			Body string `json:"body"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal([]byte(extractJSONObject(raw)), &envelope); err == nil &&
		strings.TrimSpace(envelope.Artifact.Body) != "" {
		return envelope.Artifact.Body, true
	}
	return "", false
}

func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }
