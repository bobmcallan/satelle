package agentstep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// citingRubric is a reviewer rubric that opts into plan citation excerpts.
const citingRubric = "---\nname: satelle-story-done-review\ntype: skill\ndescription: test rubric\npayload_citations: plan\n---\n\n" +
	"Judge the plan.\n\nReturn JSON {\"decision\": \"accept\"|\"reject\", \"notes\": \"…\"}.\n"

// citedTree is a judged tree with a/b.go of twenty numbered lines.
func citedTree(t *testing.T) string {
	t.Helper()
	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(tree, "a", "b.go"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return tree
}

// citationGate runs a gate over a plan with the given rubric and tree resolver
// and returns the payload and the citations material it was handed (nil when the
// payload references none).
func citationGate(t *testing.T, rubric, plan string, tree func(context.Context, string) (string, error)) (string, *CitationsState) {
	t.Helper()
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: rubric, skillFound: true})
	g.SetDocsResolver(func(context.Context, string) []DocState {
		return []DocState{{Name: "plan", Type: "plan", Body: plan}}
	})
	if tree != nil {
		g.SetCitationTreeResolver(tree)
	}
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_cite", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.got.Payload, `"citations"`) {
		return r.got.Payload, nil
	}
	var st CitationsState
	if err := json.Unmarshal(openedAt(t, r.opened, r.got.Payload, "citations"), &st); err != nil {
		t.Fatal(err)
	}
	return r.got.Payload, &st
}

func fixedTree(dir string) func(context.Context, string) (string, error) {
	return func(context.Context, string) (string, error) { return dir, nil }
}

func lines(from, to int) string {
	var sb strings.Builder
	for n := from; n <= to; n++ {
		fmt.Fprintf(&sb, "%d: line %d\n", n, n)
	}
	return sb.String()
}

func TestParseCitations(t *testing.T) {
	plan := "Owners: `internal/a/b.go:12`, (c.go:3-9) and internal/a/b.go:12 again.\n" +
		"See https://example.com/x.go:80 and localhost:8787, step:2, at 12:30, v1.22:4.\n" +
		"| engine.go:1098 | `x/y.go:5-3` |\nline-end ref internal/z.go:7.\n"
	got := parseCitations(plan)
	var keys []string
	for _, c := range got {
		keys = append(keys, fmt.Sprintf("%s:%d-%d", c.Path, c.Start, c.End))
	}
	want := []string{"internal/a/b.go:12-12", "c.go:3-9", "engine.go:1098-1098", "x/y.go:5-3", "internal/z.go:7-7"}
	if strings.Join(keys, " ") != strings.Join(want, " ") {
		t.Errorf("parseCitations = %v, want %v", keys, want)
	}
}

// AC1: path:line and path:start-end citations in the plan reach the reviewer as
// the lines they name plus three of context, read from the judged tree.
func TestPlanReviewPayloadCarriesCitationExcerpts(t *testing.T) {
	tree := citedTree(t)
	payload, st := citationGate(t, citingRubric, "Premise `a/b.go:5` and a/b.go:8-10 hold.", fixedTree(tree))
	if st == nil {
		t.Fatalf("payload has no citations block:\n%s", payload)
	}
	if len(st.Citations) != 2 {
		t.Fatalf("citations = %+v, want 2", st.Citations)
	}
	first, second := st.Citations[0], st.Citations[1]
	if first.Excerpt != lines(2, 8) || first.ContextStart != 2 || first.ContextEnd != 8 || first.Start != 5 {
		t.Errorf("a/b.go:5 = %+v, want numbered lines 2-8", first)
	}
	if second.Excerpt != lines(5, 13) || second.Start != 8 || second.End != 10 {
		t.Errorf("a/b.go:8-10 = %+v, want numbered lines 5-13", second)
	}
	if st.Truncated || st.Omitted != 0 {
		t.Errorf("two small excerpts must not be marked truncated: %+v", st)
	}
	if strings.Contains(payload, "line 5") {
		t.Error("excerpts must ride the referenced material, not the inline payload")
	}
}

