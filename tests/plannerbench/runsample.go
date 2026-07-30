//go:build plannerbench

package plannerbench

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// benchWorkflow is the minimal workflow under study: one dispatched plan step on
// the [planner] binding the study writes. Keeping it minimal is deliberate — the
// study measures the planner, not this repo's gate stack.
const benchWorkflow = `---
name: planner-benchmark
type: workflow
description: isolated planner benchmark — one dispatched plan step
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

var (
	idRE       = regexp.MustCompile(`sty_[0-9a-f]+`)
	toolLineRE = regexp.MustCompile(`\ttool_start\t`)
)

// sampleEnv is everything a sample needs that does not vary per sample.
type sampleEnv struct {
	Bin          string
	PlannerSkill string
	Study        study
	Shipped      shippedGrant
	Settings     map[string]string
	JudgeTimeout time.Duration
}

// runSample executes one sample end to end and returns its record. It never
// panics or fails a test: an infrastructure fault becomes a classified record so
// the study can report it rather than losing the sample.
func runSample(root string, env sampleEnv, b studyBinding, f fixture, dims sampleDims) runRecord {
	record := newRunRecord(dims)
	record.Environment.SatelleBinary = env.Bin
	record.Environment.SkillSHA = digest(env.PlannerSkill)
	record.Environment.WorkflowSHA = digest(benchWorkflow)
	record.Environment.StudySHA = env.Study.sha
	record.Environment.ShippedGrant = env.Shipped
	record.Environment.Settings = env.Settings
	record.ContentHashes["fixture"] = f.digest()
	record.ContentHashes["agents_toml"] = digest(b.agentsTOML())
	record.ContentHashes["planner_skill"] = record.Environment.SkillSHA
	record.ContentHashes["study"] = env.Study.sha
	record.Policy = policyEvidence{
		MirrorsShipped: b.grant == env.Shipped.Grant, Divergence: b.divergence,
	}
	record.StartedAt = time.Now().UTC()

	finish := func(sig runSignals, rawResult, provenance string) runRecord {
		record.FinishedAt = time.Now().UTC()
		record.Timing.WallMS = record.FinishedAt.Sub(record.StartedAt).Milliseconds()
		record.RawFinalResult = textRecord(rawResult, provenance, os.Getenv("HOME"))
		record.ContentHashes["raw_final_result"] = record.RawFinalResult.SHA256
		record.Diagnostics, record.InfrastructureFailure = classifyRun(sig)
		return record
	}

	// Binary resolution is a filesystem fact known BEFORE the run — the spawn
	// class, decided without reading any program output.
	if reason := b.unavailableReason(); reason != "" {
		record.Diagnostics = diagnosticEvidence{Class: classSpawn, Signal: reason}
		record.InfrastructureFailure = true
		record.FinishedAt = record.StartedAt
		return record
	}

	home := filepath.Join(root, "satelle-home")
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return finish(runSignals{SetupError: err}, "", "harness-setup")
	}
	if err := f.materialize(repo); err != nil {
		return finish(runSignals{SetupError: fmt.Errorf("materialize fixture tree: %w", err)}, "", "harness-setup")
	}
	cmdEnv := append(os.Environ(), "SATELLE_HOME="+home)

	if out, err := command(cmdEnv, repo, env.Bin, "init", "--no-workspace"); err != nil {
		return finish(runSignals{SetupError: fmt.Errorf("satelle init: %w", err)}, out, "harness-setup")
	}
	if err := replaceInFile(filepath.Join(repo, ".satelle", "satelle.toml"),
		"gate_create = true", "gate_create = false"); err != nil {
		return finish(runSignals{SetupError: err}, "", "harness-setup")
	}
	for path, body := range map[string]string{
		filepath.Join(repo, ".satelle", "agents.toml"):                       b.agentsTOML(),
		filepath.Join(repo, ".satelle", "skills", "plan.md"):                 env.PlannerSkill,
		filepath.Join(repo, ".satelle", "workflows", "planner-benchmark.md"): benchWorkflow,
	} {
		if err := writeFile(path, body); err != nil {
			return finish(runSignals{SetupError: err}, "", "harness-setup")
		}
	}
	if out, err := command(cmdEnv, repo, env.Bin, "reindex"); err != nil {
		return finish(runSignals{SetupError: fmt.Errorf("satelle reindex: %w", err)}, out, "harness-setup")
	}

	args := []string{"story", "create", "--category", "feature", "--title", f.Title, "--body", f.Body}
	for _, line := range f.acceptanceLines() {
		args = append(args, "--acceptance", line)
	}
	created, err := command(cmdEnv, repo, env.Bin, args...)
	if err != nil {
		return finish(runSignals{SetupError: fmt.Errorf("story create: %w", err)}, created, "harness-setup")
	}
	storyID := idRE.FindString(created)
	if storyID == "" {
		return finish(runSignals{SetupError: fmt.Errorf("story create output carries no story id")},
			created, "harness-setup")
	}
	before, err := productDigest(repo)
	if err != nil {
		return finish(runSignals{SetupError: fmt.Errorf("product digest: %w", err)}, "", "harness-setup")
	}

	// The transition. stdout is STREAMED rather than collected at exit so the
	// first byte can be stamped: wall time alone cannot separate a slow spawn
	// from a slow model.
	result := streamCommand(cmdEnv, repo, env.Bin, "story", "set", storyID, "--status", "plan")
	record.FinishedAt = time.Now().UTC()
	record.Timing.WallMS = record.FinishedAt.Sub(record.StartedAt).Milliseconds()
	if result.FirstByte != nil {
		record.Timing.StartupMS = int64Ptr(result.FirstByte.Sub(result.Started).Milliseconds())
	}
	record.TransitionOK = result.Err == nil

	// TTFE and tool counts come from the dispatch event log the engine writes —
	// normalized events with their own timestamps, so no polling is needed.
	if events, ok := readDispatchEvents(home, storyID); ok {
		if events.FirstAt != nil {
			record.Timing.TTFEMS = int64Ptr(events.FirstAt.Sub(record.StartedAt).Milliseconds())
			record.Timing.TTFESource = "dispatch-event-log"
		}
		record.Tools = toolEvidence{Available: true, Calls: events.ToolCalls, Source: "dispatch-event-log"}
		record.Accounting = isolatedAccounting(events.Total, "dispatch-event-log")
	} else {
		record.Tools = toolEvidence{Available: false, Source: "transport emitted no dispatch events"}
		record.Accounting = isolatedAccounting(0, "no dispatch event log")
		if result.FirstByte != nil {
			record.Timing.TTFEMS = int64Ptr(result.FirstByte.Sub(record.StartedAt).Milliseconds())
			record.Timing.TTFESource = "first-cli-stdout-byte-fallback"
		}
	}

	after, digestErr := productDigest(repo)
	if digestErr != nil {
		return finish(runSignals{SetupError: fmt.Errorf("post-run product digest: %w", digestErr)},
			result.Combined(), "satelle-cli-combined-output")
	}
	record.Policy.ReadOnlyFaithful = before == after

	// Body recovery. The attached document is preferred; a refused run falls back
	// to the executor log, so refusal no longer costs the study a scored sample.
	rawResult, provenance := result.Combined(), "satelle-cli-combined-output"
	if executorOut, err := findExecutorOutput(home, storyID); err == nil {
		rawResult, provenance = executorOut, "satelle-executor-log-final-output"
	}
	body, bodyProvenance := "", ""
	if planOut, planErr := command(cmdEnv, repo, env.Bin, "story", "doc", storyID, "plan"); planErr == nil {
		if parsed, parseErr := parseAttachedBody(planOut); parseErr == nil {
			body, bodyProvenance = parsed, "attached-story-document"
			record.Attachment = attachmentEvidence{OK: true, Body: parsed, SHA256: digest(parsed)}
			record.ContentHashes["attached_artifact"] = record.Attachment.SHA256
		} else {
			record.Attachment = attachmentEvidence{Error: parseErr.Error()}
		}
	} else {
		record.Attachment = attachmentEvidence{Error: strings.TrimSpace(planOut)}
	}
	if body == "" {
		if recovered, ok := recoverArtifactBody(rawResult); ok {
			body, bodyProvenance = recovered, "executor-log-artifact-envelope"
		}
	}
	record.Score = scoreSample(env, f, b.ID, body, bodyProvenance)

	ledgerRaw, _ := command(cmdEnv, repo, env.Bin, "ledger", "list", "--story", storyID)
	entries, ledgerErr := parseLedger(ledgerRaw)
	costRaw, _ := command(cmdEnv, repo, env.Bin, "story", "cost", storyID)
	record.Usage = aggregateUsage(entries, costRaw)
	record.Attempts = record.Usage.AttemptsTotal

	sig := runSignals{
		TransitionErr: result.Err, Entries: entries, LedgerErr: ledgerErr,
		BodyRecovered: body != "", DigestChanged: before != after,
	}
	record.Diagnostics, record.InfrastructureFailure = classifyRun(sig)
	if len(entries) > 0 {
		record.Diagnostics.LedgerExcerpt = bounded(redactEvidence(ledgerRaw, os.Getenv("HOME")), 4000)
	}
	if record.Diagnostics.Detail == "" && result.Err != nil {
		record.Diagnostics.Detail = bounded(redactEvidence(result.Stderr, os.Getenv("HOME")), 2000)
	}
	record.RawFinalResult = textRecord(rawResult, provenance, os.Getenv("HOME"))
	record.ContentHashes["raw_final_result"] = record.RawFinalResult.SHA256
	return record
}

// scoreSample runs the independent oracle. The deterministic seam oracle always
// runs, so a credential-less sample still carries a real quality signal. The
// judge runs only when the study declares one AND it is not the binding under
// test — a binding grading its own answer is not a second opinion.
func scoreSample(env sampleEnv, f fixture, bindingID, body, provenance string) artifactScore {
	score := artifactScore{BodyProvenance: provenance}
	if strings.TrimSpace(body) == "" {
		score.Unscorable = "no artifact body was obtained from the document or the executor log"
		return score
	}
	score.Scored = true
	score.Deterministic = scoreArtifact(body, f, indexTree(f))
	switch {
	case env.Study.Judge == nil:
		score.Judge = judgeScore{Reason: "study declares no independent judge binding"}
	case env.Study.Judge.ID == bindingID:
		score.Judge = judgeScore{
			BindingID: env.Study.Judge.ID,
			Reason:    "judge binding is the binding under test; a self-grade is not an independent oracle",
		}
	default:
		timeout := env.JudgeTimeout
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		score.Judge = runJudge(context.Background(), *env.Study.Judge, f, body, timeout)
	}
	return score
}

// dispatchEvents summarizes the normalized event stream one dispatch produced.
type dispatchEvents struct {
	FirstAt   *time.Time
	Total     int
	ToolCalls int
}

// readDispatchEvents parses the dispatch event log the engine writes under
// <home>/<repo-key>/logs/dispatch/. Each line is
// "<RFC3339Nano>\t<kind>\t<details>" (agentcli.FormatEvent).
func readDispatchEvents(home, storyID string) (dispatchEvents, bool) {
	var summary dispatchEvents
	found := false
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "dispatch-") || !strings.HasSuffix(name, storyID+".log") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			found = true
			summary.Total++
			if toolLineRE.MatchString(line) {
				summary.ToolCalls++
			}
			stamp, _, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			at, perr := time.Parse(time.RFC3339Nano, stamp)
			if perr != nil {
				continue
			}
			if summary.FirstAt == nil || at.Before(*summary.FirstAt) {
				at := at
				summary.FirstAt = &at
			}
		}
		return nil
	})
	return summary, found
}

// --- process helpers ---

type streamResult struct {
	Stdout    string
	Stderr    string
	Err       error
	Started   time.Time
	FirstByte *time.Time
}

func (r streamResult) Combined() string {
	if strings.TrimSpace(r.Stderr) == "" {
		return r.Stdout
	}
	return r.Stdout + "\n" + r.Stderr
}

// streamCommand runs a command capturing BOTH pipes and stamping the first
// stdout byte. stderr is still captured in full — moving off CombinedOutput must
// not lose the diagnostics detail.
func streamCommand(env []string, dir, bin string, args ...string) streamResult {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	result := streamResult{Started: time.Now()}
	stamper := &firstByteWriter{at: &result.FirstByte}
	cmd.Stdout = io.MultiWriter(stamper, &stdout)
	cmd.Stderr = &stderr
	result.Err = cmd.Run()
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	return result
}

// firstByteWriter stamps the wall time of the first byte written to it.
type firstByteWriter struct {
	at **time.Time
}

func (w *firstByteWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && *w.at == nil {
		now := time.Now()
		*w.at = &now
	}
	return len(p), nil
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

func replaceInFile(path, old, replacement string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(raw), old) {
		return fmt.Errorf("%s does not contain %q", path, old)
	}
	return writeFile(path, strings.Replace(string(raw), old, replacement, 1))
}
