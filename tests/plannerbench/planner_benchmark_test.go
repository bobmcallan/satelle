//go:build plannerbench

// Package plannerbench is an opt-in live benchmark. It is intentionally outside
// default CI: each live case spends model tokens. Fixture/schema checks still run
// when the tag is selected without SATELLE_PLANNER_BENCH=1.
package plannerbench

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	Name       string   `json:"name"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Acceptance []string `json:"acceptance"`
}

type variant struct {
	Name       string
	Provider   string
	Model      string
	Effort     string
	Interface  string
	ToolPolicy string
	AgentsTOML string
}

var idRE = regexp.MustCompile(`sty_[0-9a-f]+`)

const workflow = `---
name: planner-benchmark
type: workflow
description: isolated live planner transport benchmark
applies_to: ["feature"]
scope: project
---

` + "```dot" + `
digraph planner_benchmark {
  backlog [shape=Mdiamond]
  plan [agent=planner, prompt="@skill:plan"]
  done [shape=Msquare]
  backlog -> plan -> done
}
` + "```\n"

func TestFixturesAreRepresentative(t *testing.T) {
	for _, f := range loadFixtures(t) {
		if f.Name == "" || f.Title == "" || len(f.Body) < 40 || len(f.Acceptance) < 3 {
			t.Errorf("fixture is not representative: %+v", f)
		}
	}
}

func TestLivePlannerTransportBenchmark(t *testing.T) {
	if os.Getenv("SATELLE_PLANNER_BENCH") != "1" {
		t.Skip("set SATELLE_PLANNER_BENCH=1 (or run make planner-bench); this spends live model tokens")
	}
	bin := os.Getenv("SATELLE_BIN")
	if bin == "" {
		t.Fatal("SATELLE_BIN must name the built satelle binary")
	}
	skillPath := os.Getenv("SATELLE_PLANNER_SKILL")
	if skillPath == "" {
		skillPath = filepath.Join("..", "..", ".satelle", "skills", "plan.md")
	}
	plannerSkill, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read production planner skill %s: %v", skillPath, err)
	}
	runs := 3
	if raw := os.Getenv("SATELLE_PLANNER_BENCH_RUNS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("SATELLE_PLANNER_BENCH_RUNS=%q: want a positive integer", raw)
		}
		runs = n
	}

	variants := []variant{
		{
			Name: "claude-command", Provider: "anthropic", Model: "opus",
			Effort: "high", Interface: "command", ToolPolicy: "read-only",
			AgentsTOML: `[planner]
role = "agent"
effort = "high"
command = "claude -p --output-format json --disallowedTools Write,Edit,NotebookEdit --append-system-prompt {system} --allowedTools {tools} --model {model} --effort {effort}"
tools = "Read,Grep,Glob,Bash(satelle:*)"
model = "opus"
principles = "session"
`,
		},
		{
			Name: "grok-acp", Provider: "xai", Model: "grok-4.5",
			Effort: "high", Interface: "acp", ToolPolicy: "read-only",
			AgentsTOML: `[planner]
role = "agent"
effort = "high"
interface = "acp"
command = "grok agent stdio"
tools = "read_file,grep,list_dir,Bash(satelle:*)"
model = "grok-4.5"
principles = "session"
`,
		},
	}

	minimum := runs
	if raw := os.Getenv("SATELLE_PLANNER_BENCH_MIN_SAMPLES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("SATELLE_PLANNER_BENCH_MIN_SAMPLES=%q: want a positive integer", raw)
		}
		minimum = n
	}
	outDir := benchmarkOutDir()
	var (
		results          []runRecord
		selectedVariants []string
		selectedFixtures []string
	)
	for _, v := range variants {
		if only := os.Getenv("SATELLE_PLANNER_BENCH_VARIANT"); only != "" && only != v.Name {
			continue
		}
		selectedVariants = append(selectedVariants, v.Name)
		for _, f := range loadFixtures(t) {
			if only := os.Getenv("SATELLE_PLANNER_BENCH_FIXTURE"); only != "" && only != f.Name {
				continue
			}
			if !containsString(selectedFixtures, f.Name) {
				selectedFixtures = append(selectedFixtures, f.Name)
			}
			for run := 1; run <= runs; run++ {
				t.Run(fmt.Sprintf("%s/%s/%d", v.Name, f.Name, run), func(t *testing.T) {
					r := runCase(t, bin, string(plannerSkill), v, f, run)
					results = append(results, r)
					// The full record and raw/artifact sidecars land before the
					// aggregate, so later interruption cannot erase this sample.
					if err := writeRunEvidence(outDir, r); err != nil {
						t.Fatalf("write durable run evidence: %v", err)
					}
					if err := writeAggregateEvidence(outDir, results); err != nil {
						t.Fatalf("write aggregate evidence: %v", err)
					}
					t.Logf("wall=%s usage=%v artifact=%v policy=%v diagnostic=%s",
						time.Duration(r.WallMS)*time.Millisecond, r.Usage.Available,
						r.ArtifactScore.OK, r.ReadOnlyPolicyFaithful, r.Diagnostics.Class)
				})
			}
		}
	}
	if len(results) == 0 {
		t.Fatal("benchmark filters selected no variant/fixture cases")
	}
	for _, problem := range evidenceProblems(results, selectedVariants, selectedFixtures, minimum) {
		t.Error(problem)
	}
}

