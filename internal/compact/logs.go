package compact

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

// LogConfig configures CompressLog's deterministic reduction of a functional
// check's captured output into failing-check reject notes (sty_ef930f81).
// Every pattern and width below is authored configuration
// (internal/config.CheckLogConfig) — this package carries no language-specific
// pattern of its own. A zero-value LogConfig (no patterns, sizes unset) still
// produces bounded notes: PassthroughLines/HeadLines/TailLines fall back to
// small mechanism-level sizes (see the default* constants) when <= 0, but no
// pattern ever defaults to a non-empty value.
type LogConfig struct {
	// PassthroughLines: a log with this many lines or fewer returns verbatim —
	// compression never touches it. <=0 means DefaultPassthroughLines.
	PassthroughLines int
	// ContextLines is how many lines of context are kept on each side of a
	// kept line. <=0 disables context expansion.
	ContextLines int
	// HeadLines/TailLines bound the fallback used when no pattern is
	// configured, or none matched this log: the first HeadLines and last
	// TailLines lines. <=0 means DefaultHeadLines/DefaultTailLines.
	HeadLines int
	TailLines int
	// MaxKeptLines caps the notes' total real-content line count (kept lines
	// plus their expanded context; omission/collapse markers don't count).
	// The lowest-priority context — farthest from its kept anchor — is
	// dropped first. Lines that matched a keep/trace/warn rule are never
	// dropped. <=0 means no cap.
	MaxKeptLines int
	// KeepPatterns are regexes; a line matching any of them is always kept.
	// An invalid pattern is skipped (internal/config validates at load time).
	KeepPatterns []string
	// WarnPatterns are regexes; a line matching any of them is deduplicated —
	// only its first occurrence survives, annotated "(xN)" when it repeats.
	WarnPatterns []string
	// TraceStart opens a stack-trace block (e.g. a goroutine dump header);
	// a matching line is always kept.
	TraceStart string
	// TraceFrame matches a line that continues an open trace block.
	TraceFrame string
	// TraceAppFrame additionally matches a frame that is always kept even
	// past TraceKeepFrames (e.g. the target application's own module path).
	TraceAppFrame string
	// TraceKeepFrames is how many leading frames of a trace block are kept in
	// full; later frames collapse to "[... N frames collapsed]" unless they
	// match TraceAppFrame. A "frame" here is counted as one matched LINE, not
	// a function/file line pair — a Go trace's two-line frames therefore
	// collapse at roughly 2x this count; callers configuring TraceFrame to
	// match only one of the pair (as this repo does) get a literal count.
	TraceKeepFrames int
}

// Mechanism-level fallback sizes CompressLog applies when the corresponding
// LogConfig field is unset (<=0). These are sizes, not language patterns —
// they keep an unconfigured repo's notes bounded rather than making
// compression a no-op.
const (
	DefaultPassthroughLines = 40
	DefaultHeadLines        = 20
	DefaultTailLines        = 20
)

// CompressLog reduces log to LogConfig's bounds for use as a functional
// check's failing-notes body, offloading the full raw log to off behind a
// retrieval marker so nothing it drops is unrecoverable. off nil or a failed
// Put degrades the footer to omission counts with no marker, but never fails.
func CompressLog(log string, cfg LogConfig, off Offloader) string {
	passthrough := cfg.PassthroughLines
	if passthrough <= 0 {
		passthrough = DefaultPassthroughLines
	}
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	if len(lines) <= passthrough {
		return log
	}

	warnRE := compileLogPatterns(cfg.WarnPatterns)
	deduped, dupCount := dedupWarnLines(lines, warnRE)

	keepRE := compileLogPatterns(cfg.KeepPatterns)
	var traceStartRE, traceFrameRE, traceAppRE *regexp.Regexp
	if strings.TrimSpace(cfg.TraceStart) != "" {
		traceStartRE, _ = regexp.Compile(cfg.TraceStart)
	}
	if strings.TrimSpace(cfg.TraceFrame) != "" {
		traceFrameRE, _ = regexp.Compile(cfg.TraceFrame)
	}
	if strings.TrimSpace(cfg.TraceAppFrame) != "" {
		traceAppRE, _ = regexp.Compile(cfg.TraceAppFrame)
	}
	kept, traceGroup, matched := scoreLines(deduped, keepRE, traceStartRE, traceFrameRE, traceAppRE, cfg.TraceKeepFrames)

	if !matched {
		body, keptCount, omitted := headTail(deduped, cfg.HeadLines, cfg.TailLines)
		return withFooter(strings.Join(body, "\n"), log, len(lines), keptCount, omitted, 0, dupCount, off)
	}

	context := expandContext(kept, cfg.ContextLines)
	enforceCap(kept, context, cfg.MaxKeptLines)

	out, keptCount, omittedLines, traceFrames := renderKept(deduped, kept, context, traceGroup)
	return withFooter(strings.Join(out, "\n"), log, len(lines), keptCount, omittedLines, traceFrames, dupCount, off)
}

