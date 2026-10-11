package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Readiness mechanism (sty_5262592e): the three things a route's step knobs need
// the binary to DO — refuse a spent round budget, sequence a propose step, and
// record an edit to a still-editable definition. Every rule they apply
// (budget, propose, where the definition freezes) is read off the route; none
// is a status name.

func init() {
	Register(&Verb{
		Name:        "story-definition-edits",
		Description: "Enumerate a story's definition edits, optionally only those after its latest accepted from:to edge (enumeration only)",
		Invoke:      storyDefinitionEdits,
	})
}

// stepProposes reports whether the step the story is moving INTO declares that
// its performer runs before the entry gates. False when the route cannot be
// resolved: an unresolvable route keeps the default order, and the gate itself
// refuses on it.
func stepProposes(ctx context.Context, current workitem.Item, to string) bool {
	spec, _, _, ok := governingSpec(ctx, current)
	if !ok || !spec.HasEdge(current.Status, to) {
		return false
	}
	st, found := spec.StateNamed(to)
	return found && st.Propose
}

// planCriteriaActor is the actor on the definition_edited row written when an
// accepted document's criteria are applied on entry, so the trail tells that
// edit apart from one a person or the driving session made.
const planCriteriaActor = "accepted-plan"

// applyPlannedCriteria applies, on entry to a step that declares
// `apply_criteria`, the acceptance criteria authored under the declared heading
// in the declared story document (sty_4d9df9a0). It returns the updated item
// when it writes and `current` unchanged otherwise, so the caller can rebind it
// before any performer or reviewer reads the story. The document, the heading
// and the step are all route data. A missing document, a missing or empty
// section, or text the story already carries writes nothing; an unreadable
// document refuses the transition rather than skipping the apply silently.
func applyPlannedCriteria(ctx context.Context, current workitem.Item, to string, now time.Time) (workitem.Item, error) {
	if current.Kind != workitem.KindStory {
		return current, nil
	}
	spec, _, _, ok := governingSpec(ctx, current)
	if !ok || !spec.HasEdge(current.Status, to) {
		return current, nil
	}
	st, found := spec.StateNamed(to)
	if !found || st.ApplyCriteriaDoc == "" || st.ApplyCriteriaHeading == "" {
		return current, nil
	}
	file, dir := safeName(st.ApplyCriteriaDoc), attachmentDir(current)
	if file == "" || dir == "" {
		return current, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, file))
	if errors.Is(err, fs.ErrNotExist) {
		return current, nil
	}
	if err != nil {
		return current, fmt.Errorf("verb: transition %s→%s: reading the %q document to apply its criteria: %w", current.Status, to, st.ApplyCriteriaDoc, err)
	}
	criteria := markdownSection(string(data), st.ApplyCriteriaHeading)
	if criteria == "" || strings.Join(strings.Fields(criteria), " ") == strings.Join(strings.Fields(current.AcceptanceCriteria), " ") {
		return current, nil
	}
	store, err := requireWorkItem()
	if err != nil {
		return current, err
	}
	updated, err := store.Update(ctx, current.ID, workitem.UpdateInput{AcceptanceCriteria: &criteria, ExpectStatus: &current.Status}, now)
	if err != nil {
		return current, fmt.Errorf("verb: transition %s→%s: applying the criteria from the %q document: %w", current.Status, to, st.ApplyCriteriaDoc, err)
	}
	payload, perr := json.Marshal(definitionEditPayload{
		Field: "acceptance_criteria", Old: current.AcceptanceCriteria, New: criteria,
		Actor: planCriteriaActor, Source: st.ApplyCriteriaDoc + "#" + st.ApplyCriteriaHeading,
	})
	if perr == nil {
		appendLedgerEntry(ctx, current.ID, ledger.KindDefinitionEdited, planCriteriaActor,
			fmt.Sprintf("definition applied from the accepted plan document %q (section %q)", st.ApplyCriteriaDoc, st.ApplyCriteriaHeading),
			payload, now)
	}
	return updated, nil
}

