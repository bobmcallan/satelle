package compact

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

// RankConfig configures RankPatch's deterministic ranking of which files and
// hunks a large patch keeps inline. Every field is authored configuration
// (internal/config.DiffRankConfig) — this package carries no default of its
// own. MaxFiles<=0 or MaxHunksPerFile<=0 disables ranking entirely (RankPatch
// returns the patch unchanged), the same zero-means-off stance NoisePatterns
// takes in CompactPatch.
type RankConfig struct {
	// PassthroughLines: a patch with this many lines or fewer (or that fails
	// to parse as a unified diff) returns verbatim — ranking never touches it.
	PassthroughLines int
	// MaxFiles is the largest number of file sections kept; the rest are
	// dropped behind one marker per file, ranked by total changed lines.
	MaxFiles int
	// MaxHunksPerFile is the largest number of hunks kept per surviving file:
	// always the first and last hunk, then the highest-scored remainder.
	MaxHunksPerFile int
	// ContextLines is how many unchanged lines RankPatch keeps on each side
	// of a change within a kept hunk; a longer run is trimmed to this width
	// and replaced by a marker for the trimmed middle. <=0 disables trimming.
	ContextLines int
	// PriorityPatterns are regexes; a changed line matching any of them adds
	// hunkScorePriorityBonus to that hunk's retention score. An invalid
	// pattern is skipped (internal/config validates at load time).
	PriorityPatterns []string
}

// Hunk retention scoring weights (sty_918e2086): mechanism constants, not
// authored configuration — the ACs task only the thresholds/caps/patterns
// above with being configurable, not this formula's shape.
const (
	hunkScorePerLine       = 0.03
	hunkScoreLineCap       = 0.3
	hunkScorePriorityBonus = 0.3
	hunkScoreCap           = 1.0
)

// RankPatch reduces patch to RankConfig's caps, offloading every dropped file
// section, dropped hunk run, and trimmed context run to off behind a
// retrieve marker so nothing it removes is unrecoverable. off nil, or a
// disabled cfg (MaxFiles<=0 || MaxHunksPerFile<=0), returns patch unchanged —
// ranking never runs without somewhere to put what it drops.
func RankPatch(patch string, cfg RankConfig, off Offloader) string {
	if off == nil || cfg.MaxFiles <= 0 || cfg.MaxHunksPerFile <= 0 {
		return patch
	}
	if len(splitKeepEnds(patch)) < cfg.PassthroughLines {
		return patch
	}
	sections := splitFileSections(patch)
	if len(sections) == 0 {
		return patch
	}
	if len(sections) == 1 && !strings.HasPrefix(sections[0], "diff --git ") {
		return patch // unparseable — pass through rather than guess
	}

	priorityRE := compilePriorityPatterns(cfg.PriorityPatterns)
	files := make([]*rankFile, 0, len(sections))
	for idx, sec := range sections {
		head, hunkLines := splitHeadHunks(sec)
		hunks := splitHunks(hunkLines)
		f := &rankFile{idx: idx, path: parseDiffGitPath(head), head: head, hunks: hunks, text: sec}
		for _, h := range hunks {
			f.changed += changedCount(h)
		}
		files = append(files, f)
	}

	keptFiles := selectFiles(files, cfg.MaxFiles)
	var out strings.Builder
	for _, f := range files {
		if !keptFiles[f.idx] {
			if marker, ok := fileDropMarker(f.path, f.text, f.changed, off); ok {
				out.WriteString(marker)
				continue
			}
			out.WriteString(f.text)
			continue
		}
		out.WriteString(renderRankedFile(f, cfg, priorityRE, off))
	}
	return out.String()
}

// rankFile is one patch file section under consideration.
type rankFile struct {
	idx     int
	path    string
	head    []string
	hunks   [][]string
	text    string
	changed int
}

// splitHeadHunks splits a file section (as produced by splitFileSections)
// into its head lines (diff --git/index/---/+++) and the hunk lines that
// follow the first "@@ " line.
func splitHeadHunks(sec string) (head []string, hunkLines []string) {
	lines := splitKeepEnds(sec)
	i := 0
	for i < len(lines) && !strings.HasPrefix(lines[i], "@@ ") {
		head = append(head, lines[i])
		i++
	}
	return head, lines[i:]
}

// changedCount counts +/- lines in a hunk (h[0] is its "@@ " header).
func changedCount(h []string) int {
	n := 0
	for _, ln := range h[1:] {
		if strings.HasPrefix(ln, "+") || strings.HasPrefix(ln, "-") {
			n++
		}
	}
	return n
}

// selectFiles returns the set of kept file indices: the MaxFiles files with
// the most changed lines, ties broken by original order (stable sort over
// files already in original order).
func selectFiles(files []*rankFile, max int) map[int]bool {
	kept := make(map[int]bool, len(files))
	if len(files) <= max {
		for _, f := range files {
			kept[f.idx] = true
		}
		return kept
	}
	order := append([]*rankFile(nil), files...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].changed > order[j].changed })
	for _, f := range order[:max] {
		kept[f.idx] = true
	}
	return kept
}

