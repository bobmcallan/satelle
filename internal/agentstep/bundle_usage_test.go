package agentstep

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// sty_23e10d92 AC6: one bundled invocation is one measured call. Its usage rides
// the FIRST verdict only; the rest carry none, so summing a bundle's verdicts
// never counts the call twice. Each transport reports usage its own way, so the
// claim is checked against captured output from each.

const bundleVerdictsText = `{"verdicts":[` +
	`{"skill":"rev-a","decision":"accept","notes":"a"},` +
	`{"skill":"rev-b","decision":"reject","notes":"b"},` +
	`{"skill":"rev-c","decision":"accept","notes":"c"}]}`

// fixtureOutput reads a captured adapter output and swaps its reply text for the
// bundled verdicts, keeping the usage and model fields exactly as captured.
func fixtureOutput(t *testing.T, name, textKey string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "agentcli", "testdata", "usage", name))
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	env[textKey] = bundleVerdictsText
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// streamRunner is a transport that reports usage itself (the stream transports),
// rather than through an envelope in its output.
type streamRunner struct {
	bundleRunner
	usage agentcli.UsageResult
}

func (s *streamRunner) RunUsage(ctx context.Context, req agentcli.Request) ([]byte, agentcli.UsageResult, error) {
	out, err := s.Run(ctx, req)
	return out, s.usage, err
}

func bundleUsageGate(t *testing.T, r agentcli.Runner) []reviewerRow {
	t.Helper()
	docs := fakeDocs{workflow: bundleEdgeWF("rev-a,rev-b,rev-c", "bundle = true"), extraSkills: bundleSkillDocs("rev-a", "rev-b", "rev-c")}
	dec, err := bundleGate(t, New(r, docs, "/repo", ""))
	if err != nil {
		t.Fatal(err)
	}
	var rows []reviewerRow
	for _, rv := range dec.Reviewers {
		rows = append(rows, reviewerRow{
			skill: rv.Skill, id: rv.BundleID, avail: rv.UsageAvailable, total: rv.TokensTotal, in: rv.TokensIn,
			out: rv.TokensOut, cost: rv.CostUSD, model: rv.ModelResolved, reason: rv.UsageUnavailableReason,
			costReason: rv.CostUnavailableReason, models: len(rv.Models),
		})
	}
	return rows
}

type reviewerRow struct {
	skill, id, model, reason, costReason string
	avail                                bool
	total, in, out, models               int
	cost                                 *float64
}

// assertOneMeasuredCall checks rows are three verdicts of one bundle whose usage
// is on the first alone, and returns that first row.
func assertOneMeasuredCall(t *testing.T, rows []reviewerRow) reviewerRow {
	t.Helper()
	if len(rows) != 3 {
		t.Fatalf("want 3 verdicts, got %+v", rows)
	}
	lead := rows[0]
	for _, r := range rows {
		if r.id == "" || r.id != lead.id {
			t.Errorf("one session, one bundle id: %+v", rows)
		}
		if r.model != lead.model {
			t.Errorf("every verdict names the session's model %q, got %q", lead.model, r.model)
		}
	}
	for _, r := range rows[1:] {
		if r.avail || r.total != 0 || r.in != 0 || r.out != 0 || r.cost != nil || r.models != 0 {
			t.Errorf("only the first verdict carries the call's usage, got %+v", r)
		}
	}
	return lead
}

func TestBundleUsage_ClaudeCommandEnvelope(t *testing.T) {
	r := &bundleRunner{raw: fixtureOutput(t, "claude_result.json", "result")}
	rows := bundleUsageGate(t, r)
	if r.calls() != 1 {
		t.Fatalf("calls = %d", r.calls())
	}
	lead := assertOneMeasuredCall(t, rows)
	if !lead.avail || lead.total != 22+11000+2500+300 || lead.out != 300 {
		t.Errorf("lead must carry the measured usage: %+v", lead)
	}
	if lead.model != "claude-opus-5-5" || lead.cost == nil || *lead.cost != 0.1 {
		t.Errorf("lead model/cost = %q / %v", lead.model, lead.cost)
	}
	var sum int
	for _, r := range rows {
		sum += r.total
	}
	if sum != lead.total {
		t.Errorf("a bundle's verdicts sum to the ONE measured call: %d vs %d", sum, lead.total)
	}
}

func TestBundleUsage_GrokCommandEnvelope(t *testing.T) {
	r := &bundleRunner{raw: fixtureOutput(t, "grok_json.json", "text")}
	rows := bundleUsageGate(t, r)
	lead := assertOneMeasuredCall(t, rows)
	if !lead.avail || lead.total != 6971 || lead.out != 24 {
		t.Errorf("lead must carry the captured grok usage: %+v", lead)
	}
	if lead.model != "grok-4.5-build" || lead.cost == nil {
		t.Errorf("lead model/cost = %q / %v", lead.model, lead.cost)
	}
}

func TestBundleUsage_GrokCommandWithNoUsageIsUnavailableNotZero(t *testing.T) {
	r := &bundleRunner{raw: fixtureOutput(t, "grok_json_nousage.json", "text")}
	rows := bundleUsageGate(t, r)
	lead := assertOneMeasuredCall(t, rows)
	if lead.avail {
		t.Errorf("no usage object means unavailable, never a measured zero: %+v", lead)
	}
	if lead.reason == "" {
		t.Errorf("the adapter's reason must ride the row: %+v", lead)
	}
	if lead.cost != nil || lead.costReason == "" {
		t.Errorf("unavailable cost is nil with a reason: %+v", lead)
	}
}

func TestBundleUsage_StreamTransportReportsItsOwnUsage(t *testing.T) {
	cost := 0.25
	r := &streamRunner{
		bundleRunner: bundleRunner{raw: bundleVerdictsText},
		usage: agentcli.UsageResult{
			Available: true, CacheSplitAvailable: true,
			InputTokens: 9000, FreshInputTokens: 1000, CacheCreationInputTokens: 2000, CacheReadInputTokens: 6000,
			OutputTokens: 400, TotalTokens: 9400, CostUSD: &cost, ModelResolved: "claude-opus-5-5",
			Models: []agentcli.ModelUsage{{ID: "claude-opus-5-5", InputTokens: 9000, OutputTokens: 400}},
		},
	}
	rows := bundleUsageGate(t, r)
	lead := assertOneMeasuredCall(t, rows)
	if !lead.avail || lead.total != 9400 || lead.cost == nil || *lead.cost != 0.25 || lead.models != 1 {
		t.Errorf("stream usage on the first verdict: %+v", lead)
	}
}
