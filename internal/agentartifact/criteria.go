package agentartifact

import (
	"strconv"
	"strings"
)

// Section returns the text under the "## <heading>" line of doc, up to the next
// "## " heading or the end, trimmed. Empty when the heading is absent. It is the
// one extractor of a document's criteria section: apply_criteria applies this
// text to the story, and a reviewer's per-criterion verdict is judged against the
// same text (sty_99d3162b).
func Section(doc, heading string) string {
	var out []string
	in := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "##") && len(line) > 2 && (line[2] == ' ' || line[2] == '\t') {
			if in {
				break
			}
			in = strings.TrimSpace(line[2:]) == heading
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Criterion is one numbered acceptance criterion: the number it was written with
// and its text, continuation lines included, trimmed.
type Criterion struct {
	N    int
	Text string
}

// NumberedCriteria reads the numbered criteria out of text: a line that starts
// with "<n>." or "<n>)" opens a criterion, and the non-blank lines that follow
// until the next numbered line continue it. Text before the first numbered line
// belongs to no criterion. A repeated number keeps its first occurrence.
func NumberedCriteria(text string) []Criterion {
	var out []Criterion
	seen := map[int]bool{}
	cur := -1
	flush := func() {
		if cur >= 0 {
			out[cur].Text = strings.TrimSpace(out[cur].Text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if loc := numberedCriterion.FindStringSubmatchIndex(line); loc != nil && loc[0] == 0 {
			flush()
			n, err := strconv.Atoi(line[loc[2]:loc[3]])
			if err != nil || seen[n] {
				cur = -1
				continue
			}
			seen[n] = true
			out = append(out, Criterion{N: n, Text: line[loc[3]+1:]})
			cur = len(out) - 1
			continue
		}
		if cur >= 0 && line != "" {
			out[cur].Text += "\n" + line
		}
	}
	flush()
	return out
}