// markdownSection returns the text under the "## <heading>" line of doc, up to
// the next "## " heading or the end, trimmed. Empty when the heading is absent.
func markdownSection(doc, heading string) string {
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

// refuseSpentRejectBudget refuses another presentation of the current→to edge
// once its destination step's reject_budget rejected rounds are spent. The
// refusal is in the blocked-reason format (agent-consultation §4): the orchestrator
// parks with it and asks the developer, so the objection is quoted, not
// paraphrased. A step with no declared budget never refuses.
func refuseSpentRejectBudget(ctx context.Context, current workitem.Item, to string) error {
	if current.Kind != workitem.KindStory {
		return nil
	}
	spec, _, _, ok := governingSpec(ctx, current)
	if !ok || !spec.HasEdge(current.Status, to) {
		return nil
	}
	st, found := spec.StateNamed(to)
	if !found || st.RejectBudget < 1 {
		return nil
	}
	rounds := ledger.CountRejectedRounds(storyLedgerEntries(ctx, current.ID), current.Status, to)
	if rounds.Rounds < st.RejectBudget {
		return nil
	}
	return fmt.Errorf(
		"transition %s→%s refused: edge rejected %d times (reject budget %d) — last objection: %q — decision needed: park the story to blocked with this reason and message the developer; a resume restarts the budget",
		current.Status, to, rounds.Rounds, st.RejectBudget, rounds.ObjectionLine())
}

// storyLedgerEntries lists every ledger row of a story, oldest first; nil when
// no ledger is wired (an unwired ledger counts no rounds, so nothing refuses).
func storyLedgerEntries(ctx context.Context, storyID string) []ledger.Entry {
	led, err := requireLedger()
	if err != nil || led == nil || strings.TrimSpace(storyID) == "" {
		return nil
	}
	entries, err := led.ListByStory(ctx, storyID, "")
	if err != nil {
		return nil
	}
	return entries
}

// definitionEditPayload is one definition_edited row's payload.
type definitionEditPayload struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
	Actor string `json:"actor,omitempty"`
	// Source names where an applied edit came from ("<doc>#<heading>"); empty for
	// an edit made directly.
	Source string `json:"source,omitempty"`
}

// recordDefinitionEdits appends one definition_edited row per field a committed
// story-set changed while the route still leaves the definition editable. The
// actor is the satelle user (account principal, or the git user local-only),
// falling back to the executor role the other definition rows use. Best-effort
// like every other trail row: a failed append never reverts the edit that
// already committed.
func recordDefinitionEdits(ctx context.Context, it workitem.Item, edits []AmendField, now time.Time) {
	if len(edits) == 0 {
		return
	}
	actor := resolveActor()
	if actor == "" {
		actor = "executor"
	}
	for _, f := range edits {
		payload, err := json.Marshal(definitionEditPayload{Field: f.Field, Old: f.Old, New: f.New, Actor: actor})
		if err != nil {
			continue
		}
		appendLedgerEntry(ctx, it.ID, ledger.KindDefinitionEdited, actor,
			fmt.Sprintf("definition edited while editable: %s (before and after are on the row)", f.Field), payload, now)
	}
}

// DefinitionEdit is one recorded edit, as enumerated for a gate payload or
// `satelle story definition-edits`.
type DefinitionEdit struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
	Actor string `json:"actor,omitempty"`
	// Source is "<doc>#<heading>" for an edit applied from an accepted document.
	Source string `json:"source,omitempty"`
	At     string `json:"at,omitempty"`
}

func editsFrom(entries []ledger.Entry) []DefinitionEdit {
	var out []DefinitionEdit
	for _, e := range entries {
		var p definitionEditPayload
		if e.Kind != ledger.KindDefinitionEdited || json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		out = append(out, DefinitionEdit{
			Field: p.Field, Old: p.Old, New: p.New, Actor: p.Actor, Source: p.Source,
			At: e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// DefinitionEdits returns every definition edit recorded for itemID, oldest
// first. Nil-safe when no ledger is wired.
func DefinitionEdits(ctx context.Context, itemID string) []DefinitionEdit {
	return editsFrom(storyLedgerEntries(ctx, itemID))
}

type definitionEditsReq struct {
	ID string `json:"id"`
	// SinceEdge is "from:to": report only the edits after the latest accepted
	// transition from→to. Empty reports every edit.
	SinceEdge string `json:"since_edge,omitempty"`
}

// DefinitionEditsResult is report-only: which edits were found and the edge they
// were counted against. It carries no pass/fail — a gate's check decides what a
// non-empty list means.
type DefinitionEditsResult struct {
	StoryID   string           `json:"story_id"`
	SinceEdge string           `json:"since_edge,omitempty"`
	Count     int              `json:"count"`
	Edits     []DefinitionEdit `json:"edits"`
}

func storyDefinitionEdits(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req definitionEditsReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	if _, err := store.Get(ctx, req.ID); err != nil {
		return nil, err
	}
	entries := storyLedgerEntries(ctx, req.ID)
	res := DefinitionEditsResult{StoryID: req.ID, SinceEdge: req.SinceEdge, Edits: []DefinitionEdit{}}
	if req.SinceEdge == "" {
		res.Edits = append(res.Edits, editsFrom(entries)...)
	} else {
		from, to, ok := strings.Cut(req.SinceEdge, ":")
		if !ok || from == "" || to == "" {
			return nil, fmt.Errorf("verb: since_edge must be <from>:<to>, got %q", req.SinceEdge)
		}
		res.Edits = append(res.Edits, editsFrom(ledger.DefinitionEditedSince(entries, from, to))...)
	}
	res.Count = len(res.Edits)
	return json.Marshal(res)
}