// dedupWarnLines drops every repeat occurrence of a line matching warnRE,
// annotating the surviving first occurrence "(xN)" when it repeated. dupCount
// is the total number of dropped repeat occurrences.
func dedupWarnLines(lines []string, warnRE []*regexp.Regexp) (out []string, dupCount int) {
	if len(warnRE) == 0 {
		return append([]string(nil), lines...), 0
	}
	counts := make(map[string]int, len(lines))
	for _, l := range lines {
		if matchesAnyRE(l, warnRE) {
			counts[l]++
		}
	}
	out = make([]string, 0, len(lines))
	seen := make(map[string]bool, len(counts))
	for _, l := range lines {
		if !matchesAnyRE(l, warnRE) {
			out = append(out, l)
			continue
		}
		if seen[l] {
			dupCount++
			continue
		}
		seen[l] = true
		if counts[l] > 1 {
			out = append(out, fmt.Sprintf("%s (x%d)", l, counts[l]))
		} else {
			out = append(out, l)
		}
	}
	return out, dupCount
}

// scoreLines marks every line CompressLog always keeps: a keep-pattern match,
// a trace-start line, and a trace frame within traceKeepFrames or matching
// traceAppRE. traceGroup labels every line inside a trace block (start plus
// its contiguous frames, kept or not) with a shared id>0, so renderKept can
// tell a trace's dropped frames from ordinary omitted content. matched
// reports whether anything was kept at all.
func scoreLines(lines []string, keepRE []*regexp.Regexp, traceStartRE, traceFrameRE, traceAppRE *regexp.Regexp, traceKeepFrames int) (kept []bool, traceGroup []int, matched bool) {
	n := len(lines)
	kept = make([]bool, n)
	traceGroup = make([]int, n)
	groupID := 0
	i := 0
	for i < n {
		line := lines[i]
		if traceStartRE != nil && traceStartRE.MatchString(line) {
			groupID++
			kept[i] = true
			traceGroup[i] = groupID
			matched = true
			j := i + 1
			frameIdx := 0
			for j < n && traceFrameRE != nil && traceFrameRE.MatchString(lines[j]) {
				traceGroup[j] = groupID
				isApp := traceAppRE != nil && traceAppRE.MatchString(lines[j])
				if frameIdx < traceKeepFrames || isApp {
					kept[j] = true
				}
				frameIdx++
				j++
			}
			i = j
			continue
		}
		if matchesAnyRE(line, keepRE) {
			kept[i] = true
			matched = true
		}
		i++
	}
	return kept, traceGroup, matched
}

// expandContext marks, for every kept line, up to width lines on each side as
// context (never overriding an already-kept line).
func expandContext(kept []bool, width int) []bool {
	n := len(kept)
	context := make([]bool, n)
	if width <= 0 {
		return context
	}
	for i, k := range kept {
		if !k {
			continue
		}
		lo, hi := i-width, i+width
		if lo < 0 {
			lo = 0
		}
		if hi > n-1 {
			hi = n - 1
		}
		for x := lo; x <= hi; x++ {
			if !kept[x] {
				context[x] = true
			}
		}
	}
	return context
}

