package condense

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// dropFiller removes the configured filler phrases from body: outside code
// fences, headings, table rows, inline code, links and protected tokens. Line
// structure is preserved.
func dropFiller(body string, rules Rules) string {
	re := rules.compiled.drop
	if re == nil {
		return body
	}
	lines := strings.Split(body, "\n")
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, fenceMark) {
			inFence = !inFence
			continue
		}
		if inFence || trimmed == "" || isHeading(trimmed) || isTableRow(trimmed) {
			continue
		}
		lines[i] = dropInFreeText(line, re, rules)
	}
	return strings.Join(lines, "\n")
}

// dropInFreeText removes re's matches from the stretches of line that no
// protected span covers, and leaves every protected span as written.
func dropInFreeText(line string, re *regexp.Regexp, rules Rules) string {
	var b strings.Builder
	at := 0
	for _, sp := range protectedSpans(line, rules) {
		b.WriteString(re.ReplaceAllString(line[at:sp[0]], ""))
		b.WriteString(line[sp[0]:sp[1]])
		at = sp[1]
	}
	b.WriteString(re.ReplaceAllString(line[at:], ""))
	return b.String()
}

// lead reduces body to its lead: headings, code, tables and blank lines as
// written; for each paragraph and list item its first sentence, plus every
// clause of the later sentences that holds a protected token.
func lead(body string, rules Rules) string {
	var out []string
	for _, bl := range parseBlocks(body) {
		if bl.kind == blockKeep {
			out = append(out, bl.lines...)
			continue
		}
		out = append(out, bl.prefix+leadOfText(bl.text, rules))
	}
	return strings.Join(out, "\n")
}

// elision joins the kept parts of a paragraph where prose was left out.
const elision = " … "

// leadOfText is the lead of one run of prose.
func leadOfText(text string, rules Rules) string {
	sentences := splitAfter(text, rules.SentenceTerminators, endsSentence(rules))
	if len(sentences) <= 1 {
		return text
	}
	parts := []string{strings.TrimSpace(sentences[0])}
	for _, s := range sentences[1:] {
		if kept := keepProtectedClauses(s, rules); kept != "" {
			parts = append(parts, kept)
		}
	}
	return strings.Join(parts, elision)
}

// keepProtectedClauses returns the clauses of sentence that hold a protected
// token, in order and as written, or "" when none does.
func keepProtectedClauses(sentence string, rules Rules) string {
	var kept []string
	for _, clause := range splitAfter(sentence, rules.ClauseDelimiters, nil) {
		if holdsProtected(clause, rules) {
			kept = append(kept, strings.TrimSpace(clause))
		}
	}
	return strings.Join(kept, " ")
}

func holdsProtected(s string, rules Rules) bool {
	for _, re := range rules.compiled.protected {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// endsSentence reports whether a terminator closing `before` and followed by
// `after` ends a sentence: white space (or the end) follows it, and the word it
// closes is not one of the configured abbreviations.
func endsSentence(rules Rules) func(before, after string) bool {
	return func(before, after string) bool {
		r, _ := utf8.DecodeRuneInString(after)
		if after != "" && !unicode.IsSpace(r) {
			return false
		}
		word := strings.ToLower(before[strings.LastIndexFunc(before, unicode.IsSpace)+1:])
		for _, abbr := range rules.SentenceExceptions {
			if word == strings.ToLower(abbr) {
				return false
			}
		}
		return true
	}
}

// splitAfter cuts s after each occurrence of any of seps that sits outside
// inline code and links, and (when accept is set) that accept approves, given
// the text up to and including the separator and the text after it. Every piece
// keeps its separator, so the pieces rejoin to s.
func splitAfter(s string, seps []string, accept func(before, after string) bool) []string {
	masked := maskedCodeAndLinks(s)
	var out []string
	start := 0
	for i := 0; i < len(masked); {
		cut := 0
		for _, sep := range seps {
			if sep != "" && strings.HasPrefix(masked[i:], sep) && (accept == nil || accept(masked[:i+len(sep)], masked[i+len(sep):])) {
				if len(sep) > cut {
					cut = len(sep)
				}
			}
		}
		if cut == 0 {
			i++
			continue
		}
		out = append(out, s[start:i+cut])
		i += cut
		start = i
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
