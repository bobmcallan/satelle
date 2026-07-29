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
	AgentsTOML string
}

type result struct {
	Variant                string `json:"variant"`
	Fixture                string `json:"fixture"`
	Run                    int    `json:"run"`
	WallMS                 int64  `json:"wall_ms"`
	Tokens                 int    `json:"tokens"`
	TransitionOK           bool   `json:"transition_ok"`
	ArtifactCorrect        bool   `json:"artifact_correct"`
	ReadOnlyPolicyFaithful bool   `json:"read_only_policy_faithful"`
	FailureObservability   string `json:"failure_observability"`
	Error                  string `json:"error,omitempty"`
}

var idRE = regexp.MustCompile(`sty_[0-9a-f]+`)
var totalRE = regexp.MustCompile(`(?m)^TOTAL\s+(\d+)\s+`)

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
			Name: "claude-command",
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
			Name: "grok-acp",
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

	var results []result
	for _, v := range variants {
		if only := os.Getenv("SATELLE_PLANNER_BENCH_VARIANT"); only != "" && only != v.Name {
			continue
		}
		for _, f := range loadFixtures(t) {
			if only := os.Getenv("SATELLE_PLANNER_BENCH_FIXTURE"); only != "" && only != f.Name {
				continue
			}
			for run := 1; run <= runs; run++ {
				t.Run(fmt.Sprintf("%s/%s/%d", v.Name, f.Name, run), func(t *testing.T) {
					r := runCase(t, bin, string(plannerSkill), v, f, run)
					results = append(results, r)
					// Persist after every costly call so an outer timeout or later
					// peer failure cannot erase already measured evidence.
					writeEvidence(t, results)
					t.Logf("wall=%s tokens=%d artifact=%v policy=%v failure-observability=%s err=%s",
						time.Duration(r.WallMS)*time.Millisecond, r.Tokens,
						r.ArtifactCorrect, r.ReadOnlyPolicyFaithful, r.FailureObservability, r.Error)
				})
			}
		}
	}
	if len(results) == 0 {
		t.Fatal("benchmark filters selected no variant/fixture cases")
	}
	// Quality/policy failures are benchmark findings, not harness failures. They
	// remain explicit in the evidence and disqualify a variant under the
	// predeclared decision rule; the target itself fails only when it cannot run
	// or record evidence.
}

func runCase(t *testing.T, bin, plannerSkill string, v variant, f fixture, run int) result {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "SATELLE_HOME="+filepath.Join(root, "satelle-home"))
	mustCommand(t, env, repo, bin, "init", "--no-workspace")
	replace(t, filepath.Join(repo, ".satelle", "satelle.toml"), "gate_create = true", "gate_create = false")
	write(t, filepath.Join(repo, ".satelle", "agents.toml"), v.AgentsTOML)
	write(t, filepath.Join(repo, ".satelle", "skills", "plan.md"), plannerSkill)
	write(t, filepath.Join(repo, ".satelle", "workflows", "planner-benchmark.md"), workflow)
	mustCommand(t, env, repo, bin, "reindex")

	args := []string{"story", "create", "--category", "feature", "--title", f.Title, "--body", f.Body}
	for i, ac := range f.Acceptance {
		args = append(args, "--acceptance", fmt.Sprintf("%d. %s", i+1, ac))
	}
	created := mustCommand(t, env, repo, bin, args...)
	id := idRE.FindString(created)
	if id == "" {
		t.Fatalf("create output has no story id: %s", created)
	}
	before := productDigest(t, repo)
	start := time.Now()
	out, err := command(env, repo, bin, "story", "set", id, "--status", "plan")
	elapsed := time.Since(start)

	r := result{Variant: v.Name, Fixture: f.Name, Run: run, WallMS: elapsed.Milliseconds(), TransitionOK: err == nil}
	if err != nil {
		r.Error = strings.TrimSpace(out)
	}
	plan, planErr := command(env, repo, bin, "story", "doc", id, "plan")
	r.ArtifactCorrect = planErr == nil && planLooksComplete(plan, len(f.Acceptance))
	r.ReadOnlyPolicyFaithful = before == productDigest(t, repo)
	ledger, _ := command(env, repo, bin, "ledger", "list", "--story", id)
	switch {
	case err == nil:
		r.FailureObservability = "not-exercised"
	case strings.Contains(ledger, "agent-failure") || strings.Contains(ledger, "agent-retry"):
		r.FailureObservability = "structured-ledger"
	case strings.TrimSpace(out) != "":
		r.FailureObservability = "command-output-only"
	default:
		r.FailureObservability = "missing"
	}
	cost, _ := command(env, repo, bin, "story", "cost", id)
	if m := totalRE.FindStringSubmatch(cost); len(m) == 2 {
		r.Tokens, _ = strconv.Atoi(m[1])
	}
	return r
}

func planLooksComplete(plan string, acceptanceCount int) bool {
	if len(strings.TrimSpace(plan)) < 500 {
		return false
	}
	lower := strings.ToLower(plan)
	if !strings.Contains(lower, "test") {
		return false
	}
	for i := 1; i <= acceptanceCount; i++ {
		n := strconv.Itoa(i)
		if !strings.Contains(lower, "ac"+n) && !strings.Contains(lower, "ac "+n) &&
			!strings.Contains(lower, "criterion "+n) {
			return false
		}
	}
	return true
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

func writeEvidence(t *testing.T, results []result) {
	t.Helper()
	outDir := os.Getenv("SATELLE_PLANNER_BENCH_OUT")
	if outDir == "" {
		outDir = "out"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(outDir, "results.json"), string(raw)+"\n")
	var md strings.Builder
	md.WriteString("# Planner transport benchmark\n\n")
	md.WriteString("| Variant | Fixture | Run | Wall ms | Tokens | Artifact | Read-only | Failure observability | Error |\n")
	md.WriteString("|---|---|---:|---:|---:|---|---|---|---|\n")
	for _, r := range results {
		fmt.Fprintf(&md, "| %s | %s | %d | %d | %d | %t | %t | %s | %s |\n",
			r.Variant, r.Fixture, r.Run, r.WallMS, r.Tokens, r.ArtifactCorrect,
			r.ReadOnlyPolicyFaithful, r.FailureObservability, strings.ReplaceAll(r.Error, "|", "\\|"))
	}
	write(t, filepath.Join(outDir, "results.md"), md.String())
}

func command(env []string, dir, bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustCommand(t *testing.T, env []string, dir, bin string, args ...string) string {
	t.Helper()
	out, err := command(env, dir, bin, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", bin, strings.Join(args, " "), err, out)
	}
	return out
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func replace(t *testing.T, path, old, replacement string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	write(t, path, strings.Replace(string(raw), old, replacement, 1))
}

func productDigest(t *testing.T, root string) string {
	t.Helper()
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
		t.Fatal(err)
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return fmt.Sprintf("%x", sum)
}
