package verb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// runEditCreateGate judges a definition edit made while the story is still in
// its workflow's entry state, with the same create_review gate that judged the
// story when it was raised (sty_a8885220): an edit to the acceptance criteria,
// title, body or category is a new definition, not a note. The draft is the
// story as it would stand after the edit.
//
// It runs before any write, so a refusal leaves the stored definition exactly
// as it was. Edits at a later editable state are not gated here: the readiness
// reviewers and the definition check judge those. A repo with no create reviewer
// wired, or a workflow that declares no create_review hook (dec.Gated false),
// leaves the edit ungated — create-gating is opt-in, and so is this.
func runEditCreateGate(ctx context.Context, current workitem.Item, edits []AmendField, now time.Time) error {
	if len(edits) == 0 || createReviewer == nil || current.Kind != workitem.KindStory {
		return nil
	}
	entry, ok := storyEntryState(ctx, current)
	if !ok {
		// storyDefinitionEditable already failed closed on an unresolvable route
		// before the edit got this far; nothing here widens that.
		return nil
	}
	if current.Status != entry {
		return nil
	}
	draft := CreateDraft{
		Kind:               string(current.Kind),
		Title:              current.Title,
		Body:               current.Body,
		AcceptanceCriteria: current.AcceptanceCriteria,
		Priority:           current.Priority,
		Category:           current.Category,
		Tags:               current.Tags,
	}
	for _, f := range edits {
		switch f.Field {
		case "title":
			draft.Title = f.New
		case "body":
			draft.Body = f.New
		case "acceptance_criteria":
			draft.AcceptanceCriteria = f.New
		case "category":
			draft.Category = f.New
		}
	}
	dec, err := createReviewer.ReviewCreate(ctx, draft)
	if err != nil {
		return err
	}
	if !dec.Gated {
		return nil
	}
	// The run is recorded on the story whether it accepts or rejects: a rejection
	// changes nothing on the story, but the reviewer was still paid for.
	recordLifecycleReview(ctx, current.ID, "create_review", current.Status, current.Status, dec, now)
	if !dec.Accept {
		return fmt.Errorf("edit of %s [%s] rejected by %s: %s", current.ID, editedFieldNames(edits), dec.Skill, dec.Notes)
	}
	RecordVerdict(fmt.Sprintf("accepted edit of %s by %s: decision=accept notes=%s", current.ID, dec.Skill, dec.Notes))
	return nil
}

// editedFieldNames lists the changed field names for a refusal line.
func editedFieldNames(edits []AmendField) string {
	names := make([]string, 0, len(edits))
	for _, f := range edits {
		names = append(names, f.Field)
	}
	return strings.Join(names, ", ")
}
