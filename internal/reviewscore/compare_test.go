package reviewscore

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestCompareLinesTwoBindingsUpPerRubricAndInTotal(t *testing.T) {
	_, cases := stubCorpus(t)
	good := Score(context.Background(), cases, stub{
		"a-defect-marked":   {Verdict: VerdictReject, Notes: "missing retry", Wall: time.Second},
		"a-defect-unmarked": {Verdict: VerdictReject, Wall: time.Second},
		"a-valid":           {Verdict: VerdictAccept, Wall: time.Second},
		"b-defect-marked":   {Verdict: VerdictReject, Notes: "missing retry", Wall: time.Second},
		"b-valid":           {Verdict: VerdictAccept, Wall: time.Second},
	}, Options{Binding: "good", Adapter: "stubcli"})
	bad := Score(context.Background(), cases, stub{
		"a-defect-marked":   {Verdict: VerdictAccept, Wall: 3 * time.Second},
		"a-defect-unmarked": {Verdict: VerdictReject, Wall: time.Second},
		"a-valid":           {Verdict: VerdictReject, Wall: time.Second},
		"b-defect-marked":   {Verdict: VerdictReject, Notes: "style", Wall: time.Second},
		"b-valid":           {Verdict: VerdictAccept, Wall: time.Second},
	}, Options{Binding: "bad", Adapter: "stubcli"})

	cmp, err := Compare(good, bad)
	if err != nil {
		t.Fatal(err)
	}
	find := func(rubric, metric string) CompareRow {
		for _, r := range cmp.Rows {
			if r.Rubric == rubric && r.Metric == metric {
				return r
			}
		}
		t.Fatalf("no row %s/%s", rubric, metric)
		return CompareRow{}
	}
	if r := find("a", "escapes"); !strings.HasPrefix(r.A, "0/2") || !strings.HasPrefix(r.B, "1/2") || r.Delta != "+1" {
		t.Errorf("a/escapes = %+v", r)
	}
	if r := find("a", "false blockers"); !strings.HasPrefix(r.A, "0/1") || !strings.HasPrefix(r.B, "1/1") || r.Delta != "+1" {
		t.Errorf("a/false blockers = %+v", r)
	}
	if r := find("TOTAL", "recall"); !strings.HasPrefix(r.A, "3/3") || !strings.HasPrefix(r.B, "2/3") || r.Delta != "-33.3pp" {
		t.Errorf("TOTAL/recall = %+v", r)
	}
	if r := find("TOTAL", "wall time"); r.A != "5s" || r.B != "7s" || r.Delta != "2s" {
		t.Errorf("TOTAL/wall = %+v", r)
	}
	var out bytes.Buffer
	cmp.Render(&out)
	for _, want := range []string{"GOOD", "BAD", "TOTAL", "recall", "tokens", "cost", "DELTA"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render missing %q:\n%s", want, out.String())
		}
	}
}

func TestCompareRefusesReportsOverDifferentCaseSets(t *testing.T) {
	_, cases := stubCorpus(t)
	full := Score(context.Background(), cases, stub{}, Options{Binding: "full"})
	partial := Score(context.Background(), cases[:3], stub{}, Options{Binding: "partial"})
	_, err := Compare(full, partial)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	msg := err.Error()
	for _, want := range []string{"not scored over the same case set", "full", "partial", "only in full", "b/b-valid"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
}

func TestCompareRefusesSameIDsWithDifferentContent(t *testing.T) {
	_, cases := stubCorpus(t)
	edited := append([]Case{}, cases...)
	edited[0].Digest = "changed"
	a := Score(context.Background(), cases, stub{}, Options{Binding: "a"})
	b := Score(context.Background(), edited, stub{}, Options{Binding: "b"})
	_, err := Compare(a, b)
	if err == nil || !strings.Contains(err.Error(), "same case ids carry different content") {
		t.Fatalf("err = %v", err)
	}
}

func TestCompareRefusesDifferentRunCounts(t *testing.T) {
	_, cases := stubCorpus(t)
	a := Score(context.Background(), cases, stub{}, Options{Binding: "a", Runs: 1})
	b := Score(context.Background(), cases, stub{}, Options{Binding: "b", Runs: 2})
	if _, err := Compare(a, b); err == nil || !strings.Contains(err.Error(), "run counts") {
		t.Fatalf("err = %v", err)
	}
}