// A span cut at the window start/end clamps to the file, and a long span is cut
// at the span ceiling and flagged.
func TestCitationExcerptClampsToFileAndCapsSpan(t *testing.T) {
	tree := t.TempDir()
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(tree, "big.go"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	_, st := citationGate(t, citingRubric, "big.go:1 big.go:99-200 big.go:10-90", fixedTree(tree))
	if st == nil || len(st.Citations) != 3 {
		t.Fatalf("citations = %+v", st)
	}
	if c := st.Citations[0]; c.ContextStart != 1 || c.ContextEnd != 4 {
		t.Errorf("big.go:1 context = %d-%d, want 1-4", c.ContextStart, c.ContextEnd)
	}
	if c := st.Citations[1]; c.ContextEnd != 100 || c.Truncated {
		t.Errorf("big.go:99-200 = %+v, want clamped to the file's end, not span-truncated", c)
	}
	if c := st.Citations[2]; !c.Truncated || c.ContextEnd != 10+citationSpanCeiling-1+citationContext {
		t.Errorf("big.go:10-90 = %+v, want cut at %d lines", c, citationSpanCeiling)
	}
}

// AC2: a citation that no longer resolves is listed with the reason, in plan
// order, and every parsed citation has an entry.
func TestUnresolvedCitationIsListedWithReason(t *testing.T) {
	tree := citedTree(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(tree, "link")); err != nil {
		t.Fatal(err)
	}
	plan := "gone.go:3 a/b.go:99 ../x.go:1 a/b.go:9-4 link/f.go:1 /etc/passwd:1 a/b.go:2"
	_, st := citationGate(t, citingRubric, plan, fixedTree(tree))
	if st == nil {
		t.Fatal("no citations block")
	}
	want := []struct{ path, reason string }{
		{"gone.go", "file not found"},
		{"a/b.go", "line 99 beyond end of file (20 lines)"},
		{"../x.go", "outside the judged tree"},
		{"a/b.go", "invalid span"},
		{"link/f.go", "file not found"},
		{"/etc/passwd", "outside the judged tree"},
	}
	if len(st.Citations) != len(want)+1 {
		t.Fatalf("entries = %d, want %d (one per parsed citation): %+v", len(st.Citations), len(want)+1, st.Citations)
	}
	for i, w := range want {
		c := st.Citations[i]
		if c.Path != w.path || !c.Unresolved || c.Reason != w.reason || c.Excerpt != "" {
			t.Errorf("entry %d = %+v, want unresolved %q: %q", i, c, w.path, w.reason)
		}
	}
	if last := st.Citations[len(want)]; last.Unresolved || last.Excerpt == "" {
		t.Errorf("the resolvable citation after them must still be excerpted: %+v", last)
	}
}

// AC2: a symlink that leaves the tree is outside it even though the file exists.
func TestCitationThroughSymlinkOutOfTreeIsUnresolved(t *testing.T) {
	tree, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "f.go"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tree, "link")); err != nil {
		t.Fatal(err)
	}
	_, st := citationGate(t, citingRubric, "link/f.go:1", fixedTree(tree))
	if st == nil || len(st.Citations) != 1 || st.Citations[0].Reason != "outside the judged tree" || st.Citations[0].Excerpt != "" {
		t.Errorf("citations = %+v, want one unresolved outside the judged tree", st)
	}
}

// AC2: a tree that cannot be resolved lists every citation with that reason,
// never drops them and never fails the gate; so does an unwired resolver.
func TestCitationsWithoutAJudgedTreeAreListedUnresolved(t *testing.T) {
	failing := func(context.Context, string) (string, error) { return "", errors.New("no cwd") }
	for name, tree := range map[string]func(context.Context, string) (string, error){"error": failing, "unwired": nil} {
		t.Run(name, func(t *testing.T) {
			_, st := citationGate(t, citingRubric, "a/b.go:5 c.go:1-2", tree)
			if st == nil || len(st.Citations) != 2 {
				t.Fatalf("citations = %+v, want both listed", st)
			}
			for _, c := range st.Citations {
				if !c.Unresolved || !strings.HasPrefix(c.Reason, "judged tree unavailable: ") {
					t.Errorf("%+v, want unresolved with the tree reason", c)
				}
			}
		})
	}
	_, st := citationGate(t, citingRubric, "a/b.go:5", failing)
	if !strings.Contains(st.Citations[0].Reason, "no cwd") {
		t.Errorf("reason must carry the resolver's error: %+v", st.Citations[0])
	}
}