func runCase(t *testing.T, bin, plannerSkill string, v variant, f fixture, run int) runRecord {
	t.Helper()
	record := newRunRecord(v.Name, f.Name, run)
	record.StartedAt = time.Now().UTC()
	record.Binding = bindingEvidence{
		Name: v.Name, Provider: v.Provider, Model: v.Model, Effort: v.Effort,
		Interface: v.Interface, ToolPolicy: v.ToolPolicy,
		AgentsTOMLSHA: digest(v.AgentsTOML),
	}
	record.Environment.SatelleBinary = bin
	record.Environment.SkillSHA = digest(plannerSkill)
	record.Environment.WorkflowSHA = digest(workflow)
	record.Environment.Settings = map[string]string{
		"runs":           os.Getenv("SATELLE_PLANNER_BENCH_RUNS"),
		"variant_filter": os.Getenv("SATELLE_PLANNER_BENCH_VARIANT"),
		"fixture_filter": os.Getenv("SATELLE_PLANNER_BENCH_FIXTURE"),
	}
	if version, err := command(os.Environ(), ".", bin, "version"); err == nil {
		record.Environment.SatelleVersion = strings.TrimSpace(version)
	}
	record.ContentHashes["fixture"] = digest(f.Title + "\n" + f.Body + "\n" + strings.Join(f.Acceptance, "\n"))
	record.ContentHashes["agents_toml"] = record.Binding.AgentsTOMLSHA
	record.ContentHashes["planner_skill"] = record.Environment.SkillSHA

	failInfra := func(class, detail, raw string) runRecord {
		record.FinishedAt = time.Now().UTC()
		if !record.StartedAt.IsZero() {
			record.WallMS = record.FinishedAt.Sub(record.StartedAt).Milliseconds()
		}
		record.InfrastructureFailure = true
		record.Diagnostics = diagnosticEvidence{Class: class, Detail: detail}
		record.RawFinalResult = textRecord(raw, "satelle-cli-combined-output", os.Getenv("HOME"))
		record.ContentHashes["raw_final_result"] = record.RawFinalResult.SHA256
		return record
	}

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return failInfra("setup", err.Error(), "")
	}
	env := append(os.Environ(), "SATELLE_HOME="+filepath.Join(root, "satelle-home"))
	if out, err := command(env, repo, bin, "init", "--no-workspace"); err != nil {
		return failInfra("setup", "satelle init: "+err.Error(), out)
	}
	if err := replaceFile(filepath.Join(repo, ".satelle", "satelle.toml"), "gate_create = true", "gate_create = false"); err != nil {
		return failInfra("setup", err.Error(), "")
	}
	for path, body := range map[string]string{
		filepath.Join(repo, ".satelle", "agents.toml"):                       v.AgentsTOML,
		filepath.Join(repo, ".satelle", "skills", "plan.md"):                 plannerSkill,
		filepath.Join(repo, ".satelle", "workflows", "planner-benchmark.md"): workflow,
	} {
		if err := writeFile(path, body); err != nil {
			return failInfra("setup", err.Error(), "")
		}
	}
	if out, err := command(env, repo, bin, "reindex"); err != nil {
		return failInfra("setup", "satelle reindex: "+err.Error(), out)
	}

	args := []string{"story", "create", "--category", "feature", "--title", f.Title, "--body", f.Body}
	for i, ac := range f.Acceptance {
		args = append(args, "--acceptance", fmt.Sprintf("%d. %s", i+1, ac))
	}
	created, err := command(env, repo, bin, args...)
	if err != nil {
		return failInfra("setup", "story create: "+err.Error(), created)
	}
	id := idRE.FindString(created)
	if id == "" {
		return failInfra("setup", "create output has no story id", created)
	}
	before, err := productDigest(repo)
	if err != nil {
		return failInfra("setup", "product digest: "+err.Error(), "")
	}
	out, transitionErr := command(env, repo, bin, "story", "set", id, "--status", "plan")
	record.FinishedAt = time.Now().UTC()
	record.WallMS = record.FinishedAt.Sub(record.StartedAt).Milliseconds()
	record.TransitionOK = transitionErr == nil
	rawResult, provenance := out, "satelle-cli-combined-output"
	if executorResult, err := findExecutorOutput(filepath.Join(root, "satelle-home"), id); err == nil {
		rawResult, provenance = executorResult, "satelle-executor-log-final-output"
	}
	record.RawFinalResult = textRecord(rawResult, provenance, os.Getenv("HOME"))
	record.ContentHashes["raw_final_result"] = record.RawFinalResult.SHA256

	after, digestErr := productDigest(repo)
	if digestErr != nil {
		return failInfra("setup", "post-run product digest: "+digestErr.Error(), out)
	}
	record.ReadOnlyPolicyFaithful = before == after
	if !record.ReadOnlyPolicyFaithful {
		record.Diagnostics = diagnosticEvidence{Class: "denied_mutation", Detail: "product tree digest changed"}
	}

	planOut, planErr := command(env, repo, bin, "story", "doc", id, "plan")
	if planErr == nil {
		body, bodyErr := parseAttachedBody(planOut)
		if bodyErr == nil {
			record.Attachment = attachmentEvidence{OK: true, Body: body, SHA256: digest(body)}
			record.ContentHashes["attached_artifact"] = record.Attachment.SHA256
			record.ArtifactScore = scorePlan(body, f.Acceptance)
		} else {
			record.Attachment = attachmentEvidence{Error: bodyErr.Error()}
		}
	} else {
		record.Attachment = attachmentEvidence{Error: strings.TrimSpace(planOut)}
	}

	ledger, _ := command(env, repo, bin, "ledger", "list", "--story", id)
	cost, _ := command(env, repo, bin, "story", "cost", id)
	record.Usage = parseUsageEvidence(cost, ledger)
	if transitionErr != nil {
		class, infrastructure := classifyRunFailure(out)
		record.Diagnostics = diagnosticEvidence{
			Class: class, Detail: strings.TrimSpace(out), LedgerExcerpt: bounded(ledger, 4000),
		}
		record.InfrastructureFailure = infrastructure
	} else if !record.Attachment.OK {
		record.Diagnostics = diagnosticEvidence{Class: "attachment", Detail: record.Attachment.Error}
		record.InfrastructureFailure = true
	}
	return record
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	raw, err := os.ReadFile("fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []fixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < 3 {
		t.Fatalf("want at least 3 fixtures, got %d", len(fixtures))
	}
	return fixtures
}

