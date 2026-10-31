package substratereview_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/reviewscore"
	"github.com/bobmcallan/satelle/internal/substratereview"
)

// replayRoot holds the substrate-review replay cases, scored with
// `satelle review score --replay internal/substratereview/testdata/replay`.
const replayRoot = "testdata/replay"

// Every substrate-review case loads through LoadReplay: one per directive (the
// six AC2 rules) plus the mixed-six fixture (AC4), each declaring its expected
// dispositions, its manifest's {{case_dir}} paths resolved to files that exist,
// and a judgement naming exactly those dispositions scoring OK while a
// keep-everything judgement misses every cut.
func TestSubstrateReplayCasesLoad(t *testing.T) {
	cases, err := reviewscore.LoadReplay(replayRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"remove-covered": 1, "remove-unread": 1, "reduce-history": 1, "reduce-methodology": 1,
		"conflict-code": 1, "keep-specific": 1, "mixed-six": 6}
	seen := map[string]bool{}
	for _, c := range cases {
		seen[c.ID] = true
		if c.Rubric != "satelle-substrate-review" || c.Skill != "satelle-substrate-review" {
			t.Errorf("%s: rubric %q skill %q", c.ID, c.Rubric, c.Skill)
		}
		if n, ok := want[c.ID]; !ok || len(c.ExpectedDispositions) != n {
			t.Errorf("%s declares %d expected disposition(s), want %d", c.ID, len(c.ExpectedDispositions), want[c.ID])
		}
		if c.Payload == nil || len(c.Payload.Docs) != 1 {
			t.Fatalf("%s carries no manifest document", c.ID)
		}
		body := c.Payload.Docs[0].Body
		if strings.Contains(body, reviewscore.CaseDirToken) {
			t.Errorf("%s: {{case_dir}} was not substituted", c.ID)
		}
		var m substratereview.Manifest
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("%s: the manifest does not decode: %v", c.ID, err)
		}
		var right, keepAll substratereview.Judgement
		for _, it := range m.Items {
			for _, p := range []string{it.Path, it.HistoryPath, it.DefaultPath} {
				if p == "" {
					continue
				}
				if _, err := os.Stat(p); err != nil || !filepath.IsAbs(p) {
					t.Errorf("%s: %s points at %s, which is not an absolute existing file", c.ID, it.Item, p)
				}
			}
			d, ok := c.ExpectedDispositions[it.Item]
			if !ok {
				t.Errorf("%s: %s has no expected disposition", c.ID, it.Item)
			}
			right.Items = append(right.Items, substratereview.ItemJudgement{Item: it.Item, Disposition: d, Reason: "r", Evidence: "e", Replacement: "x"})
			keepAll.Items = append(keepAll.Items, substratereview.ItemJudgement{Item: it.Item, Disposition: "keep", Reason: "r", Evidence: "e"})
		}
		// keep-all misses unless every expected disposition is keep.
		allKeep := true
		for _, d := range c.ExpectedDispositions {
			allKeep = allKeep && d == substratereview.DispKeep
		}
		for name, tc := range map[string]struct {
			j    substratereview.Judgement
			want string
		}{
			"right":    {right, reviewscore.OutcomeOK},
			"keep-all": {keepAll, map[bool]string{true: reviewscore.OutcomeOK, false: reviewscore.OutcomeDispositionMiss}[allKeep]},
		} {
			notes, _ := json.Marshal(tc.j)
			judge := reviewscore.JudgeFunc(func(context.Context, reviewscore.Case) (reviewscore.JudgeResult, error) {
				return reviewscore.JudgeResult{Verdict: reviewscore.VerdictAccept, Notes: string(notes)}, nil
			})
			rep := reviewscore.Score(context.Background(), []reviewscore.Case{c}, judge, reviewscore.Options{Dispositions: substratereview.Dispositions})
			if got := rep.Cases[0].Outcome; got != tc.want {
				t.Errorf("%s: a %s judgement scores %q, want %q", c.ID, name, got, tc.want)
			}
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("no substrate-review replay case %s", id)
		}
	}
}
