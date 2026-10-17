package agentartifact

import (
	"strings"
	"testing"
)

// sty_af0b1761: the changes a performer reports ride beside its verdict or
// artifact and are decoded once, the same for every adapter that hands its final
// text to this package.

func TestDecodeChangesBesideEachResponseShape(t *testing.T) {
	const list = `"changes":[{"op":"add-tags","story":"sty_a","tags":["order:2"]},{"op":"create-child","title":"T","body":"B","acceptance":"1. x","category":"feature","tags":["surface:cli"]}]`
	for name, out := range map[string]string{
		"alone":          `{` + list + `}`,
		"with a verdict": `{"decision":"accept","notes":"ok",` + list + `}`,
		"with a reject":  `{"decision":"reject","notes":"no",` + list + `}`,
		"with artifact":  `{"artifact":{"name":"plan","type":"plan","body":"b"},` + list + `}`,
		"after prose":    "I repaired the tags.\n```json\n{" + list + "}\n```\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, reported, err := DecodeChanges([]byte(out))
			if err != nil || !reported {
				t.Fatalf("reported=%v err=%v", reported, err)
			}
			if len(got) != 2 || got[0].Op != "add-tags" || got[0].Story != "sty_a" || got[0].Tags[0] != "order:2" {
				t.Errorf("first change = %+v", got)
			}
			if got[1].Op != "create-child" || got[1].Title != "T" || got[1].Acceptance != "1. x" || got[1].Category != "feature" || got[1].Tags[0] != "surface:cli" {
				t.Errorf("second change = %+v", got[1])
			}
		})
	}
}

func TestDecodeChangesLastReportWins(t *testing.T) {
	out := `{"changes":[{"op":"add-tags","story":"sty_first","tags":["order:1"]}]} then {"changes":[{"op":"add-tags","story":"sty_last","tags":["order:2"]}]}`
	got, _, err := DecodeChanges([]byte(out))
	if err != nil || len(got) != 1 || got[0].Story != "sty_last" {
		t.Fatalf("want the last report, got %+v err=%v", got, err)
	}
}

func TestDecodeChangesAbsent(t *testing.T) {
	for name, out := range map[string]string{
		"no json":              "all good",
		"a verdict only":       `{"decision":"accept","notes":""}`,
		"other json":           `{"changes":[{"op":"x"}],"version":"1.0.0"}`, // quoted JSON with other keys is not a report
		"changes not a list":   `{"changes":"none"}`,
		"a nested lookalike":   `{"decision":"accept","notes":"{\"changes\":[1]}"}`,
		"artifact only":        `{"artifact":{"name":"plan","type":"plan","body":"b"}}`,
		"unrelated changelist": `{"changelog":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, reported, err := DecodeChanges([]byte(out))
			if err != nil || reported || len(got) != 0 {
				t.Errorf("want no report, got %+v reported=%v err=%v", got, reported, err)
			}
		})
	}
}

func TestDecodeChangesEmptyListIsAReportOfNothing(t *testing.T) {
	got, reported, err := DecodeChanges([]byte(`{"changes":[]}`))
	if err != nil || !reported || len(got) != 0 {
		t.Fatalf("got %+v reported=%v err=%v", got, reported, err)
	}
}

func TestDecodeChangesRefusesMalformedReports(t *testing.T) {
	for name, tc := range map[string]struct{ out, want string }{
		"unknown field":      {`{"changes":[{"op":"add-tags","story":"s","tags":["a:b"],"priority":"high"}]}`, `changes[0]`},
		"not an object":      {`{"changes":["add-tags"]}`, `changes[0]`},
		"second is bad":      {`{"changes":[{"op":"add-tags"},{"op":"x","nope":1}]}`, `changes[1]`},
		"tags not a list":    {`{"changes":[{"op":"add-tags","tags":"order:1"}]}`, `changes[0]`},
		"title not a string": {`{"changes":[{"op":"create-child","title":3}]}`, `changes[0]`},
	} {
		t.Run(name, func(t *testing.T) {
			_, reported, err := DecodeChanges([]byte(tc.out))
			if err == nil || !reported || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error naming %q, got reported=%v err=%v", tc.want, reported, err)
			}
		})
	}
}

// The report is decoded from the final text alone, so a transport envelope that
// the adapter has already unwrapped adds nothing: the same bytes decode the same.
func TestDecodeChangesIsIndependentOfTheArtifactDecode(t *testing.T) {
	out := []byte(`{"artifact":{"name":"plan","type":"plan","body":"b"},"changes":[{"op":"add-tags","story":"s","tags":["lane:trunk"]}]}`)
	a, aerr := Decode(out)
	c, reported, cerr := DecodeChanges(out)
	if aerr != nil || cerr != nil || a.Body != "b" || !reported || len(c) != 1 {
		t.Fatalf("artifact %+v err=%v; changes %+v reported=%v err=%v", a, aerr, c, reported, cerr)
	}
}
