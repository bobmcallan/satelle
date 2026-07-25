//go:build llm

// Package llm holds opt-in rubric-judgment fixtures (sty_6830e78e AC5).
// These call a live reviewer model when SATELLE_LLM_JUDGMENT=1, cost tokens,
// and are intentionally excluded from `go test ./...` and `make integration`.
//
// Run at release time or on demand:
//
//	make judgment
//	SATELLE_LLM_JUDGMENT=1 go test -tags llm ./tests/llm/...
//
// Pass rule: best-of-three — a fixture passes when ≥2/3 live runs match the label.
// Without SATELLE_LLM_JUDGMENT=1, only fixture-structure checks run (no model call).
// The human half of this tier remains the re-runnable audit tasks
// (tsk_substrate-audit, tsk_reviewer-objective-audit, tsk_context-audit).
package llm

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fixture is one labelled accept/reject case for a gate skill.
type fixture struct {
	Name    string `json:"name"`
	Want    string `json:"want"` // accept | reject
	Why     string `json:"why"`
	Payload string `json:"payload"` // optional story JSON for the live harness
}

// requiredGates is the minimum LLM gate set that must carry fixtures.
var requiredGates = []string{
	"satelle-story-intent-review",
	"satelle-story-plan-review",
	"satelle-story-done-review",
	"satelle-story-scope-review",
	"satelle-story-cancel-review",
	"satelle-story-blocked-review",
	"satelle-workflow-change-review",
}

// TestJudgmentFixtureStructure validates the labelled fixture corpus without
// calling a model. Always runs under -tags llm (make judgment).
func TestJudgmentFixtureStructure(t *testing.T) {
	dir := fixturesDir(t)
	for _, gate := range requiredGates {
		path := filepath.Join(dir, gate+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing fixtures for %s: %v", gate, err)
			continue
		}
		var cases []fixture
		if err := json.Unmarshal(raw, &cases); err != nil {
			t.Errorf("%s: invalid JSON: %v", gate, err)
			continue
		}
		if len(cases) < 2 {
			t.Errorf("%s: need at least 2 fixtures (accept + reject), got %d", gate, len(cases))
		}
		hasAccept, hasReject := false, false
		for _, fc := range cases {
			if strings.TrimSpace(fc.Name) == "" || strings.TrimSpace(fc.Why) == "" {
				t.Errorf("%s: fixture missing name or why", gate)
			}
			switch strings.ToLower(fc.Want) {
			case "accept":
				hasAccept = true
			case "reject":
				hasReject = true
			default:
				t.Errorf("%s/%s: want must be accept|reject, got %q", gate, fc.Name, fc.Want)
			}
		}
		if !hasAccept || !hasReject {
			t.Errorf("%s: fixtures must cover both accept and reject", gate)
		}
	}
}

// TestJudgmentLive runs fixtures against the real reviewer when
// SATELLE_LLM_JUDGMENT=1. Skips otherwise so `make judgment` is safe by default.
func TestJudgmentLive(t *testing.T) {
	if os.Getenv("SATELLE_LLM_JUDGMENT") != "1" {
		t.Skip("set SATELLE_LLM_JUDGMENT=1 to spend tokens on live rubric judgment")
	}
	if !agentConfigured() {
		t.Fatal("SATELLE_LLM_JUDGMENT=1 but no claude/grok agent binary on PATH")
	}
	dir := fixturesDir(t)
	for _, gate := range requiredGates {
		raw, err := os.ReadFile(filepath.Join(dir, gate+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var cases []fixture
		if err := json.Unmarshal(raw, &cases); err != nil {
			t.Fatal(err)
		}
		t.Run(gate, func(t *testing.T) {
			for _, fc := range cases {
				fc := fc
				t.Run(fc.Name, func(t *testing.T) {
					matches := 0
					const n = 3
					var last string
					for i := 0; i < n; i++ {
						got := runLiveJudgment(t, gate, fc)
						last = got
						if strings.EqualFold(got, fc.Want) {
							matches++
						}
					}
					if matches < 2 {
						t.Fatalf("want ≥2/%d %q (why: %s); matched %d; last=%q", n, fc.Want, fc.Why, matches, last)
					}
				})
			}
		})
	}
}

func fixturesDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "fixtures")
}

func agentConfigured() bool {
	for _, bin := range []string{"claude", "grok"} {
		if _, err := exec.LookPath(bin); err == nil {
			return true
		}
	}
	return false
}

// runLiveJudgment invokes the configured agent with the gate skill as system
// context and the fixture payload, then parses a decision from the output.
// Operators may override with SATELLE_LLM_JUDGMENT_CMD (a shell command that
// receives gate name on argv and fixture JSON on stdin, prints decision).
func runLiveJudgment(t *testing.T, gate string, fc fixture) string {
	t.Helper()
	if cmd := os.Getenv("SATELLE_LLM_JUDGMENT_CMD"); cmd != "" {
		c := exec.Command("sh", "-c", cmd+" "+gate)
		c.Stdin = strings.NewReader(mustJSON(fc))
		out, err := c.CombinedOutput()
		if err != nil {
			t.Logf("judgment cmd err: %v\n%s", err, out)
		}
		return parseDecision(string(out))
	}
	// Default: ask grok/claude briefly — best-effort, not a full satelle gate run.
	bin := "grok"
	if _, err := exec.LookPath("claude"); err == nil {
		bin = "claude"
	}
	prompt := "You are the " + gate + " gate. Fixture: " + fc.Name + " (" + fc.Why + "). " +
		"Reply with ONLY a JSON object {\"decision\":\"accept\"|\"reject\",\"notes\":\"...\"}."
	var c *exec.Cmd
	if bin == "claude" {
		c = exec.Command("claude", "-p", prompt, "--output-format", "json")
	} else {
		c = exec.Command("grok", "-p", prompt)
	}
	out, err := c.CombinedOutput()
	if err != nil {
		t.Logf("%s err: %v\n%s", bin, err, out)
	}
	return parseDecision(string(out))
}

func mustJSON(fc fixture) string {
	b, _ := json.Marshal(fc)
	return string(b)
}

func parseDecision(out string) string {
	lower := strings.ToLower(out)
	// Prefer explicit JSON decision field
	if i := strings.Index(lower, `"decision"`); i >= 0 {
		rest := lower[i:]
		if strings.Contains(rest, "reject") {
			return "reject"
		}
		if strings.Contains(rest, "accept") {
			return "accept"
		}
	}
	if strings.Contains(lower, "reject") {
		return "reject"
	}
	if strings.Contains(lower, "accept") {
		return "accept"
	}
	return "unknown"
}
