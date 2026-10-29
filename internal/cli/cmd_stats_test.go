package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/throughput"
	"github.com/bobmcallan/satelle/internal/workitem"
)

var statsClock = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// statsPRStep overlays the shipped [coded] step (in_progress) with pr_step, so a
// story on this repo's route has a second phase.
const statsPRStep = `[meta]
name = "step"
type = "workflow"
scope = "project"
description = "fixture: the coded step raises the pull request."

[coded]
status = "in_progress"
agent = "executor"
reviewers = ["satelle-story-intent-review"]
reviewer_agent = "reviewer"
requires = ["raised"]
pr_step = true
`

// statsStory is one seeded story: its category, when it was created (in hours
// before statsClock), and the statuses it moved through, each with its hour.
type statsStory struct {
	category string
	created  float64
	moves    []statsMove
}

type statsMove struct {
	ago      float64
	from, to string
}

func ago(h float64) time.Time { return statsClock.Add(-time.Duration(h * float64(time.Hour))) }

// seedStatsRepo writes a repo at root holding stories, each with its
// story_created and status_transition rows at the given times, and returns
// their ids in order. stepToml, when set, is the repo's own step catalogue.
func seedStatsRepo(t *testing.T, root, stepToml string, stories ...statsStory) []string {
	t.Helper()
	dataDir := filepath.Join(root, config.DefaultDataDir)
	if err := os.MkdirAll(filepath.Join(dataDir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, config.ConfigName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if stepToml != "" {
		if err := os.WriteFile(filepath.Join(dataDir, "workflows", "step.toml"), []byte(stepToml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(config.Config{}.ResolveDB(root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var ids []string
	for i, s := range stories {
		it, err := db.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: fmt.Sprintf("story %d", i), Category: s.category,
		}, ago(s.created))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, it.ID)
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: it.ID, Kind: ledger.KindStoryCreated, Payload: json.RawMessage(`{"status":"backlog"}`),
		}, ago(s.created)); err != nil {
			t.Fatal(err)
		}
		for _, m := range s.moves {
			p, _ := json.Marshal(map[string]string{"from": m.from, "to": m.to})
			if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
				StoryID: it.ID, Kind: ledger.KindStatusTransition, Body: m.from + " → " + m.to, Payload: p,
			}, ago(m.ago)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return ids
}

// seedStatsCost tags the story id in the repo at root and appends one measured
// dispatch row per usage payload.
func seedStatsCost(t *testing.T, root, id string, tags []string, usage ...map[string]any) {
	t.Helper()
	db, err := store.Open(config.Config{}.ResolveDB(root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Stories.Update(ctx, id, workitem.UpdateInput{Tags: &tags}, statsClock.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, u := range usage {
		p := map[string]any{"adapter": "claude", "usage_available": true}
		for k, v := range u {
			p[k] = v
		}
		raw, _ := json.Marshal(p)
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: id, Kind: ledger.KindAgentInvocation, Payload: raw}, statsClock.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
}

func statsLedgerCount(t *testing.T, root string) int {
	t.Helper()
	db, err := store.OpenReadOnly(config.Config{}.ResolveDB(root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	n, err := db.Ledger.Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// statsCostIDs are the stories statsFixture gives usage rows: alpha's story
// finished in the window, alpha's story finished before it, and beta's.
type statsCostIDs struct{ alphaIn, alphaOld, beta string }

var statsCost statsCostIDs

// statsFixture registers three repos: alpha on the shipped route (no PR step),
// beta whose own route marks a PR step, and gamma, which is not a satelle repo.
// The operator's [pricing] rates m-big (priceRatesToml).
func statsFixture(t *testing.T) (alpha, beta, gamma string) {
	t.Helper()
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")
	base := t.TempDir()
	alpha, beta, gamma = filepath.Join(base, "alpha"), filepath.Join(base, "beta"), filepath.Join(base, "gamma")
	if err := os.MkdirAll(gamma, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("SATELLE_HOME"), config.GlobalConfigName), []byte(priceRatesToml), 0o644); err != nil {
		t.Fatal(err)
	}
	days := func(d float64) float64 { return d * 24 }
	alphaIDs := seedStatsRepo(t, alpha, "",
		// Created before the window, finished inside it.
		statsStory{"feature", days(10), []statsMove{{days(2), "backlog", "in_progress"}, {days(1), "in_progress", "done"}}},
		statsStory{"epic-parent", days(3), []statsMove{{days(3) - 1, "backlog", "ready"}, {days(2), "ready", "merging"}, {days(2) - 1, "merging", "done"}}},
		// A parent container is a story, not an epic.
		statsStory{"parent", days(4), []statsMove{{days(4) - 1, "backlog", "ready"}, {days(3), "ready", "merging"}, {days(3) - 1, "merging", "done"}}},
		// Still open.
		statsStory{"feature", days(2), []statsMove{{days(1), "backlog", "in_progress"}}},
		// Finished before the window.
		statsStory{"feature", days(20), []statsMove{{days(19), "backlog", "in_progress"}, {days(18), "in_progress", "done"}}},
	)
	betaIDs := seedStatsRepo(t, beta, statsPRStep,
		statsStory{"feature", days(5), []statsMove{{days(4), "backlog", "in_progress"}, {days(1), "in_progress", "done"}}},
	)
	statsCost = statsCostIDs{alphaIn: alphaIDs[0], alphaOld: alphaIDs[4], beta: betaIDs[0]}
	// $3 + $1.50; actual fresh input is 2x its estimate.
	seedStatsCost(t, alpha, statsCost.alphaIn, []string{"estimate-fresh-input:500000"},
		map[string]any{"agent": "coder", "model_resolved": "m-big", "tokens_in_fresh": 1_000_000, "tokens_in": 1_000_000, "tokens_out": 100_000})
	seedStatsCost(t, alpha, statsCost.alphaOld, nil,
		map[string]any{"agent": "coder", "model_resolved": "m-big", "tokens_in_fresh": 7, "tokens_in": 7, "tokens_out": 7})
	// $0.60 + $0.75; actual output is half its estimate.
	seedStatsCost(t, beta, statsCost.beta, []string{"estimate-output:100000"},
		map[string]any{"agent": "reviewer", "model_resolved": "m-big", "tokens_in_fresh": 200_000, "tokens_in": 200_000, "tokens_out": 50_000})
	prevReg, prevNow := statsRegistry, statsNow
	statsRegistry = func() ([]string, error) { return []string{alpha, beta, gamma, alpha}, nil }
	statsNow = func() time.Time { return statsClock }
	t.Cleanup(func() { statsRegistry, statsNow = prevReg, prevNow })
	return alpha, beta, gamma
}

func TestStatsReportsPerRepoAndTotal(t *testing.T) {
	alpha, beta, _ := statsFixture(t)
	before := statsLedgerCount(t, alpha) + statsLedgerCount(t, beta)

	text, err := runRoot(t, "stats")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, text)
	}
	sections := strings.Split(text, "\n== ")
	if len(sections) != 5 {
		t.Fatalf("want a window line, three repo sections and a total, got:\n%s", text)
	}
	want := []struct {
		head  string
		lines []string
	}{
		{"repo alpha", []string{
			"finished: stories 2  epics 1",
			"2026-10-28  stories 1  epics 0",
			"2026-10-27  stories 0  epics 1",
			"2026-10-23  stories 0  epics 0",
			"phase 1 (start to PR step, or to terminal): median 25h0m0s  p90 216h0m0s  (3 items)",
			"phase 2 (PR step to terminal, or to release when the story ended at its PR): median n/a  p90 n/a  (0 items)",
			"in_progress      24h0m0s  (1 items)",
		}},
		{"repo beta", []string{
			"finished: stories 1  epics 0",
			"phase 1 (start to PR step, or to terminal): median 24h0m0s  p90 24h0m0s  (1 items)",
			"phase 2 (PR step to terminal, or to release when the story ended at its PR): median 72h0m0s  p90 72h0m0s  (1 items)",
		}},
		{"repo gamma", []string{
			"error: ",
			"finished: stories unavailable (repo unreadable: ",
			"phase 1 (start to PR step, or to terminal): median unavailable (repo unreadable: ",
		}},
		{"total (excluded, unreadable: gamma)", []string{
			"finished: stories 3  epics 1",
			"phase 2 (PR step to terminal, or to release when the story ended at its PR): median 72h0m0s  p90 72h0m0s  (1 items)",
		}},
	}
	for i, w := range want {
		sec := sections[i+1]
		if !strings.HasPrefix(sec, w.head) {
			t.Errorf("section %d = %q, want it to open %q", i+1, firstLine(sec), w.head)
			continue
		}
		for _, l := range w.lines {
			if !strings.Contains(sec, l) {
				t.Errorf("%s section missing %q:\n%s", w.head, l, sec)
			}
		}
	}

	if after := statsLedgerCount(t, alpha) + statsLedgerCount(t, beta); after != before {
		t.Errorf("ledger rows %d before, %d after: stats must write nothing", before, after)
	}
}

func firstLine(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }

// The JSON and the text are one report: re-rendering the decoded JSON gives the
// text byte for byte, and an unavailable figure is null with its reason.
func TestStatsJSONCarriesTheTextFigures(t *testing.T) {
	statsFixture(t)
	text, err := runRoot(t, "stats")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, text)
	}
	raw, err := runRoot(t, "stats", "--json", "--since", "7d")
	if err != nil {
		t.Fatalf("stats --json: %v\n%s", err, raw)
	}
	var rep throughput.Report
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	var again bytes.Buffer
	throughput.Text(&again, rep)
	if again.String() != text {
		t.Errorf("text from the JSON differs from the text output:\n--- json\n%s\n--- text\n%s", again.String(), text)
	}

	if len(rep.Repos) != 3 || rep.Repos[2].Name != "gamma" || rep.Repos[2].Error == "" {
		t.Fatalf("repos = %+v, want alpha, beta and an unreadable gamma", rep.Repos)
	}
	gamma := rep.Repos[2]
	if gamma.Stories.Value != nil || gamma.Stories.Reason == "" || gamma.Phase1.Median.Seconds != nil {
		t.Errorf("gamma figures = %+v / %+v, want null with a reason", gamma.Stories, gamma.Phase1.Median)
	}
	if !strings.Contains(raw, `"value": null`) || !strings.Contains(raw, `"seconds": null`) {
		t.Errorf("unavailable figures must encode as null:\n%s", raw)
	}
	alpha := rep.Repos[0]
	if !alpha.Phase2.Median.NotApplicable || alpha.Phase2.Median.Seconds != nil {
		t.Errorf("alpha phase 2 = %+v, want not applicable", alpha.Phase2.Median)
	}
	if rep.Total.Stories.Value == nil || *rep.Total.Stories.Value != 3 || len(rep.Total.Excluded) != 1 {
		t.Errorf("total = %+v, want 3 stories with gamma excluded", rep.Total)
	}
}

func costItem(t *testing.T, r throughput.RepoReport, id string) (throughput.StoryCost, bool) {
	t.Helper()
	for _, s := range r.Cost.Stories {
		if s.ID == id {
			return s, true
		}
	}
	return throughput.StoryCost{}, false
}

func spreadOf(r throughput.RepoReport, unit string) throughput.Spread {
	for _, s := range r.Spread {
		if s.Unit == unit {
			return s
		}
	}
	return throughput.Spread{}
}

func countIs(c throughput.Count, n int) bool { return c.Value != nil && *c.Value == n }

func usdIs(c throughput.Cost, usd float64) bool {
	return c.USD != nil && math.Abs(*c.USD-usd) < 1e-9
}

// Cost and estimate figures cover every registered repo, the total sums the
// readable ones with each item labelled by repo, an item finished before the
// window is absent, and the text carries the figures --json does.
func TestStatsCostAndEstimatesAcrossRepos(t *testing.T) {
	statsFixture(t)
	raw, err := runRoot(t, "stats", "--json", "--since", "7d")
	if err != nil {
		t.Fatalf("stats --json: %v\n%s", err, raw)
	}
	var rep throughput.Report
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	alpha, beta, gamma := rep.Repos[0], rep.Repos[1], rep.Repos[2]

	for _, sec := range []throughput.RepoReport{alpha, rep.Total} {
		s, ok := costItem(t, sec, statsCost.alphaIn)
		if !ok {
			t.Fatalf("%s cost has no item %s: %+v", sec.Name, statsCost.alphaIn, sec.Cost.Stories)
		}
		if !countIs(s.FreshInput, 1_000_000) || !countIs(s.CacheRead, 0) || !countIs(s.CacheWrite, 0) || !countIs(s.Output, 100_000) || !usdIs(s.Cost, 4.5) {
			t.Errorf("%s item %s usage = %+v, want 1000000 / 0 / 0 / 100000 at $4.50", sec.Name, s.ID, s.Usage)
		}
		if len(s.Estimates) != 1 || s.Estimates[0].Ratio == nil || *s.Estimates[0].Ratio != 2 {
			t.Errorf("%s item %s estimates = %+v, want fresh-input at 2.0", sec.Name, s.ID, s.Estimates)
		}
		if _, old := costItem(t, sec, statsCost.alphaOld); old {
			t.Errorf("%s cost carries %s, which finished before the window", sec.Name, statsCost.alphaOld)
		}
	}
	if s, ok := costItem(t, rep.Total, statsCost.alphaIn); !ok || s.Repo != "alpha" {
		t.Errorf("total item %s repo = %q, want alpha", statsCost.alphaIn, s.Repo)
	}
	if s, ok := costItem(t, rep.Total, statsCost.beta); !ok || s.Repo != "beta" || !usdIs(s.Cost, 1.35) {
		t.Errorf("total item %s = %+v, want beta's at $1.35", statsCost.beta, s)
	}
	if !usdIs(alpha.Cost.Cost, 4.5) || !usdIs(beta.Cost.Cost, 1.35) || !usdIs(rep.Total.Cost.Cost, 5.85) {
		t.Errorf("repo costs = %+v / %+v / %+v, want $4.50, $1.35 and a $5.85 total", alpha.Cost.Cost, beta.Cost.Cost, rep.Total.Cost.Cost)
	}
	if len(rep.Total.Cost.BySeat) != 2 || rep.Total.Cost.BySeat[0].Name != "coder" || !usdIs(rep.Total.Cost.BySeat[1].Cost, 1.35) {
		t.Errorf("total by seat = %+v, want coder $4.50 then reviewer $1.35", rep.Total.Cost.BySeat)
	}
	if len(rep.Total.Cost.ByModel) != 1 || rep.Total.Cost.ByModel[0].Name != "m-big" || !usdIs(rep.Total.Cost.ByModel[0].Cost, 5.85) {
		t.Errorf("total by model = %+v, want m-big at $5.85", rep.Total.Cost.ByModel)
	}

	// Four items finished across the readable repos; one ratio per unit.
	for unit, sp := range map[string]throughput.Spread{"fresh-input": spreadOf(rep.Total, "fresh-input"), "output": spreadOf(rep.Total, "output")} {
		if !countIs(sp.Stories, 1) || !countIs(sp.Unavailable, 3) || sp.Median.Value == nil {
			t.Errorf("total %s spread = %+v, want 1 item and 3 unavailable", unit, sp)
		}
	}
	if gamma.Cost.Cost.USD != nil || gamma.Cost.FreshInput.Value != nil || spreadOf(gamma, "output").Stories.Value != nil {
		t.Errorf("unreadable gamma cost = %+v, want unavailable", gamma.Cost)
	}

	text, err := runRoot(t, "stats", "--since", "7d")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, text)
	}
	for _, want := range []string{
		fmt.Sprintf("item %s \"story 0\" (repo alpha): fresh input 1000000  cache read 0  cache write 0  output 100000  cost $4.5000", statsCost.alphaIn),
		"estimate output       est 100000  actual 50000  actual÷estimate 0.50",
		"by seat reviewer     fresh input 200000",
		"by model m-big       fresh input 1200000  cache read 0  cache write 0  output 150000  cost $5.8500",
		"fresh-input  items 1  unavailable 3  median 2.00  p90 2.00  min 2.00  max 2.00",
		"cost (each finished item's own rows): fresh input unavailable (repo unreadable: ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}

	help, err := runRoot(t, "stats", "--help")
	if err != nil {
		t.Fatalf("stats --help: %v", err)
	}
	for _, want := range []string{"Cost:", "by seat (agent) and by", "Estimate:", "actual÷estimate", "spread"} {
		if !strings.Contains(help, want) {
			t.Errorf("stats --help does not name %q:\n%s", want, help)
		}
	}
}

func TestStatsRepoFlagAndSince(t *testing.T) {
	_, beta, _ := statsFixture(t)
	prev := statsFind
	statsFind = func(name string) ([]string, error) {
		if name == "beta" {
			return []string{beta}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { statsFind = prev })

	out, err := runRoot(t, "stats", "--repo", "beta", "--since", "2d")
	if err != nil {
		t.Fatalf("stats --repo beta: %v\n%s", err, out)
	}
	if strings.Contains(out, "repo alpha") || !strings.Contains(out, "== repo beta") ||
		!strings.Contains(out, "window 2026-10-27T12:00:00Z to 2026-10-29T12:00:00Z") {
		t.Errorf("want beta alone over two days:\n%s", out)
	}
	if _, err := runRoot(t, "stats", "--repo", "nope"); err == nil || !strings.Contains(err.Error(), "not in the workspace registry") {
		t.Errorf("an unregistered --repo must be refused, got %v", err)
	}
	for _, bad := range []string{"soon", "0d", "-1h"} {
		if _, err := runRoot(t, "stats", "--since", bad); err == nil {
			t.Errorf("--since %q must be refused", bad)
		}
	}
}

// statsAppend writes one ledger row for story id in the repo at root.
func statsAppend(t *testing.T, db *store.DB, id, kind string, at time.Time, payload map[string]any) {
	t.Helper()
	p, _ := json.Marshal(payload)
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{StoryID: id, Kind: kind, Payload: p}, at); err != nil {
		t.Fatal(err)
	}
}

// A repo's queue entry and the release a task recorded against a story reach
// its cycle: beta's queued story measures phase 2 from its PR step to that
// release, and a story beside it with a release row but no queue entry keeps PR
// step to terminal. stats reports the queued figure.
func TestStatsPhaseTwoRunsToTheReleaseOfAQueuedStory(t *testing.T) {
	_, beta, _ := statsFixture(t)
	ctx := context.Background()
	days := func(d float64) float64 { return d * 24 }

	bdb, err := store.Open(config.Config{}.ResolveDB(beta))
	if err != nil {
		t.Fatal(err)
	}
	stray, err := bdb.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "unqueued", Category: "feature"}, ago(days(5)))
	if err != nil {
		t.Fatal(err)
	}
	statsAppend(t, bdb, stray.ID, ledger.KindStoryCreated, ago(days(5)), map[string]any{"status": "backlog"})
	statsAppend(t, bdb, stray.ID, ledger.KindStatusTransition, ago(days(4)), map[string]any{"from": "backlog", "to": "in_progress"})
	statsAppend(t, bdb, stray.ID, ledger.KindReleaseRecord, ago(days(4)-6), map[string]any{"release": "0.0.1", "commit": "abc"})
	statsAppend(t, bdb, stray.ID, ledger.KindStatusTransition, ago(days(1)), map[string]any{"from": "in_progress", "to": "done"})
	// beta's own story entered its PR step 4 days ago: it is queued there and a
	// task records its release 6h later.
	statsAppend(t, bdb, statsCost.beta, ledger.KindQueueEnqueued, ago(days(4)), map[string]any{"story": statsCost.beta, "state": "in_progress", "branch": "b", "head_sha": "abc"})
	statsAppend(t, bdb, statsCost.beta, ledger.KindReleaseRecord, ago(days(4)-6), map[string]any{"release": "0.0.1", "commit": "abc"})
	bdb.Close()

	repo, err := throughput.ReadRepo(ctx, beta, throughput.Window{Since: ago(days(7)), Until: statsClock}, throughput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	phase2 := map[string]string{}
	for _, it := range repo.Items {
		d, ok := it.Phases.Phase2()
		phase2[it.ID] = fmt.Sprintf("%v/%v/%v", it.Phases.Queued, d, ok)
	}
	want := map[string]string{statsCost.beta: "true/6h0m0s/true", stray.ID: "false/72h0m0s/true"}
	if fmt.Sprint(phase2) != fmt.Sprint(want) {
		t.Errorf("phase 2 per story (queued/figure/recorded) = %v, want %v", phase2, want)
	}

	out, err := runRoot(t, "stats")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, out)
	}
	if l := "phase 2 (PR step to terminal, or to release when the story ended at its PR): median 6h0m0s  p90 72h0m0s  (2 items)"; !strings.Contains(out, l) {
		t.Errorf("stats missing %q:\n%s", l, out)
	}
}

