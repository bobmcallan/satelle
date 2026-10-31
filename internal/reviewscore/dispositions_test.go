package reviewscore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseItems is a stand-in for the substrate review's parser: notes are a JSON
// object of item → disposition.
func parseItems(notes string) (map[string]string, error) {
	var m map[string]string
	err := json.Unmarshal([]byte(notes), &m)
	return m, err
}

// A case that declares expected dispositions is OK only when every one
// matches: a keep-everything judge misses a cut, an unparseable answer misses,
// the expected map scores OK.
func TestScoreDispositions(t *testing.T) {
	c := Case{ID: "remove-covered", Rubric: "satelle-substrate-review", Skill: "satelle-substrate-review", Label: LabelValid,
		ExpectedVerdict: VerdictAccept, ExpectedDispositions: map[string]string{"skills/x": "remove", "skills/y": "keep"}}
	score := func(notes string, parse func(string) (map[string]string, error)) Report {
		judge := JudgeFunc(func(context.Context, Case) (JudgeResult, error) {
			return JudgeResult{Verdict: VerdictAccept, Notes: notes}, nil
		})
		return Score(context.Background(), []Case{c}, judge, Options{Dispositions: parse})
	}
	for name, tc := range map[string]struct {
		notes string
		parse func(string) (map[string]string, error)
		want  string
	}{
		"keep everything":  {`{"skills/x":"keep","skills/y":"keep"}`, parseItems, OutcomeDispositionMiss},
		"item missing":     {`{"skills/y":"keep"}`, parseItems, OutcomeDispositionMiss},
		"unparseable":      {`not json`, parseItems, OutcomeDispositionMiss},
		"no parser":        {`{"skills/x":"remove","skills/y":"keep"}`, nil, OutcomeDispositionMiss},
		"every one agrees": {`{"skills/x":"remove","skills/y":"keep"}`, parseItems, OutcomeOK},
	} {
		rep := score(tc.notes, tc.parse)
		if got := rep.Cases[0].Outcome; got != tc.want {
			t.Errorf("%s: outcome %q, want %q", name, got, tc.want)
		}
		if miss := tc.want == OutcomeDispositionMiss; (rep.Total.DispositionMisses == 1) != miss {
			t.Errorf("%s: disposition misses = %d", name, rep.Total.DispositionMisses)
		}
	}
}

// A replay document's {{case_dir}} is the case's absolute directory once
// loaded.
func TestLoadReplaySubstitutesCaseDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "satelle-substrate-review", "c1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("case.json", `{"id":"c1","rubric":"satelle-substrate-review","skill":"satelle-substrate-review","label":"valid","story_id":"substrate-review","expected_verdict":"accept","summary":"s","expected_dispositions":{"skills/x":"remove"}}`)
	write("payload.json", `{"title":"t","no_patch":true,"docs":[{"name":"m","type":"document","body":"{\"path\":\"{{case_dir}}/x.md\"}"}]}`)
	cs, err := LoadReplay(root)
	if err != nil {
		t.Fatal(err)
	}
	body := cs[0].Payload.Docs[0].Body
	if strings.Contains(body, CaseDirToken) || !strings.Contains(body, dir+"/x.md") {
		t.Errorf("the doc body was not substituted: %s", body)
	}
	if cs[0].ExpectedDispositions["skills/x"] != "remove" {
		t.Errorf("expected_dispositions = %v", cs[0].ExpectedDispositions)
	}
}
