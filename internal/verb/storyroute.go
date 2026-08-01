// The route document (sty_39e2d9df): ONE artifact per story carrying both the
// route it will walk and the reasoning behind every outcome so far.
//
// It is written FORWARD. The plan half is re-rendered from the governing spec on
// every transition, so the "you are here" marker stays true; the outcome half is
// APPENDED and never rewritten, so the artifact is a trail rather than a
// current-state view. They are deliberately the same document: a route the
// operator reads separately from the verdicts would be two things that drift.
//
// Best-effort throughout. A transition that passed its gates is committed before
// this runs, and a doc write must never revert it.
package verb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/wfroute"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// RouteDocName is the single artifact's name. One name, one writer — that is how
// "route and reasoning are one artifact" is enforced, structurally.
const RouteDocName = "route"

// routeOutcomesHeading separates the re-rendered plan half from the appended
// outcome half. Everything at or after it is preserved verbatim across writes.
const routeOutcomesHeading = "## Outcomes"

// recordRoute re-renders the story's route and appends the outcome of the
// transition that just enacted. Silent on any failure: the caller sits in the
// post-commit, best-effort band beside recordChangeSet.
func recordRoute(ctx context.Context, item workitem.Item, from, to string, verdicts []ReviewerVerdict, unresolved []string, now time.Time) {
	if item.Kind != workitem.KindStory && item.Kind != workitem.KindTask {
		return
	}
	spec, wfName, advisors, ok := governingSpec(ctx, item)
	if !ok {
		return
	}
	prior := routeOutcomes(item)
	body := renderRouteDoc(spec, wfName, item, to, prior+renderOutcome(item.ID, from, to, verdicts, unresolved, now), advisors)
	_, _, _ = writeAttachedDoc(ctx, item, RouteDocName, RouteDocName, body, now)
}

// StoryRoute returns the story's route document for the read surface
// (`satelle story route <id>`). When no document exists yet — a story that has
// not transitioned — the route is rendered LIVE from the governing spec, so the
// question "what is my route" is answerable from backlog, before any outcome
// exists. That is what makes the route readable without opening a workflow file.
func StoryRoute(ctx context.Context, id string) (string, error) {
	store, err := requireWorkItem()
	if err != nil {
		return "", err
	}
	item, err := store.Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("verb: route: %s: %w", id, err)
	}
	if body := readRouteDoc(item); body != "" {
		return body, nil
	}
	spec, wfName, advisors, ok := governingSpec(ctx, item)
	if !ok {
		return "", fmt.Errorf("verb: route: %s has no governing workflow with a parseable lifecycle", id)
	}
	return renderRouteDoc(spec, wfName, item, item.Status, "", advisors), nil
}

// renderRouteDoc assembles the whole artifact: the plan half rendered fresh, then
// the outcome half (prior blocks plus any new one) under a stable heading.
func renderRouteDoc(spec wfdot.Spec, wfName string, item workitem.Item, at, outcomes string, advisors []wfroute.Advisor) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Route — %s\n\n", item.ID)
	fmt.Fprintf(&b, "`%s` · category %s · currently **%s**\n\n", item.ID, orDash(item.Category), item.Status)
	// Advisors are declared by the route's `advise` lines — entry dispatch is
	// retired (sty_05a5e203), so nothing dispatches them; the orchestrator
	// consults them and records the advice.
	b.WriteString(wfroute.Build(spec, wfName, item.Tags, advisors).Render(at))
	b.WriteString("\n" + routeOutcomesHeading + "\n")
	if strings.TrimSpace(outcomes) == "" {
		b.WriteString("\n(no step has resolved yet)\n")
		return b.String()
	}
	b.WriteString(outcomes)
	return b.String()
}