func benchmarkOutDir() string {
	outDir := os.Getenv("SATELLE_PLANNER_BENCH_OUT")
	if outDir == "" {
		outDir = "out"
	}
	return outDir
}

func command(env []string, dir, bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func replaceFile(path, old, replacement string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(raw), old) {
		return fmt.Errorf("%s does not contain %q", path, old)
	}
	return writeFile(path, strings.Replace(string(raw), old, replacement, 1))
}

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
		if d.IsDir() && (rel == ".satelle" || rel == ".git" || rel == ".claude" || rel == ".grok" || rel == ".codex") {
			return filepath.SkipDir
		}
		if d.IsDir() || rel == ".gitignore" {
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

func classifyRunFailure(out string) (string, bool) {
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline exceeded"):
		return "timeout", true
	case strings.Contains(lower, "auth"), strings.Contains(lower, "unauthorized"):
		return "auth", true
	case strings.Contains(lower, "executable file not found"), strings.Contains(lower, "spawn"):
		return "spawn", true
	case strings.Contains(lower, "artifact attachment"):
		return "attachment", true
	case strings.Contains(lower, "structured output"), strings.Contains(lower, "no structured"),
		strings.Contains(lower, "artifact.body"):
		return "malformed_output", false
	default:
		return "execution", true
	}
}

func bounded(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

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