// AC3: the excerpts are bounded by count and by bytes, every citation past a
// bound stays listed as omitted, and the block says it was truncated.
func TestCitationsStayWithinCeilingAndMarkTruncation(t *testing.T) {
	tree := t.TempDir()
	var sb strings.Builder
	for i := 1; i <= 6000; i++ {
		fmt.Fprintf(&sb, "%s %d\n", strings.Repeat("x", 90), i)
	}
	if err := os.WriteFile(filepath.Join(tree, "big.go"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(i int) string{
		"bytes": func(i int) string { return fmt.Sprintf("big.go:%d-%d", i*40+1, i*40+40) },
		"count": func(i int) string { return fmt.Sprintf("big.go:%d", i*10+1) },
	}
	for name, cite := range cases {
		t.Run(name, func(t *testing.T) {
			var plan []string
			for i := 0; i < 100; i++ {
				plan = append(plan, cite(i))
			}
			_, st := citationGate(t, citingRubric, strings.Join(plan, " "), fixedTree(tree))
			if st == nil || len(st.Citations) != 100 {
				t.Fatalf("every citation must stay listed: %+v", st)
			}
			var bytes, excerpts, omitted int
			for _, c := range st.Citations {
				bytes += len(c.Excerpt)
				switch {
				case c.Omitted:
					omitted++
					if c.Excerpt != "" || c.Path != "big.go" || c.Start == 0 {
						t.Errorf("an omitted entry keeps its citation and no excerpt: %+v", c)
					}
				case c.Excerpt != "":
					excerpts++
				}
			}
			if bytes > CitationsPayloadCeiling || excerpts > citationCount {
				t.Errorf("%d excerpts / %d bytes exceed the bounds (%d / %d)", excerpts, bytes, citationCount, CitationsPayloadCeiling)
			}
			if omitted == 0 || !st.Truncated || st.Omitted != omitted || excerpts+omitted != 100 {
				t.Errorf("truncated=%v omitted=%d (counted %d) excerpts=%d", st.Truncated, st.Omitted, omitted, excerpts)
			}
		})
	}
}

// AC3: a plan citing more than the list bound still lists a bounded block and
// counts the rest as omitted.
func TestCitationListIsBounded(t *testing.T) {
	var plan []string
	for i := 1; i <= citationListCeiling+25; i++ {
		plan = append(plan, fmt.Sprintf("gone.go:%d", i))
	}
	_, st := citationGate(t, citingRubric, strings.Join(plan, " "), fixedTree(t.TempDir()))
	if st == nil || len(st.Citations) != citationListCeiling || st.Omitted != 25 || !st.Truncated {
		t.Errorf("listed %d, omitted %d, truncated %v", len(st.Citations), st.Omitted, st.Truncated)
	}
}

// AC4: a gate whose rubric does not opt in receives no excerpts and never even
// resolves a tree — its payload is the one it had before this block existed.
func TestGateWithoutCitationKeyGetsNoCitations(t *testing.T) {
	tree := citedTree(t)
	called := false
	resolver := func(context.Context, string) (string, error) { called = true; return tree, nil }
	plan := "Premise a/b.go:5 holds."

	payload, st := citationGate(t, "ordinary rubric", plan, resolver)
	if st != nil || strings.Contains(payload, "citations") {
		t.Errorf("a rubric without payload_citations must get no citations:\n%s", payload)
	}
	if called {
		t.Error("the tree resolver must not run for a gate that did not opt in")
	}
	base, _ := citationGate(t, "ordinary rubric", plan, nil)
	if stripScratch(base) != stripScratch(payload) {
		t.Errorf("payload differs once a resolver is wired:\n%s\n%s", base, payload)
	}

	// An opted-in rubric whose named document type is absent gets none either.
	other := strings.Replace(citingRubric, "payload_citations: plan", "payload_citations: design", 1)
	if payload, st = citationGate(t, other, plan, resolver); st != nil {
		t.Errorf("no document of the named type, yet citations were attached:\n%s", payload)
	}
}

var scratchPath = regexp.MustCompile(`"path":"[^"]*"`)

// stripScratch blanks the per-dispatch scratch paths so two payloads compare.
func stripScratch(p string) string { return scratchPath.ReplaceAllString(p, `"path":""`) }

// A bundled session carries the citations once, whichever rubric opted in.
func TestBundledSessionTakesCitationsFromTheDeclaringRubric(t *testing.T) {
	st := &CitationsState{Citations: []Citation{{Path: "a/b.go", Start: 1, End: 1, Excerpt: "1: x\n"}}}
	preps := []reviewerPrep{
		{},
		{tp: transitionPayload{Citations: st}, material: reviewerMaterial{Citations: st}},
	}
	if tp, mat := bundleCitations(preps); tp != st || mat != st {
		t.Error("the bundle must take the citations of the rubric that declared them")
	}
	if tp, mat := bundleCitations(preps[:1]); tp != nil || mat != nil {
		t.Error("no declaring rubric, no citations")
	}
}