// renderOutcome renders one enacted transition's outcome: every reviewer's
// verdict with its reasoning, and a pointer to the full output rather than a copy
// of it — the route stays legible, the ledger keeps the long form.
func renderOutcome(itemID, from, to string, verdicts []ReviewerVerdict, unresolved []string, now time.Time) string {
	var b strings.Builder
	result := "accepted"
	if len(verdicts) == 0 {
		result = "ungated"
	}
	for _, v := range verdicts {
		if !v.Accept {
			result = "rejected"
		}
	}
	fmt.Fprintf(&b, "\n### %s → %s — %s · %s\n\n", from, to, result, now.UTC().Format(time.RFC3339))
	for _, v := range verdicts {
		verdict := "ACCEPT"
		if !v.Accept {
			verdict = "REJECT"
		}
		scope := ""
		if v.System {
			scope = " (always-on)"
		}
		fmt.Fprintf(&b, "- **%s**%s — %s\n", v.Skill, scope, verdict)
		if notes := excerpt(v.Notes); notes != "" {
			fmt.Fprintf(&b, "  - notes: %s\n", notes)
		}
		if reasoning := excerpt(v.Reasoning); reasoning != "" {
			fmt.Fprintf(&b, "  - reasoning: %s\n", reasoning)
		}
		fmt.Fprintf(&b, "  - full output: `satelle ledger list --story %s --kind %s`%s\n",
			itemID, reviewKindFor(v.Accept), modelNote(v.Model))
	}
	for _, skill := range unresolved {
		fmt.Fprintf(&b, "- **%s** — NOT JUDGED (rubric does not resolve in the substrate; this advance was ungated)\n", skill)
	}
	if len(verdicts) == 0 && len(unresolved) == 0 {
		b.WriteString("- no reviewer governed this edge\n")
	}
	return b.String()
}

// routeOutcomes returns the accumulated outcome blocks from the existing
// document, or "" when there are none. Everything after the outcomes heading is
// carried verbatim — appended history is never re-derived.
func routeOutcomes(item workitem.Item) string {
	body := readRouteDoc(item)
	_, after, found := strings.Cut(body, routeOutcomesHeading+"\n")
	if !found {
		return ""
	}
	if strings.Contains(after, "(no step has resolved yet)") {
		return ""
	}
	return strings.TrimRight(after, "\n") + "\n"
}

// readRouteDoc reads the raw route document, frontmatter stripped. Empty when
// there is none.
func readRouteDoc(item workitem.Item) string {
	dir := attachmentDir(item)
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, RouteDocName+".md"))
	if err != nil {
		return ""
	}
	body := string(data)
	if rest, ok := strings.CutPrefix(body, "---\n"); ok {
		if _, after, found := strings.Cut(rest, "\n---\n"); found {
			body = strings.TrimLeft(after, "\n")
		}
	}
	return body
}

// governingSpec resolves the workflow governing item and parses its lifecycle.
// ok=false when no workflow resolves or its lifecycle is not parseable — the
// route is then simply unavailable, never wrong.
func governingSpec(ctx context.Context, item workitem.Item) (wfdot.Spec, string, []wfroute.Advisor, bool) {
	idx, err := requireDocIndex()
	if err != nil {
		return wfdot.Spec{}, "", nil, false
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return wfdot.Spec{}, "", nil, false
	}
	spec, name, advisors, err := wfgovern.SpecFor(wfs, item)
	if err != nil {
		return wfdot.Spec{}, "", nil, false
	}
	return spec, name, advisors, true
}

func reviewKindFor(accept bool) string {
	if accept {
		return "review_accept"
	}
	return "review_reject"
}

func modelNote(model string) string {
	if strings.TrimSpace(model) == "" {
		return ""
	}
	return " (model " + model + ")"
}

// routeExcerptLimit bounds a verdict's prose in the route. The route records a
// POINTER to the reviewer's full output, never a transcript of it: a functional
// check's notes carry its whole script, and a judgment reviewer's can run to
// paragraphs. Both belong on the ledger, which the bullet above them names.
const routeExcerptLimit = 300

// excerpt flattens reviewer prose to one line and bounds it, so one verdict stays
// one bullet — the route's legibility budget is per step, not per paragraph.
func excerpt(s string) string {
	flat := strings.Join(strings.Fields(s), " ")
	if len(flat) <= routeExcerptLimit {
		return flat
	}
	return strings.TrimSpace(flat[:routeExcerptLimit]) + " […]"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
