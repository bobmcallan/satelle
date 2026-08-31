package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{
		Name:        "story-amend",
		Description: "Amend a story's frozen definition fields under the amend gate",
		Invoke:      storyAmend,
	})
}

// amendReq is the request body for story-amend: the story id, the definition
// fields to change (nil = leave alone), and the mandatory reason recorded with
// the amendment.
type amendReq struct {
	ID                 string  `json:"id"`
	Title              *string `json:"title,omitempty"`
	Body               *string `json:"body,omitempty"`
	AcceptanceCriteria *string `json:"acceptance_criteria,omitempty"`
	Category           *string `json:"category,omitempty"`
	Reason             string  `json:"reason,omitempty"`
}

// storyAmend is the ONLY writer of a story's frozen definition fields once the
// story has left its entry state (sty_81aa4d8f). `story set` still refuses them
// outright: the freeze exists so an agent cannot quietly weaken its own
// acceptance criteria to make a gate pass. Amend does not lift that — it makes
// the change VISIBLE and JUDGED: a mandatory reason, a reviewer gate the repo
// declares (the amend_review lifecycle hook), and a ledger row carrying every
// field's before and after.
//
// Fail-closed by construction. A repo that declares no amend gate gets no amend:
// with nothing to judge the correction, the freeze holds and cancel-and-re-raise
// remains the exit. That is the opposite of create_review, whose absence keeps a
// bare create legal — a create needs no permission, piercing a freeze does.
//
// Amend never touches status: no transition, no seat, no dispatch. It runs
// wherever the story sits — a performing state, or PARKED in blocked, which is
// the case that used to force cancel-and-re-raise on a story stuck behind a
// wrong definition. Terminal and cancel states are refused: a closed record is
// history, and correcting it is a new story.
func storyAmend(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req amendReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	current, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if current.Kind != workitem.KindStory {
		return nil, fmt.Errorf("verb: amend is story-only — a %s carries no frozen definition (edit it with %s set)",
			current.Kind, current.Kind)
	}
	// Canonicalise the category BEFORE diffing so a casing-only change is the
	// no-op it looks like, exactly as story-set does.
	if req.Category != nil {
		canon, cerr := canonicaliseCategory(*req.Category)
		if cerr != nil {
			return nil, cerr
		}
		req.Category = &canon
	}
	// One function owns what "a definition field" is, shared with the story-set
	// refusal, so the freeze and its amendment can never drift apart.
	changed := definitionFieldsChanged(current, setReq{
		Title:              req.Title,
		Body:               req.Body,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Category:           req.Category,
	})
	if len(changed) == 0 {
		return json.Marshal(current) // idempotent: nothing proposed, nothing judged
	}
	if strings.TrimSpace(req.Reason) == "" {
		return nil, fmt.Errorf(
			"verb: amend requires --reason — an amendment of [%s] on %s is recorded on the ledger and judged by the amend gate, and both read the reason",
			strings.Join(changed, ", "), current.ID)
	}
	if err := refuseUnamendableState(ctx, current); err != nil {
		return nil, err
	}
	fields := amendFields(current, req)
	// Category is the lane selector, so amending it re-derives the route. Refuse
	// an amendment that would strand the story on a route with no such state —
	// the off-route condition routedrift reports, which an amend must not create.
	if err := refuseOffRouteCategory(ctx, current, req.Category); err != nil {
		return nil, err
	}
	dec, err := runAmendGate(ctx, current, fields, req.Reason)
	if err != nil {
		return nil, err
	}
	// Everything above this line is read-only: a refusal leaves the story exactly
	// as it was, which is what makes "rejection changes nothing" structural
	// rather than a promise.
	now := time.Now()
	upd := workitem.UpdateInput{
		Title:              req.Title,
		Body:               req.Body,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Category:           req.Category,
		// Compare-and-set (sty_2c71eff6): the gate ran against the story as it
		// stood, so the write applies only while the row still holds that status.
		// A transition that landed during the review loses loudly instead of
		// silently taking an amendment judged against a different state.
		ExpectStatus: &current.Status,
	}
	it, err := store.Update(ctx, req.ID, upd, now)
	if err != nil {
		return nil, staleWriteError(current, nil, err)
	}
	appendLedgerEntry(ctx, it.ID, ledger.KindDefinitionAmended, "executor",
		amendLedgerBody(fields, req.Reason, dec.Skill),
		amendPayload(fields, req.Reason, dec), now)
	appendOpLog("story-amend", it.ID,
		fmt.Sprintf("amended [%s] at status %s: %s", strings.Join(changed, ", "), current.Status, req.Reason), now)
	notifyChange(panelTopic(it.Kind))
	return json.Marshal(it)
}

// runAmendGate runs the repo's declared amend reviewer and returns its accepting
// verdict. Every other outcome is an error: no reviewer wired, no gate declared,
// a reviewer failure, or a reject. The absence of a judge is a refusal here, not
// a pass — see storyAmend.
func runAmendGate(ctx context.Context, current workitem.Item, fields []AmendField, reason string) (GateDecision, error) {
	undeclared := fmt.Errorf(
		"verb: refusing to amend %s — this repo declares no amend gate, so nothing would judge the correction; declare the %s lifecycle hook in the workflow (skill + agent), or cancel this story with a recorded reason and re-raise it carrying supersedes:%s",
		current.ID, "amend_review", current.ID)
	if amendReviewer == nil {
		return GateDecision{}, undeclared
	}
	dec, gerr := amendReviewer.ReviewAmend(ctx, AmendDraft{
		Item:   current,
		Status: current.Status,
		Fields: fields,
		Reason: reason,
	})
	if gerr != nil {
		return GateDecision{}, gerr
	}
	if !dec.Gated {
		return GateDecision{}, undeclared
	}
	if !dec.Accept {
		return GateDecision{}, fmt.Errorf("amendment of %s rejected by %s: %s", current.ID, dec.Skill, dec.Notes)
	}
	return dec, nil
}

