package wfdot

import (
	"fmt"
	"sort"
	"strings"
)

// OverFireWarnings returns advisory (non-fatal) messages for scoped reviewers
// whose on= state is likely gate-specific but will re-fire on rework/loop
// inbound edges. Mechanism only — never a refusal. See satelle help workflows
// ("Binding a reviewer: edge CSV vs scoped on=").
//
// Rule (deliberately narrow so estimate-style always-on stays clean):
//   - edge-less agent=reviewer with a single-state on= (not "*" and not multi-state)
//   - that state has ≥2 inbound edges
//   - at least one inbound edge f→t is a rework/loop: t can reach f forward
//
// Step-summary nodes are skipped (they are post-transition summarisers).
func OverFireWarnings(s Spec) []string {
	// inbound[to] = list of from states that edge into to
	inbound := map[string][]string{}
	for _, tr := range s.Transitions {
		inbound[tr.To] = append(inbound[tr.To], tr.From)
	}
	// Forward reachability cache: start → set of reachable states (incl. start)
	reachCache := map[string]map[string]bool{}
	reach := func(start string) map[string]bool {
		if r, ok := reachCache[start]; ok {
			return r
		}
		r := s.forwardReachable(start)
		reachCache[start] = r
		return r
	}

	// Node name by skill for readable messages (scoped nodes often have skill = name intent)
	nameBySkill := map[string]string{}
	for _, st := range s.States {
		if st.Skill != "" {
			nameBySkill[st.Skill] = st.Name
		}
	}

	var warns []string
	for _, st := range s.States {
		if st.Agent != "reviewer" || st.Skill == "" || len(st.On) == 0 {
			continue
		}
		if st.Skill == StepSummarySkill {
			continue
		}
		// Skip wildcards and multi-state always-on (legit estimate/step pattern).
		if containsStr(st.On, "*") || len(st.On) >= 2 {
			continue
		}
		t := st.On[0]
		froms := inbound[t]
		if len(froms) < 2 {
			continue
		}
		// Is any inbound a rework/loop edge? f→t rework when t reaches f forward.
		var reworkFrom string
		for _, f := range froms {
			if reach(t)[f] {
				reworkFrom = f
				break
			}
		}
		if reworkFrom == "" {
			continue
		}
		nodeName := st.Name
		if nodeName == "" {
			nodeName = nameBySkill[st.Skill]
		}
		if nodeName == "" {
			nodeName = st.Skill
		}
		// Dedup froms for stable count message
		uniq := uniqueSorted(froms)
		warns = append(warns, fmt.Sprintf(
			`scoped reviewer %q (on=%q): %q has %d inbound edges including rework edge %s→%s — gate-specific? bind it to the edge (prompt="@skill:…"); see satelle help workflows`,
			nodeName, t, t, len(uniq), reworkFrom, t,
		))
	}
	sort.Strings(warns)
	return warns
}

// forwardReachable returns every state reachable from start by following edges
// forward (inclusive of start).
func (s Spec) forwardReachable(start string) map[string]bool {
	adj := map[string][]string{}
	for _, tr := range s.Transitions {
		adj[tr.From] = append(adj[tr.From], tr.To)
	}
	reach := map[string]bool{start: true}
	stack := []string{start}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, to := range adj[n] {
			if !reach[to] {
				reach[to] = true
				stack = append(stack, to)
			}
		}
	}
	return reach
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// FormatOverFireWarning is a small helper for callers that want a single-line
// prefix; currently unused by validate but kept for tests / reuse.
func FormatOverFireWarning(msg string) string {
	return strings.TrimSpace(msg)
}
