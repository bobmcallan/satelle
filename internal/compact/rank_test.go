package compact

import (
	"fmt"
	"strings"
	"testing"
)

// ctxLines returns n context lines "<prefix> line %d\n".
func ctxLines(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(" %s line %d\n", prefix, i)
	}
	return out
}

// hunkText builds one hunk's raw text: its "@@ ... @@" header followed by
// body lines (each already carrying its leading ' '/'+'/'-' and "\n").
func hunkText(oldStart, oldCount, newStart, newCount int, body []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
	for _, ln := range body {
		b.WriteString(ln)
	}
	return b.String()
}

// fileSection builds one `diff --git` file section from pre-built hunk texts.
func fileSection(path string, hunks ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", path, path, path, path)
	for _, h := range hunks {
		b.WriteString(h)
	}
	return b.String()
}

func defaultRankConfig() RankConfig {
	return RankConfig{
		PassthroughLines: 50,
		MaxFiles:         20,
		MaxHunksPerFile:  10,
		ContextLines:     2,
	}
}

func TestRankPatchPassthroughUnderThreshold(t *testing.T) {
	store := newMemStore()
	lines := make([]string, 49)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d\n", i)
	}
	patch := strings.Join(lines, "")
	out := RankPatch(patch, defaultRankConfig(), store)
	if out != patch {
		t.Errorf("passthrough under threshold changed the patch")
	}
	if len(store.blobs) != 0 {
		t.Errorf("passthrough must not offload anything, got %d blobs", len(store.blobs))
	}
}

func TestRankPatchPassthroughUnparseable(t *testing.T) {
	store := newMemStore()
	var b strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "not a diff, just noise line %d\n", i)
	}
	patch := b.String()
	out := RankPatch(patch, defaultRankConfig(), store)
	if out != patch {
		t.Errorf("unparseable patch must pass through verbatim")
	}
	if len(store.blobs) != 0 {
		t.Errorf("passthrough must not offload anything, got %d blobs", len(store.blobs))
	}
}

func TestRankPatchFileCapKeepsTopByChangedLines(t *testing.T) {
	store := newMemStore()
	const total = 25
	const keep = 20
	var sections []string
	var b strings.Builder
	for i := 0; i < total; i++ {
		// Distinct, strictly decreasing changed-line counts so the top-keep
		// selection is unambiguous: file 0 has the most changes, file 24 the
		// fewest.
		n := total - i
		var body []string
		for j := 0; j < n; j++ {
			body = append(body, fmt.Sprintf("+added %d\n", j))
		}
		sec := fileSection(fmt.Sprintf("file%02d.go", i), hunkText(1, 1, 1, n+1, body))
		sections = append(sections, sec)
		b.WriteString(sec)
	}
	patch := b.String()

	cfg := defaultRankConfig()
	cfg.MaxFiles = keep
	out := RankPatch(patch, cfg, store)

	for i := 0; i < keep; i++ {
		if !strings.Contains(out, fmt.Sprintf("file%02d.go", i)) {
			t.Errorf("kept file%02d.go missing from output", i)
		}
	}
	dropped := 0
	for i := keep; i < total; i++ {
		name := fmt.Sprintf("file%02d.go", i)
		if strings.Contains(out, "diff --git a/"+name) {
			t.Errorf("dropped %s still has its diff header inline", name)
		}
		if !strings.Contains(out, "dropped file "+name) {
			t.Errorf("missing dropped-file marker for %s", name)
			continue
		}
		dropped++
	}
	if dropped != total-keep {
		t.Errorf("dropped file marker count = %d, want %d", dropped, total-keep)
	}
	// Original order preserved among kept files.
	last := -1
	for i := 0; i < keep; i++ {
		idx := strings.Index(out, fmt.Sprintf("file%02d.go", i))
		if idx < last {
			t.Fatalf("file%02d.go out of original order", i)
		}
		last = idx
	}
	// Every dropped file's marker resolves to its exact original section.
	for i := keep; i < total; i++ {
		hash := markerHashFor(t, out, "dropped file "+fmt.Sprintf("file%02d.go", i))
		got, err := store.Get(hash)
		if err != nil {
			t.Fatalf("resolve dropped file%02d.go: %v", i, err)
		}
		if string(got) != sections[i] {
			t.Errorf("dropped file%02d.go round trip mismatch:\ngot:  %q\nwant: %q", i, got, sections[i])
		}
	}
}

