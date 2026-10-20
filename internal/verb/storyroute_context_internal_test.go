package verb

import (
	"strings"
	"testing"
	"time"
)

// sty_12499ecc AC4: the route shows, per verdict, the context sources the
// reviewer was offered and the lookups it reports having made through them.
func TestRouteOutcomeShowsContextOfferedAndConsulted(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	withContext := ReviewerVerdict{Skill: "gate-a", Accept: true, Command: "claude -p"}
	withContext.ContextSources = []string{"src"}
	withContext.Consulted = []ConsultedRef{{Source: "src", Ref: "jira:ID-12"}}
	withContext.ConsultedDropped = 1
	offeredOnly := ReviewerVerdict{Skill: "gate-b", Accept: true, Command: "claude -p"}
	offeredOnly.ContextSources = []string{"src"}
	plain := ReviewerVerdict{Skill: "gate-c", Accept: true, Command: "claude -p"}

	got := renderOutcome("sty_x", "plan", "in_progress", []ReviewerVerdict{withContext, offeredOnly, plain}, nil, now)

	for _, want := range []string{
		"- **gate-a** — ACCEPT",
		"  - context: src\n",
		"  - consulted: src jira:ID-12\n",
		"  - consulted entries dropped (source not offered): 1\n",
		"- **gate-b** — ACCEPT",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("outcome missing %q:\n%s", want, got)
		}
	}
	// A reviewer offered nothing prints nothing about context, and gate-b was
	// offered a source but reported no lookup.
	b, c := section(got, "gate-b", "gate-c"), section(got, "gate-c", "")
	if !strings.Contains(b, "context: src") || strings.Contains(b, "consulted") {
		t.Errorf("gate-b block = %q", b)
	}
	if strings.Contains(c, "context") || strings.Contains(c, "consulted") {
		t.Errorf("gate-c block = %q", c)
	}
}

// section is the text from the bullet of skill from to the bullet of skill to
// ("" for the end).
func section(s, from, to string) string {
	i := strings.Index(s, "- **"+from+"**")
	if i < 0 {
		return ""
	}
	s = s[i:]
	if to != "" {
		if j := strings.Index(s, "- **"+to+"**"); j >= 0 {
			s = s[:j]
		}
	}
	return s
}
