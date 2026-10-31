package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/substratereview"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// treeHashes is every file under repo with its sha256 — the tree a command
// must leave unchanged.
func treeHashes(t *testing.T, repo string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[strings.TrimPrefix(p, repo)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertTreeUnchanged(t *testing.T, stage string, before, after map[string]string) {
	t.Helper()
	for p, h := range before {
		if after[p] != h {
			t.Errorf("%s changed %s in the tree", stage, p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			t.Errorf("%s created %s in the tree", stage, p)
		}
	}
}

// substrateReviewFixture lays a repo holding one item of each origin the apply
// path distinguishes: a seeded copy of an embedded skill, a repo-authored skill,
// an embedded-only skill, and a skill the project tier owns. The constitution
// links three rules, and one story's ledger holds a rejection by the repo skill.
func substrateReviewFixture(t *testing.T) string {
	t.Helper()
	repo := tempRepo(t)
	writeRepoFile(t, repo, ".satelle/skills/repo-cut.md", "---\ntype: skill\nname: repo-cut\ndescription: a repo skill\n---\n# repo cut\n\nkeep me\n\ndrop me\n")
	writeRepoFile(t, repo, ".satelle/constitution.md", "# constitution\n\n- first: [[rule-a]]\n- second: [[rule-b]]\n- third: [[rule-c]]\n")
	if out, err := runRoot(t, "substrate", "edit", "skills", "satelle-panel-majority"); err != nil {
		t.Fatalf("substrate edit: %v\n%s", err, out)
	}

	// The project tier owns skills/team-skill.md.
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("[review]\ngate_create = false\n\n[hosted]\nproject = \"team-p\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	tier := config.ProjectTierDir(cfg.ResolveRuntimeDir(repo).Dir)
	writeRepoFile(t, tier, "skills/team-skill.md", "---\ntype: skill\nname: team-skill\ndescription: team skill\n---\n# team skill\n")
	stamp, _ := json.Marshal(config.ProjectTierStamp{Project: "team-p", TeamPaths: []string{"skills/team-skill.md"}, Files: []string{"skills/team-skill.md"}, DeployedAt: time.Now()})
	writeRepoFile(t, tier, config.ProjectTierStampName, string(stamp))

	if out, err := runRoot(t, "reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}
	out, err := runRoot(t, "story", "create", "--title", "History", "--body", "b", "--status", "backlog")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created workitem.Item
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	b, _ := json.Marshal(map[string]any{"from": "backlog", "to": "plan", "skill": "repo-cut", "attempt": "att_x", "notes": "too long", "model_resolved": "M1"})
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{StoryID: created.ID, Kind: ledger.KindReviewReject, Actor: "reviewer", Body: "reject", Payload: b}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return repo
}

// stubSubstrateJudge swaps the judge for one returning j, and records the input
// it was handed.
func stubSubstrateJudge(t *testing.T, j substratereview.Judgement) *substratereview.JudgeInput {
	t.Helper()
	prev := newSubstrateJudge
	t.Cleanup(func() { newSubstrateJudge = prev })
	var got substratereview.JudgeInput
	newSubstrateJudge = func(*cobra.Command, string) (substrateJudge, error) {
		return func(_ context.Context, in substratereview.JudgeInput) (substratereview.Judgement, error) {
			got = in
			return j, nil
		}, nil
	}
	return &got
}

func TestSubstrateReviewReportsEveryItemAndRuleAndAppliesChosenCuts(t *testing.T) {
	repo := substrateReviewFixture(t)
	input := stubSubstrateJudge(t, substratereview.Judgement{
		Items: []substratereview.ItemJudgement{
			{Item: "skills/satelle-panel-majority", Disposition: "remove", Reason: "the embedded default covers it"},
			{Item: "skills/repo-cut", Disposition: "reduce", KeptLines: []string{"keep me"}, Reason: "rejected once for length"},
			{Item: "skills/satelle-story-cancel-review", Disposition: "keep", Rule: "names the cancel reason"},
			{Item: "agents/executor", Disposition: "conflict", Rule: "satelle-agent-agnostic"},
			// skills/team-skill is omitted: it must read "not judged".
		},
		Constitution: []substratereview.RuleFinding{
			{Rule: "rule-a", File: "internal/x.go", Line: 7, Note: "names a vendor"},
			{Rule: "rule-b", None: true},
			// rule-c is omitted: it must read "not judged".
		},
	})
	scratch := t.TempDir()
	report := filepath.Join(scratch, "report.json")

	// AC1 + AC3: one verb, a report, no file in the tree changed.
	before := treeHashes(t, repo)
	out, err := runRoot(t, "substrate", "review", "--out", report)
	if err != nil {
		t.Fatalf("substrate review: %v\n%s", err, out)
	}
	assertTreeUnchanged(t, "substrate review", before, treeHashes(t, repo))

	origins := map[string]string{}
	for _, it := range input.Items {
		origins[it.Key] = it.Origin
	}
	for key, want := range map[string]string{
		"skills/satelle-panel-majority":      substratereview.OriginSeeded,
		"skills/repo-cut":                    substratereview.OriginRepo,
		"skills/satelle-story-cancel-review": substratereview.OriginEmbedded,
		"skills/team-skill":                  substratereview.OriginTeam,
	} {
		if origins[key] != want {
			t.Errorf("origin of %s = %q, want %q", key, origins[key], want)
		}
	}
	for _, kind := range []string{"principles/", "skills/", "workflows/default/", "agents/"} {
		found := false
		for key := range origins {
			found = found || strings.HasPrefix(key, kind)
		}
		if !found {
			t.Errorf("the inventory holds no %s item", kind)
		}
	}
	for _, want := range []string{
		"### skills/satelle-panel-majority — remove", "- origin: seeded",
		"### skills/repo-cut — reduce", "- kept: keep me", "- rejected: too long", "model M1: 0 invocation(s), 0 accept(s), 1 rejection(s)",
		"### skills/satelle-story-cancel-review — keep", "- rule: names the cancel reason", "- origin: embedded",
		"### agents/executor — conflict", "- rule: satelle-agent-agnostic",
		"### skills/team-skill — not judged", "- origin: team",
		"- rule-a (constitution:3): finding internal/x.go:7 — names a vendor",
		"- rule-b (constitution:4): no finding",
		"- rule-c (constitution:5): not judged",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatalf("the JSON report was not written: %v", err)
	}

	// --out inside the tree is refused, so the report cannot change the tree.
	if out, err := runRoot(t, "substrate", "review", "--out", filepath.Join(repo, "report.json")); err == nil || !strings.Contains(err.Error()+out, "inside the repo tree") {
		t.Errorf("--out inside the tree should be refused: %v\n%s", err, out)
	}

	// AC4: apply the chosen cuts from the saved report.
	before = treeHashes(t, repo)
	out, err = runRoot(t, "substrate", "review", "--from", report,
		"--apply", "skills/satelle-panel-majority", "--apply", "skills/repo-cut",
		"--apply", "skills/satelle-story-cancel-review", "--apply", "skills/team-skill")
	if err == nil {
		t.Errorf("apply with refused items should fail:\n%s", out)
	}
	assertTreeUnchanged(t, "substrate review --apply", before, treeHashes(t, repo))
	for _, want := range []string{
		"for skills/satelle-panel-majority (remove, origin seeded)",
		"for skills/repo-cut (reduce, origin repo)",
		"refused skills/satelle-story-cancel-review: origin embedded",
		"refused skills/team-skill: origin team",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("apply output lacks %q:\n%s", want, out)
		}
	}

	out, err = runRoot(t, "story", "list", "--tag", substratereview.ApplyTag)
	if err != nil {
		t.Fatalf("story list: %v\n%s", err, out)
	}
	var raised []workitem.Item
	if err := json.Unmarshal([]byte(out), &raised); err != nil {
		t.Fatalf("parse story list: %v\n%s", err, out)
	}
	if len(raised) != 2 {
		t.Fatalf("raised %d stories, want 2 (seeded and repo only):\n%s", len(raised), out)
	}
	bodies := map[string]workitem.Item{}
	for _, it := range raised {
		if it.Category != substratereview.ApplyCategory {
			t.Errorf("%s category = %q, want substrate", it.ID, it.Category)
		}
		for _, key := range []string{"skills/satelle-panel-majority", "skills/repo-cut"} {
			if strings.Contains(it.Title, key) {
				bodies[key] = it
			}
		}
	}
	seeded, repoCut := bodies["skills/satelle-panel-majority"], bodies["skills/repo-cut"]
	for _, c := range []struct {
		it   workitem.Item
		want []string
	}{
		{seeded, []string{"- origin: seeded", "- disposition: remove", "## Evidence", "dissent: not recorded"}},
		{repoCut, []string{"- origin: repo", "- disposition: reduce", "- kept: keep me", "- rejected: too long"}},
	} {
		for _, w := range c.want {
			if !strings.Contains(c.it.Body, w) {
				t.Errorf("story %q body lacks %q:\n%s", c.it.Title, w, c.it.Body)
			}
		}
	}
	if !strings.Contains(repoCut.AcceptanceCriteria, `keeps only these lines: "keep me"`) || !strings.Contains(repoCut.AcceptanceCriteria, "1 rejection(s)") {
		t.Errorf("repo-cut criteria should carry the kept lines and the evidence:\n%s", repoCut.AcceptanceCriteria)
	}
	if !strings.Contains(seeded.AcceptanceCriteria, "skills/satelle-panel-majority is removed") {
		t.Errorf("seeded criteria should carry the cut:\n%s", seeded.AcceptanceCriteria)
	}
}
