package compact

import (
	"regexp"
	"strconv"
	"strings"
)

// DefaultRepeatMin is FoldRepeats' run-length threshold when min <= 0.
const DefaultRepeatMin = 3

// FoldRepeats collapses each run of at least min consecutive identical lines
// to the line once followed by "... (repeated N times)". min <= 0 means
// DefaultRepeatMin. UnfoldRepeats is the exact inverse.
func FoldRepeats(s string, min int) string {
	if min <= 0 {
		min = DefaultRepeatMin
	}
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		run := j - i
		if run >= min {
			out = append(out, lines[i], "... (repeated "+strconv.Itoa(run)+" times)")
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return strings.Join(out, "\n")
}

var repeatRE = regexp.MustCompile(`(?m)^(.*)\n\.\.\. \(repeated (\d+) times\)$`)

// UnfoldRepeats reverses FoldRepeats exactly, for the round-trip guard (Fold).
func UnfoldRepeats(s string) string {
	return repeatRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := repeatRE.FindStringSubmatch(m)
		line, n := sub[1], sub[2]
		count, err := strconv.Atoi(n)
		if err != nil {
			return m
		}
		lines := make([]string, count)
		for i := range lines {
			lines[i] = line
		}
		return strings.Join(lines, "\n")
	})
}

// ansiRE matches CSI (\x1b[...letter) and OSC (\x1b]...BEL) escape sequences.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]|\x1b\\][^\x07]*\x07")

// StripANSI removes terminal escape sequences. satelle's CLI output is
// JSON-derived text, never a captured terminal stream, so in practice there is
// nothing to strip and this is a no-op — kept for parity with the algorithm
// spec and for any text (e.g. a captured suite-run log) that does carry them.
func StripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}