// The quality section is part of each repo's report and the total: beta's gate
// rejected its story once, and a story alpha created after beta's story was done
// cites it, so it came back in the total only. A cited story that was cancelled
// never came back.
func TestStatsQualitySection(t *testing.T) {
	alpha, beta, _ := statsFixture(t)
	ctx := context.Background()

	bdb, err := store.Open(config.Config{}.ResolveDB(beta))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := bdb.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory})
	if err != nil || len(listed) != 1 {
		t.Fatalf("beta stories = %v, %v; want one", listed, err)
	}
	betaID := listed[0].ID
	statsAppend(t, bdb, betaID, ledger.KindReviewReject, ago(24*3), map[string]any{"from": "backlog", "to": "in_progress", "attempt": "att_1", "skill": "satelle-story-intent-review"})
	statsAppend(t, bdb, betaID, ledger.KindReviewAccept, ago(24*3-1), map[string]any{"from": "backlog", "to": "in_progress", "attempt": "att_2", "skill": "satelle-story-intent-review"})
	gone, err := bdb.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "dropped", Category: "feature"}, ago(24*3))
	if err != nil {
		t.Fatal(err)
	}
	statsAppend(t, bdb, gone.ID, ledger.KindStoryCreated, ago(24*3), map[string]any{"status": "backlog"})
	statsAppend(t, bdb, gone.ID, ledger.KindStatusTransition, ago(24*3-1), map[string]any{"from": "backlog", "to": "in_progress"})
	statsAppend(t, bdb, gone.ID, ledger.KindStatusTransition, ago(24*2), map[string]any{"from": "in_progress", "to": workitem.StatusCancelled})
	bdb.Close()

	adb, err := store.Open(config.Config{}.ResolveDB(alpha))
	if err != nil {
		t.Fatal(err)
	}
	redo, err := adb.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "redo", Category: "feature",
		Body: "fix what " + betaID + " shipped; " + gone.ID + " was dropped",
	}, ago(12))
	if err != nil {
		t.Fatal(err)
	}
	statsAppend(t, adb, redo.ID, ledger.KindStoryCreated, ago(12), map[string]any{"status": "backlog"})
	adb.Close()

	text, err := runRoot(t, "stats")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, text)
	}
	sections := strings.Split(text, "\n== ")
	if len(sections) != 5 {
		t.Fatalf("want a window line, three repo sections and a total, got:\n%s", text)
	}
	for i, want := range [][]string{
		{"work that came back: 0\n"},
		{
			"in_progress                          0/1 (0%)  accepted: none  rejected: " + betaID,
			"satelle-story-intent-review          0/1 (0%)  accepted: none  rejected: " + betaID,
			"backlog → in_progress        mean 1.0  max 1  (1 stories)  at max: " + betaID,
			"coder re-dispatches after an exit rejection: 0  stories: none",
			"rejects naming no criterion: 1 of 1  stories: " + betaID,
		},
		{"first-pass acceptance by step:\n    unavailable (repo unreadable: "},
		{"work that came back: 1\n    " + redo.ID + " cites " + betaID + "\n"},
	} {
		for _, l := range want {
			if !strings.Contains(sections[i+1], l) {
				t.Errorf("section %q missing %q:\n%s", firstLine(sections[i+1]), l, sections[i+1])
			}
		}
	}

	raw, err := runRoot(t, "stats", "--json")
	if err != nil {
		t.Fatalf("stats --json: %v\n%s", err, raw)
	}
	var rep throughput.Report
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	var again bytes.Buffer
	throughput.Text(&again, rep)
	if again.String() != text {
		t.Errorf("text from the JSON differs from the text output:\n--- json\n%s\n--- text\n%s", again.String(), text)
	}
	bq := rep.Repos[1].Quality
	if len(bq.FirstPassByStep.Rows) != 1 || fmt.Sprint(bq.FirstPassByStep.Rows[0].RejectedStories) != "["+betaID+"]" {
		t.Errorf("beta first pass = %+v, want %s rejected", bq.FirstPassByStep, betaID)
	}
	if fmt.Sprint(bq.NoCriterion.Stories) != "["+betaID+"]" {
		t.Errorf("beta no-criterion stories = %v", bq.NoCriterion.Stories)
	}
	cb := rep.Total.Quality.CameBack
	if cb.Count.Value == nil || *cb.Count.Value != 1 || len(cb.Pairs) != 1 || cb.Pairs[0] != (throughput.CameBackPair{Story: redo.ID, Cites: betaID}) {
		t.Errorf("total came back = %+v, want %s citing %s", cb, redo.ID, betaID)
	}
	if !strings.Contains(raw, `"quality"`) || !strings.Contains(raw, `"came_back"`) {
		t.Errorf("JSON missing the quality section:\n%s", raw)
	}
}