func TestRankPatchHunkCapKeepsFirstAndLast(t *testing.T) {
	store := newMemStore()
	const total = 15
	const keep = 10
	var hunks []string
	var hunkTexts []string
	for i := 0; i < total; i++ {
		body := []string{fmt.Sprintf("+line %d\n", i)}
		h := hunkText(i*10+1, 1, i*10+1, 1, body)
		hunks = append(hunks, h)
		hunkTexts = append(hunkTexts, h)
	}
	patch := fileSection("big.go", hunks...)

	cfg := defaultRankConfig()
	cfg.PassthroughLines = 0
	cfg.MaxHunksPerFile = keep
	out := RankPatch(patch, cfg, store)

	if !strings.Contains(out, "line 0\n") {
		t.Error("first hunk dropped")
	}
	if !strings.Contains(out, "line 14\n") {
		t.Error("last hunk dropped")
	}
	kept := 0
	for i := 0; i < total; i++ {
		if strings.Contains(out, fmt.Sprintf("+line %d\n", i)) {
			kept++
		}
	}
	if kept != keep {
		t.Errorf("kept hunk count = %d, want %d", kept, keep)
	}
	markers := strings.Count(out, "@@ dropped ")
	if markers != 1 {
		t.Fatalf("dropped-hunk marker count = %d, want 1 (equal scores keep a single contiguous run)", markers)
	}
	hash := markerHashFor(t, out, "@@ dropped ")
	got, err := store.Get(hash)
	if err != nil {
		t.Fatalf("resolve dropped hunk run: %v", err)
	}
	wantDropped := strings.Join(hunkTexts[9:14], "")
	if string(got) != wantDropped {
		t.Errorf("dropped hunk run round trip mismatch:\ngot:  %q\nwant: %q", got, wantDropped)
	}
}

func TestRankPatchPriorityPatternBeatsSize(t *testing.T) {
	plainHunk := func(id, n int) string {
		var body []string
		for i := 0; i < n; i++ {
			body = append(body, fmt.Sprintf("+plain%d line %d\n", id, i))
		}
		return hunkText(1, n, 1, n, body)
	}
	todoHunk := hunkText(1, 1, 1, 1, []string{"+ // TODO: fix this\n"})
	first := hunkText(1, 1, 1, 1, []string{"+first\n"})
	last := hunkText(1, 1, 1, 1, []string{"+last\n"})

	patch := fileSection("f.go", first, plainHunk(1, 20), plainHunk(2, 20), plainHunk(3, 20), todoHunk, last)

	cfg := defaultRankConfig()
	cfg.MaxHunksPerFile = 4
	cfg.PriorityPatterns = []string{"(?i)todo"}

	store := newMemStore()
	out := RankPatch(patch, cfg, store)
	if !strings.Contains(out, "TODO: fix this") {
		t.Error("priority-matched hunk should survive despite being far smaller than the plain hunks")
	}
	if !strings.Contains(out, "plain1 line 0") {
		t.Error("earliest-index plain hunk should win the remaining tie-broken slot")
	}
	if strings.Contains(out, "plain2 line 0") || strings.Contains(out, "plain3 line 0") {
		t.Error("later plain hunks should be dropped once the cap is spent")
	}

	// Same fixture with PriorityPatterns=nil: the TODO hunk loses its bonus
	// and is dropped for being small, proving retention follows config.
	cfg.PriorityPatterns = nil
	store2 := newMemStore()
	out2 := RankPatch(patch, cfg, store2)
	if strings.Contains(out2, "TODO: fix this") {
		t.Error("with no priority patterns the small TODO hunk should not survive the cap")
	}
}