// refuseUnamendableState refuses an amendment the route says has no point:
// a terminal state (the record is closed history) or a cancel sink. Every other
// state — the entry state, a performing step, or a resume park (blocked) — may
// be amended, which is what lets a story parked over a wrong definition be
// corrected and resumed in place.
//
// Fail-closed when the governing route cannot be resolved, matching the freeze
// guard: a broken deployment must not become a way past the freeze.
func refuseUnamendableState(ctx context.Context, current workitem.Item) error {
	spec, ok := storyGoverningSpec(ctx, current)
	if !ok {
		return fmt.Errorf(
			"satelle: refusing to amend %s — cannot resolve the story's governing route, so whether this state may be amended is unknown (fix the workflow config and retry)",
			current.ID)
	}
	status := current.Status
	if spec.IsTerminalState(status) {
		return fmt.Errorf(
			"satelle: refusing to amend %s — %q is a terminal state and its record is closed history; raise a new story carrying supersedes:%s",
			current.ID, status, current.ID)
	}
	// A cancel sink is a park by shape but never resumes, so it is as closed as
	// terminal. Classification stays wfdot's, not a status-name list here.
	if spec.IsParkState(status) && !spec.IsResumePark(status) {
		return fmt.Errorf(
			"satelle: refusing to amend %s — %q is a cancel sink, not a resumable state; raise a new story carrying supersedes:%s",
			current.ID, status, current.ID)
	}
	return nil
}

// refuseOffRouteCategory refuses a category amendment that would leave the story
// on a route which never declares its current status — the stranded condition
// `satelle story restamp` guards the same way. Skipped when the category is not
// being amended, or when the amended route cannot be resolved at all (the
// state-classification guard above has already refused that case).
func refuseOffRouteCategory(ctx context.Context, current workitem.Item, category *string) error {
	if category == nil || *category == current.Category {
		return nil
	}
	amended := current
	amended.Category = *category
	spec, ok := storyGoverningSpec(ctx, amended)
	if !ok {
		return fmt.Errorf(
			"satelle: refusing to amend %s — category %q resolves no governing route", current.ID, *category)
	}
	if !specHasState(spec, current.Status) {
		return fmt.Errorf(
			"satelle: refusing to amend %s to category %q — that route declares no %q state, so the story would be stranded off-route; amend the category from a state both routes share, or cancel and re-raise",
			current.ID, *category, current.Status)
	}
	return nil
}

// storyGoverningSpec resolves the route governing item through the same pure
// path the definition freeze uses, so amendability holds with no agent CLI
// configured. ok is false whenever the route cannot be resolved.
func storyGoverningSpec(ctx context.Context, item workitem.Item) (wfdot.Spec, bool) {
	idx, err := requireDocIndex()
	if err != nil {
		return wfdot.Spec{}, false
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return wfdot.Spec{}, false
	}
	spec, _, _, serr := wfgovern.SpecFor(wfs, item)
	if serr != nil {
		return wfdot.Spec{}, false
	}
	return spec, true
}

// specHasState reports whether the route declares a state by that name.
func specHasState(spec wfdot.Spec, name string) bool {
	for _, st := range spec.States {
		if st.Name == name {
			return true
		}
	}
	return false
}

// amendFields renders the proposed change as the ordered before/after set the
// gate judges and the ledger records. Only fields that actually change appear.
func amendFields(current workitem.Item, req amendReq) []AmendField {
	var out []AmendField
	add := func(name, old string, proposed *string) {
		if proposed != nil && *proposed != old {
			out = append(out, AmendField{Field: name, Old: old, New: *proposed})
		}
	}
	add("title", current.Title, req.Title)
	add("body", current.Body, req.Body)
	add("acceptance_criteria", current.AcceptanceCriteria, req.AcceptanceCriteria)
	add("category", current.Category, req.Category)
	return out
}

// amendLedgerBody is the one-line summary shown in a ledger listing. The full
// before/after text rides in the payload — a truncated old value would defeat
// the audit trail the amendment exists to leave.
func amendLedgerBody(fields []AmendField, reason, skill string) string {
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Field)
	}
	body := fmt.Sprintf("definition amended: [%s] — %s", strings.Join(names, ", "), reason)
	if skill != "" {
		body += " (accepted by " + skill + ")"
	}
	return body
}

// amendPayload carries the full before/after of every changed field, the reason,
// and the accepting reviewer — the record `satelle ledger list` renders and a
// later reviewer reads to see what the definition used to say.
func amendPayload(fields []AmendField, reason string, dec GateDecision) json.RawMessage {
	b, err := json.Marshal(map[string]any{
		"reason": reason,
		"skill":  dec.Skill,
		"notes":  dec.Notes,
		"fields": fields,
	})
	if err != nil {
		return nil
	}
	return b
}