// enforceCap drops context lines (never kept lines) until the total
// kept+context count is at most maxLines, dropping the lines farthest from
// their nearest kept anchor first. maxLines<=0 means no cap.
func enforceCap(kept, context []bool, maxLines int) {
	if maxLines <= 0 {
		return
	}
	total := 0
	for i := range kept {
		if kept[i] || context[i] {
			total++
		}
	}
	need := total - maxLines
	if need <= 0 {
		return
	}
	dist := contextDistances(kept)
	type candidate struct{ idx, dist int }
	candidates := make([]candidate, 0, total)
	for i := range context {
		if context[i] {
			candidates = append(candidates, candidate{i, dist[i]})
		}
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		if candidates[a].dist != candidates[b].dist {
			return candidates[a].dist > candidates[b].dist
		}
		return candidates[a].idx > candidates[b].idx
	})
	for _, c := range candidates {
		if need <= 0 {
			break
		}
		context[c.idx] = false
		need--
	}
}

// contextDistances returns, for every index, its distance to the nearest
// kept[true] index (0 for a kept index itself).
func contextDistances(kept []bool) []int {
	n := len(kept)
	const inf = 1 << 30
	dist := make([]int, n)
	for i := range dist {
		dist[i] = inf
	}
	last := -inf
	for i := 0; i < n; i++ {
		if kept[i] {
			last = i
		}
		if last != -inf && i-last < dist[i] {
			dist[i] = i - last
		}
	}
	last = inf
	for i := n - 1; i >= 0; i-- {
		if kept[i] {
			last = i
		}
		if last != inf && last-i < dist[i] {
			dist[i] = last - i
		}
	}
	return dist
}

// renderKept builds the compressed body from lines given the final kept and
// context marks, replacing every contiguous unkept run with one marker: a
// trace run (traceGroup[i]!=0 for the run) collapses to "[... N frames
// collapsed]"; any other run collapses to "[... N lines omitted]".
func renderKept(lines []string, kept, context []bool, traceGroup []int) (out []string, keptCount, omittedLines, traceFrames int) {
	n := len(lines)
	i := 0
	for i < n {
		if kept[i] || context[i] {
			out = append(out, lines[i])
			keptCount++
			i++
			continue
		}
		j := i
		for j < n && !kept[j] && !context[j] {
			j++
		}
		runLen := j - i
		if traceGroup[i] != 0 {
			out = append(out, fmt.Sprintf("[... %d frames collapsed]", runLen))
			traceFrames += runLen
		} else {
			out = append(out, fmt.Sprintf("[... %d lines omitted]", runLen))
			omittedLines += runLen
		}
		i = j
	}
	return out, keptCount, omittedLines, traceFrames
}

// headTail is the fallback body when no pattern matched (or none are
// configured): the first head and last tail lines, separated by one omission
// marker when they don't already cover the whole log.
func headTail(lines []string, head, tail int) (out []string, keptCount, omitted int) {
	if head <= 0 {
		head = DefaultHeadLines
	}
	if tail <= 0 {
		tail = DefaultTailLines
	}
	n := len(lines)
	if head+tail >= n {
		return append([]string(nil), lines...), n, 0
	}
	out = append(out, lines[:head]...)
	omitted = n - head - tail
	out = append(out, fmt.Sprintf("[... %d lines omitted]", omitted))
	out = append(out, lines[n-tail:]...)
	return out, head + tail, omitted
}

// withFooter appends the omission summary and, when off successfully stores
// the full raw log, a retrieval marker for it.
func withFooter(body, fullLog string, total, kept, omittedLines, traceFrames, dupWarnings int, off Offloader) string {
	marker := ""
	if off != nil {
		if hash, err := off.Put([]byte(fullLog)); err == nil {
			marker = " — full log: " + retrieve.MarkerKind(hash, "log", len(fullLog))
		}
	}
	footer := fmt.Sprintf("[log compressed: kept %d of %d lines; omitted %d lines, %d trace frames, %d duplicate warnings%s]",
		kept, total, omittedLines, traceFrames, dupWarnings, marker)
	if body == "" {
		return footer
	}
	return body + "\n" + footer
}

func compileLogPatterns(pats []string) []*regexp.Regexp {
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

func matchesAnyRE(s string, res []*regexp.Regexp) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