// renderRankedFile emits a kept file's head plus its ranked, context-trimmed
// hunks, with any dropped hunk run replaced by one marker.
func renderRankedFile(f *rankFile, cfg RankConfig, priorityRE []*regexp.Regexp, off Offloader) string {
	var out strings.Builder
	for _, h := range f.head {
		out.WriteString(h)
	}
	keptHunks := selectHunks(f.hunks, cfg.MaxHunksPerFile, priorityRE)
	i := 0
	for i < len(f.hunks) {
		if keptHunks[i] {
			out.WriteString(strings.Join(trimContext(f.hunks[i], cfg.ContextLines, off), ""))
			i++
			continue
		}
		j := i
		var buf strings.Builder
		lines := 0
		for j < len(f.hunks) && !keptHunks[j] {
			body := strings.Join(f.hunks[j], "")
			buf.WriteString(body)
			lines += strings.Count(body, "\n")
			j++
		}
		if marker, ok := hunkDropMarker(buf.String(), j-i, lines, off); ok {
			out.WriteString(marker)
		} else {
			out.WriteString(buf.String())
		}
		i = j
	}
	return out.String()
}

// selectHunks returns the set of kept hunk indices: the first, the last (when
// distinct), and the highest-scored remainder up to max, ties broken by
// position.
func selectHunks(hunks [][]string, max int, priorityRE []*regexp.Regexp) map[int]bool {
	n := len(hunks)
	kept := make(map[int]bool, n)
	if n <= max {
		for i := range hunks {
			kept[i] = true
		}
		return kept
	}
	kept[0] = true
	if n > 1 {
		kept[n-1] = true
	}
	remaining := max - len(kept)
	if remaining > 0 {
		type scored struct {
			idx   int
			score float64
		}
		candidates := make([]scored, 0, n-len(kept))
		for i := 1; i < n-1; i++ {
			candidates = append(candidates, scored{i, scoreHunk(hunks[i], priorityRE)})
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}
			return candidates[i].idx < candidates[j].idx
		})
		for _, c := range candidates {
			if remaining == 0 {
				break
			}
			kept[c.idx] = true
			remaining--
		}
	}
	return kept
}

// scoreHunk is 0.03 per changed line (capped at 0.3), +0.3 when a changed
// line matches a priority pattern, the total capped at 1.0.
func scoreHunk(h []string, priorityRE []*regexp.Regexp) float64 {
	score := float64(changedCount(h)) * hunkScorePerLine
	if score > hunkScoreLineCap {
		score = hunkScoreLineCap
	}
	if hunkMatchesPriority(h, priorityRE) {
		score += hunkScorePriorityBonus
	}
	if score > hunkScoreCap {
		score = hunkScoreCap
	}
	return score
}

func hunkMatchesPriority(h []string, priorityRE []*regexp.Regexp) bool {
	for _, ln := range h[1:] {
		if !strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "-") {
			continue
		}
		for _, re := range priorityRE {
			if re.MatchString(ln) {
				return true
			}
		}
	}
	return false
}

func compilePriorityPatterns(pats []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if re, err := regexp.Compile(p); err == nil {
			out = append(out, re)
		}
	}
	return out
}

// fileDropMarker offloads a whole dropped file section, returning the single
// line that replaces it.
func fileDropMarker(path, body string, changed int, off Offloader) (string, bool) {
	if body == "" {
		return "", false
	}
	hash, err := off.Put([]byte(body))
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("@@ dropped file %s %s (%d changed lines) @@\n",
		path, retrieve.MarkerKind(hash, "file", len(body)), changed), true
}

// hunkDropMarker offloads a contiguous run of count dropped hunks (lines is
// the run's total line count), returning the single line that replaces them.
func hunkDropMarker(body string, count, lines int, off Offloader) (string, bool) {
	if body == "" {
		return "", false
	}
	hash, err := off.Put([]byte(body))
	if err != nil {
		return "", false
	}
	word := "hunk"
	if count != 1 {
		word = "hunks"
	}
	return fmt.Sprintf("@@ dropped %d %s %s (%d lines) @@\n",
		count, word, retrieve.MarkerKind(hash, "hunk", len(body)), lines), true
}

// ctxTrimMarker offloads a run of trimmed context lines, returning the single
// line that replaces them.
func ctxTrimMarker(body string, lines int, off Offloader) (string, bool) {
	if body == "" {
		return "", false
	}
	hash, err := off.Put([]byte(body))
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("@@ trimmed %s (%d lines) @@\n", retrieve.MarkerKind(hash, "ctx", len(body)), lines), true
}

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)

type hunkHeader struct {
	oldStart, oldCount, newStart, newCount int
	suffix                                 string
}

