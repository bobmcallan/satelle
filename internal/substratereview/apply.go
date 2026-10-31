package substratereview

import (
	"fmt"
	"strings"
)

// ApplyCategory is the category of the story --apply raises for a cut: the
// repo's substrate lane.
const ApplyCategory = "substrate"

// ApplyTag marks every story --apply raises.
const ApplyTag = "substrate-review"

// Refusal is why an item cannot be applied in this repo, or "" when it can.
// An embedded item has no file in this repo to cut, and a team item is owned by
// the project tier; a cut to either is raised where it lives, not here.
func Refusal(it ReportItem) string {
	switch it.Origin {
	case OriginEmbedded:
		return fmt.Sprintf("%s: origin embedded — it is the binary's default with no file in this repo, so there is nothing here to cut; change it in satelle itself", it.Key)
	case OriginTeam:
		return fmt.Sprintf("%s: origin team — the project tier owns it (%s); raise the cut in the tier's repo", it.Key, it.Source)
	}
	return ""
}

// StoryFor is the story --apply raises for one item: its title, body and
// acceptance criteria carry the item, its origin, its disposition and its
// evidence, so the story stands on its own when it is planned.
func StoryFor(it ReportItem) (title, body, criteria string) {
	title = fmt.Sprintf("substrate review: %s %s", it.Disposition, it.Key)
	var b strings.Builder
	fmt.Fprintf(&b, "Raised by `satelle substrate review --apply %s`.\n\n", it.Key)
	fmt.Fprintf(&b, "- item: %s (%s)\n", it.Key, it.Kind)
	fmt.Fprintf(&b, "- origin: %s\n", it.Origin)
	fmt.Fprintf(&b, "- source: %s\n", it.Source)
	fmt.Fprintf(&b, "- disposition: %s\n", it.Disposition)
	if it.Reason != "" {
		fmt.Fprintf(&b, "- reason: %s\n", oneLine(it.Reason))
	}
	if it.Rule != "" {
		fmt.Fprintf(&b, "- rule: %s\n", it.Rule)
	}
	for _, l := range it.KeptLines {
		fmt.Fprintf(&b, "- kept: %s\n", oneLine(l))
	}
	b.WriteString("\n## Evidence\n\n")
	for _, l := range it.Evidence.Lines() {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	body = b.String()

	evidence := it.Evidence.Lines()[0]
	var first string
	switch it.Disposition {
	case DispRemove:
		first = fmt.Sprintf("%s is removed from %s, and the default that covers it governs in its place (evidence: %s).", it.Key, it.Source, evidence)
	case DispReduce:
		first = fmt.Sprintf("%s keeps only these lines: %s (evidence: %s).", it.Key, quoteLines(it.KeptLines), evidence)
	case DispKeep:
		first = fmt.Sprintf("%s is kept, and the quality rule it carries that the default lacks (%s) is named beside it (evidence: %s).", it.Key, nonEmpty(it.Rule, oneLine(it.Reason)), evidence)
	case DispConflict:
		first = fmt.Sprintf("%s no longer contradicts %s (evidence: %s).", it.Key, nonEmpty(it.Rule, "the constitution or the BDD baseline"), evidence)
	default:
		first = fmt.Sprintf("%s is judged and given a disposition by its owner (the review returned %s; evidence: %s).", it.Key, it.Disposition, evidence)
	}
	criteria = "1. " + first + "\n2. `satelle validate` passes after the change."
	return title, body, criteria
}

func quoteLines(ls []string) string {
	if len(ls) == 0 {
		return "(the review named none)"
	}
	q := make([]string, len(ls))
	for i, l := range ls {
		q[i] = "\"" + oneLine(l) + "\""
	}
	return strings.Join(q, "; ")
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}
