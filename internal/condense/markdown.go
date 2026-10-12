package condense

import (
	"regexp"
	"strings"
)

// The markdown structure Go recognises: code fences, inline code, headings,
// [[links]] and list items. Nothing here is a word of prose.

const fenceMark = "```"

var (
	listMarkerRE = regexp.MustCompile(`^(\s*)((?:[-*+]|\d+[.)])\s+)(.*)$`)
	inlineCodeRE = regexp.MustCompile("`[^`\n]*`")
	wikiLinkRE   = regexp.MustCompile(`\[\[[^\]\n]*\]\]`)
)

type blockKind int

const (
	blockKeep blockKind = iota // heading, code line, table row, blank: kept as written
	blockText                  // a paragraph or a list item: condensable prose
)

// block is one unit of a body. A keep block is lines kept verbatim; a text block
// is a paragraph or one list item, its lines joined into a single run of prose.
type block struct {
	kind   blockKind
	lines  []string // keep: the lines as written
	prefix string   // text: indent + list marker of the first line ("" for a paragraph)
	indent string   // text: indent of the continuation lines
	text   string   // text: the prose, lines joined by one space
}

func isHeading(trimmed string) bool { return strings.HasPrefix(trimmed, "#") }

func isTableRow(trimmed string) bool { return strings.HasPrefix(trimmed, "|") }

// parseBlocks cuts a body into blocks. A fenced code block is kept whole; so is
// a heading, a table row and a blank line; consecutive prose lines form a
// paragraph, and a list marker starts a new item.
func parseBlocks(body string) []block {
	var out []block
	var cur *block
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	keep := func(line string) {
		flush()
		out = append(out, block{kind: blockKeep, lines: []string{line}})
	}
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, fenceMark):
			inFence = !inFence
			keep(line)
		case inFence, trimmed == "", isHeading(trimmed), isTableRow(trimmed):
			keep(line)
		default:
			if m := listMarkerRE.FindStringSubmatch(line); m != nil {
				flush()
				cur = &block{kind: blockText, prefix: m[1] + m[2], indent: m[1] + strings.Repeat(" ", len(m[2])), text: strings.TrimSpace(m[3])}
				continue
			}
			if cur == nil {
				cur = &block{kind: blockText, text: trimmed}
				continue
			}
			cur.text += " " + trimmed
		}
	}
	flush()
	return out
}

// protectedSpans returns the [start,end) byte ranges of s that no rung may edit:
// inline code, [[links]] and every protected-pattern match.
func protectedSpans(s string, rules Rules) [][2]int {
	var spans [][2]int
	add := func(re *regexp.Regexp) {
		for _, loc := range re.FindAllStringIndex(s, -1) {
			spans = append(spans, [2]int{loc[0], loc[1]})
		}
	}
	add(inlineCodeRE)
	add(wikiLinkRE)
	for _, re := range rules.compiled.protected {
		add(re)
	}
	return mergeSpans(spans)
}

// mergeSpans sorts and unions overlapping ranges.
func mergeSpans(spans [][2]int) [][2]int {
	if len(spans) < 2 {
		return spans
	}
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j][0] < spans[j-1][0]; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	out := [][2]int{spans[0]}
	for _, sp := range spans[1:] {
		last := &out[len(out)-1]
		if sp[0] <= last[1] {
			if sp[1] > last[1] {
				last[1] = sp[1]
			}
			continue
		}
		out = append(out, sp)
	}
	return out
}

// maskedCodeAndLinks returns s with every inline-code span and [[link]] blanked
// to spaces of the same length, so a delimiter or terminator inside them is not
// seen while byte offsets stay valid.
func maskedCodeAndLinks(s string) string {
	b := []byte(s)
	for _, re := range []*regexp.Regexp{inlineCodeRE, wikiLinkRE} {
		for _, loc := range re.FindAllStringIndex(s, -1) {
			for i := loc[0]; i < loc[1]; i++ {
				b[i] = ' '
			}
		}
	}
	return string(b)
}
