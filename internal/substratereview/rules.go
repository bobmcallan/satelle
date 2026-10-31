package substratereview

import (
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/ctxaudit"
)

// Rule is one constitution rule the codebase is checked against: a principle
// the constitution links, or a session principle (principles:session).
type Rule struct {
	Name string `json:"rule"`
	// Origin says why the rule is in the set: "constitution" (linked at Line of
	// the constitution) or "session" (always resident, not linked).
	Origin string `json:"origin"`
	Line   int    `json:"line,omitempty"`
}

// Rules enumerates every rule, constitution links first in the order they
// appear, then the session principles the constitution does not link, sorted.
// The binary enumerates so that no rule can go missing from the report; whether
// the code contradicts one is the judge's.
func Rules(constitution string, session []string) []Rule {
	var out []Rule
	seen := map[string]bool{}
	masked := ctxaudit.MaskQuotedCode(constitution)
	for i, line := range strings.Split(masked, "\n") {
		for _, m := range ctxaudit.WikiLinkRe.FindAllStringSubmatch(line, -1) {
			name := strings.TrimSpace(m[1])
			if name == "" || seen[name] || strings.HasPrefix(name, "sty_") || strings.HasPrefix(name, "tsk_") {
				continue
			}
			seen[name] = true
			out = append(out, Rule{Name: name, Origin: "constitution", Line: i + 1})
		}
	}
	rest := append([]string(nil), session...)
	sort.Strings(rest)
	for _, name := range rest {
		if !seen[name] {
			seen[name] = true
			out = append(out, Rule{Name: name, Origin: "session"})
		}
	}
	return out
}