func TestScoreHunkWeightsAndCaps(t *testing.T) {
	mk := func(n int, extra ...string) []string {
		h := []string{"@@ -1,1 +1,1 @@\n"}
		for i := 0; i < n; i++ {
			h = append(h, fmt.Sprintf("+line %d\n", i))
		}
		h = append(h, extra...)
		return h
	}
	todoRE := compilePriorityPatterns([]string{"(?i)todo"})

	if got := scoreHunk(mk(5), nil); got != 0.15 {
		t.Errorf("5 changed lines: score = %v, want 0.15", got)
	}
	if got := scoreHunk(mk(20), nil); got != 0.3 {
		t.Errorf("20 changed lines: score = %v, want 0.3 (capped)", got)
	}
	if got := scoreHunk(mk(20, "+// TODO\n"), todoRE); got != 0.6 {
		t.Errorf("20 changed lines + priority match: score = %v, want 0.6", got)
	}
	if got := scoreHunk(mk(1000, "+// TODO\n"), todoRE); got > 1.0 {
		t.Errorf("score = %v, must never exceed 1.0", got)
	}
}

func TestRankPatchContextTrim(t *testing.T) {
	store := newMemStore()
	lead := ctxLines("lead", 5)
	trail := ctxLines("trail", 5)
	var body []string
	body = append(body, lead...)
	body = append(body, "-old line\n", "+new line\n")
	body = append(body, trail...)
	h := hunkText(1, 11, 1, 11, body)
	patch := fileSection("f.go", h)

	cfg := defaultRankConfig()
	cfg.PassthroughLines = 0
	cfg.ContextLines = 2
	out := RankPatch(patch, cfg, store)

	if !strings.Contains(out, "@@ -4,5 +4,5 @@\n") {
		t.Errorf("rewritten header missing or wrong:\n%s", out)
	}
	if strings.Count(out, "lead line") != 2 {
		t.Errorf("leading context not trimmed to 2 lines:\n%s", out)
	}
	if strings.Count(out, "trail line") != 2 {
		t.Errorf("trailing context not trimmed to 2 lines:\n%s", out)
	}
	if !strings.Contains(out, "-old line\n") || !strings.Contains(out, "+new line\n") {
		t.Errorf("change lines lost during context trim:\n%s", out)
	}
	markers := strings.Count(out, "@@ trimmed ")
	if markers != 2 {
		t.Fatalf("trimmed-context marker count = %d, want 2 (one leading, one trailing)", markers)
	}

	// Reassembling every marker's resolved bytes back into place reproduces
	// the exact original hunk body.
	rebuilt := out
	for _, hash := range markerHashesFor(t, out, "@@ trimmed ") {
		got, err := store.Get(hash)
		if err != nil {
			t.Fatalf("resolve trimmed marker: %v", err)
		}
		rebuilt = replaceOneMarkerLine(rebuilt, "@@ trimmed ", string(got))
	}
	wantBody := strings.Join(lead, "") + "-old line\n+new line\n" + strings.Join(trail, "")
	if !strings.Contains(rebuilt, wantBody) {
		t.Errorf("reassembled hunk body mismatch:\ngot:  %q\nwant it to contain: %q", rebuilt, wantBody)
	}
}

// markerHashFor finds the line containing marker in out and extracts its
// <<ccr:HASH,...>> hash. Fails the test if none is found.
func markerHashFor(t *testing.T, out, marker string) string {
	t.Helper()
	hashes := markerHashesFor(t, out, marker)
	if len(hashes) == 0 {
		t.Fatalf("no marker line containing %q found in:\n%s", marker, out)
	}
	return hashes[0]
}

func markerHashesFor(t *testing.T, out, marker string) []string {
	t.Helper()
	var hashes []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		start := strings.Index(line, "<<ccr:")
		if start < 0 {
			continue
		}
		end := strings.Index(line[start:], ">>")
		if end < 0 {
			continue
		}
		field := line[start+len("<<ccr:") : start+end]
		parts := strings.SplitN(field, ",", 2)
		hashes = append(hashes, parts[0])
	}
	return hashes
}

// replaceOneMarkerLine replaces the first line containing marker with
// replacement text (which may itself span multiple lines with trailing
// newlines already included).
func replaceOneMarkerLine(s, marker, replacement string) string {
	lines := strings.SplitAfter(s, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, marker) {
			lines[i] = replacement
			return strings.Join(lines, "")
		}
	}
	return s
}