func parseHunkHeader(line string) (hunkHeader, bool) {
	m := hunkHeaderRE.FindStringSubmatch(line)
	if m == nil {
		return hunkHeader{}, false
	}
	oldStart, _ := strconv.Atoi(m[1])
	oldCount := 1
	if m[2] != "" {
		oldCount, _ = strconv.Atoi(m[2])
	}
	newStart, _ := strconv.Atoi(m[3])
	newCount := 1
	if m[4] != "" {
		newCount, _ = strconv.Atoi(m[4])
	}
	return hunkHeader{oldStart, oldCount, newStart, newCount, m[5]}, true
}

func formatHunkHeader(h hunkHeader, ending string) string {
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@%s%s", h.oldStart, h.oldCount, h.newStart, h.newCount, h.suffix, ending)
}

// splitLineEnding separates a line's trailing "\r\n"/"\n" from its body.
func splitLineEnding(line string) (body, ending string) {
	if strings.HasSuffix(line, "\r\n") {
		return line[:len(line)-2], "\r\n"
	}
	if strings.HasSuffix(line, "\n") {
		return line[:len(line)-1], "\n"
	}
	return line, ""
}

// hunkSegment is one maximal run of context lines, or one maximal run of
// non-context (change/no-newline-marker) lines, in a hunk body's order.
type hunkSegment struct {
	isContext bool
	lines     []string
}

func segmentHunkBody(body []string) []hunkSegment {
	var segs []hunkSegment
	for _, ln := range body {
		isCtx := strings.HasPrefix(ln, " ")
		if len(segs) > 0 && segs[len(segs)-1].isContext == isCtx {
			segs[len(segs)-1].lines = append(segs[len(segs)-1].lines, ln)
			continue
		}
		segs = append(segs, hunkSegment{isContext: isCtx, lines: []string{ln}})
	}
	return segs
}

// trimContext collapses a kept hunk's context runs to contextLines on each
// side of a change (interior runs keep contextLines at both ends and trim the
// middle; the leading run keeps only its last contextLines lines; the
// trailing run keeps only its first), rewriting the hunk header's line counts
// to match. Every trimmed run is offloaded behind a marker inserted in its
// place. contextLines<=0, off nil, an unparseable header, or a hunk with no
// changes at all (nothing to trim around) all return h unchanged.
func trimContext(h []string, contextLines int, off Offloader) []string {
	if contextLines <= 0 || off == nil || len(h) < 2 {
		return h
	}
	headerBody, ending := splitLineEnding(h[0])
	hf, ok := parseHunkHeader(headerBody)
	if !ok {
		return h
	}
	segs := segmentHunkBody(h[1:])
	hasChange := false
	for _, s := range segs {
		if !s.isContext {
			hasChange = true
			break
		}
	}
	if !hasChange {
		return h
	}

	leadTrim, otherTrim := 0, 0
	out := make([]string, 0, len(h))
	out = append(out, "") // header placeholder, filled in below
	for idx, seg := range segs {
		if !seg.isContext {
			out = append(out, seg.lines...)
			continue
		}
		n := len(seg.lines)
		isLeading := idx == 0
		isTrailing := idx == len(segs)-1
		switch {
		case isLeading && !isTrailing && n > contextLines:
			trimmed, kept := seg.lines[:n-contextLines], seg.lines[n-contextLines:]
			if marker, ok := ctxTrimMarker(strings.Join(trimmed, ""), len(trimmed), off); ok {
				out = append(out, marker)
				leadTrim += len(trimmed)
				out = append(out, kept...)
			} else {
				out = append(out, seg.lines...)
			}
		case isTrailing && !isLeading && n > contextLines:
			kept, trimmed := seg.lines[:contextLines], seg.lines[contextLines:]
			out = append(out, kept...)
			if marker, ok := ctxTrimMarker(strings.Join(trimmed, ""), len(trimmed), off); ok {
				out = append(out, marker)
				otherTrim += len(trimmed)
			} else {
				out = append(out, trimmed...)
			}
		case !isLeading && !isTrailing && n > 2*contextLines:
			head, mid, tail := seg.lines[:contextLines], seg.lines[contextLines:n-contextLines], seg.lines[n-contextLines:]
			out = append(out, head...)
			if marker, ok := ctxTrimMarker(strings.Join(mid, ""), len(mid), off); ok {
				out = append(out, marker)
				otherTrim += len(mid)
				out = append(out, tail...)
			} else {
				out = append(out, mid...)
				out = append(out, tail...)
			}
		default:
			out = append(out, seg.lines...)
		}
	}

	if leadTrim == 0 && otherTrim == 0 {
		return h
	}
	newHF := hunkHeader{
		oldStart: hf.oldStart + leadTrim,
		oldCount: hf.oldCount - leadTrim - otherTrim,
		newStart: hf.newStart + leadTrim,
		newCount: hf.newCount - leadTrim - otherTrim,
		suffix:   hf.suffix,
	}
	out[0] = formatHunkHeader(newHF, ending)
	return out
}
