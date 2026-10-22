package agentartifact

import (
	"strings"
	"testing"
)

// sty_f948d345: the answers a performer gives the findings it was shown ride
// beside its artifact and changes, decoded once for every adapter.

func TestDecodeAnswers(t *testing.T) {
	const list = `"answers":[{"finding":"skill-a/seat-1#1","answer":"adopted","evidence":"reworked AC3"},{"finding":"skill-b#1","answer":"rebutted","evidence":"engine.go:1336 shows it"}]`
	for name, out := range map[string]string{
		"alone":         `{` + list + `}`,
		"with artifact": `{"artifact":{"name":"plan","type":"plan","body":"b"},` + list + `}`,
		"after prose":   "Done.\n```json\n{" + list + "}\n```\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeAnswers([]byte(out))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got[0].Finding != "skill-a/seat-1#1" || got[0].Answer != AnswerAdopted ||
				got[1].Answer != AnswerRebutted || got[1].Evidence != "engine.go:1336 shows it" {
				t.Errorf("answers = %+v", got)
			}
		})
	}
}

func TestDecodeAnswersAbsent(t *testing.T) {
	for _, out := range []string{``, `plain prose`, `{"artifact":{"name":"p","type":"plan","body":"b"}}`, `{"answers":[],"other":1}`} {
		got, err := DecodeAnswers([]byte(out))
		if err != nil || got != nil {
			t.Errorf("DecodeAnswers(%q) = %+v, %v; want nil, nil", out, got, err)
		}
	}
}

func TestDecodeAnswersRefusesMalformed(t *testing.T) {
	for name, tc := range map[string]struct{ out, want string }{
		"bad enum":      {`{"answers":[{"finding":"x#1","answer":"maybe"}]}`, "must be"},
		"no finding":    {`{"answers":[{"answer":"adopted"}]}`, "finding is required"},
		"unknown field": {`{"answers":[{"finding":"x#1","answer":"adopted","why":"y"}]}`, "answers[0]"},
		"not an object": {`{"answers":["x"]}`, "answers[0]"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeAnswers([]byte(tc.out))
			if err == nil || !strings.Contains(err.Error(), tc.want) || got != nil {
				t.Errorf("got %+v, err=%v; want an error containing %q", got, err, tc.want)
			}
		})
	}
}

// changes beside answers is still one report: the answers key must not make
// DecodeChanges skip the object and silently drop the changes.
func TestDecodeChangesBesideAnswers(t *testing.T) {
	out := `{"artifact":{"name":"plan","type":"plan","body":"b"},"changes":[{"op":"add-tags","story":"sty_a","tags":["order:2"]}],"answers":[{"finding":"x#1","answer":"adopted"}]}`
	changes, reported, err := DecodeChanges([]byte(out))
	if err != nil || !reported || len(changes) != 1 || changes[0].Story != "sty_a" {
		t.Fatalf("changes = %+v reported=%v err=%v", changes, reported, err)
	}
	answers, err := DecodeAnswers([]byte(out))
	if err != nil || len(answers) != 1 || answers[0].Finding != "x#1" {
		t.Fatalf("answers = %+v err=%v", answers, err)
	}
}
