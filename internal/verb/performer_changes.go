package verb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Changes a performer reports (sty_af0b1761). A read-only readiness performer
// cannot run `satelle story set` or `satelle story create`, so it reports the
// configuration it wants as a `changes` list in its final response and satelle
// applies it on the record — the way the driving session applies the planner's
// criteria. Which operations and which tag prefixes are allowed is the route
// step's `apply_changes` declaration; the limits below are the mechanism that
// holds whatever the declaration says:
//
//   - a change targets only the dispatched container or one of its members, as
//     epicset.Members defines them;
//   - tags are only added, never removed, and only under a declared prefix;
//   - no title, body, acceptance criteria or category of an existing story is
//     changed;
//   - a created child names no other story and repeats no member's title;
//   - the whole batch is checked before anything is written, so a violation
//     applies nothing.

// performerSource names the step whose performer reported a change; it rides on
// the one row that records each applied change.
func performerSource(stepName string) string { return "performer:" + stepName }

// tagWrite is one add-tags change that will write.
type tagWrite struct {
	Target string
	Tags   []string
}

// changePlan is a checked batch: the children to create, then the tags to add.
type changePlan struct {
	Children []createReq
	Tags     []tagWrite
}

// refuseChange names the offending change and the limit it broke.
func refuseChange(i int, c agentartifact.Change, limit string) error {
	what := c.Op
	if c.Story != "" {
		what += " on " + c.Story
	}
	return fmt.Errorf("changes[%d] (%s): %s", i, what, limit)
}

// hasDeclaredPrefix reports whether tag starts with one of prefixes.
func hasDeclaredPrefix(tag string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(tag, p) {
			return true
		}
	}
	return false
}

// checkPerformerTags refuses a reported tag outside the declared prefixes.
func checkPerformerTags(tags, prefixes []string) error {
	for _, t := range tags {
		if !hasDeclaredPrefix(t, prefixes) {
			return fmt.Errorf("tag %q has no declared prefix (apply_changes allows: %s)", t, strings.Join(prefixes, ", "))
		}
	}
	return nil
}

// planPerformerChanges checks the whole batch against the step's declaration and
// the limits above and returns what to write. Nothing is written here.
func planPerformerChanges(ctx context.Context, store dependsOnStore, container workitem.Item, ops, prefixes []string, changes []agentartifact.Change) (changePlan, error) {
	var plan changePlan
	members, err := epicset.Members(ctx, store, container)
	if err != nil {
		return plan, fmt.Errorf("cannot resolve the members of %s: %w", container.ID, err)
	}
	// state is each reachable story as the batch leaves it, so two changes on one
	// story see each other's tags.
	state := map[string]workitem.Item{container.ID: container}
	titles := map[string]string{}
	for _, m := range members {
		state[m.ID] = m
		titles[strings.ToLower(strings.TrimSpace(m.Title))] = m.ID
	}
	themes := epicset.Themes(container)
	pending := map[string][]string{} // depends-on edges the batch gives existing stories
	touched := map[string]int{}      // story -> index of the change that added a depends-on tag
	type childCheck struct {
		index int
		tags  []string
	}
	var childChecks []childCheck

	for i, c := range changes {
		if !hasTag(ops, c.Op) {
			return plan, refuseChange(i, c, fmt.Sprintf("op %q is not declared by apply_changes (allowed: %s)", c.Op, strings.Join(ops, ", ")))
		}
		if len(c.Remove) > 0 {
			return plan, refuseChange(i, c, "removes tags: a reported change only adds tags")
		}
		switch c.Op {
		case "add-tags":
			cur, ok := state[c.Story]
			if c.Story == "" || !ok {
				return plan, refuseChange(i, c, fmt.Sprintf("targets %q, which is neither the container %s nor one of its members", c.Story, container.ID))
			}
			if c.Title != "" || c.Body != "" || c.Acceptance != "" || c.Category != "" {
				return plan, refuseChange(i, c, fmt.Sprintf("changes the definition of existing story %s: title, body, acceptance criteria and category are never changed by a reported change", c.Story))
			}
			if len(c.Tags) == 0 {
				return plan, refuseChange(i, c, "adds no tags")
			}
			if err := checkPerformerTags(c.Tags, prefixes); err != nil {
				return plan, refuseChange(i, c, err.Error())
			}
			merged := applyTagMutation(cur.Tags, c.Tags, nil)
			canon, err := canonicaliseTags(merged)
			if err != nil {
				return plan, refuseChange(i, c, err.Error())
			}
			if err := refuseUnheldReadinessMark(ctx, cur, canon); err != nil {
				return plan, refuseChange(i, c, err.Error())
			}
			cur.Tags = canon
			state[c.Story] = cur
			plan.Tags = append(plan.Tags, tagWrite{Target: c.Story, Tags: c.Tags})
			for _, t := range c.Tags {
				if strings.HasPrefix(t, DependsOnPrefix) {
					touched[c.Story] = i
					pending[c.Story] = localDependsOnTargets(canon)
				}
			}
		case "create-child":
			if c.Story != "" {
				return plan, refuseChange(i, c, fmt.Sprintf("names story %s: a created child has no target", c.Story))
			}
			title := strings.TrimSpace(c.Title)
			if title == "" {
				return plan, refuseChange(i, c, "has no title")
			}
			if dup, ok := titles[strings.ToLower(title)]; ok {
				return plan, refuseChange(i, c, fmt.Sprintf("duplicates the title of %s", dup))
			}
			titles[strings.ToLower(title)] = "an earlier change"
			if err := checkPerformerTags(c.Tags, prefixes); err != nil {
				return plan, refuseChange(i, c, err.Error())
			}
			tags := append(append([]string{}, c.Tags...), themesOfEpicParent(container, themes)...)
			canon, err := canonicaliseTags(tags)
			if err != nil {
				return plan, refuseChange(i, c, err.Error())
			}
			category, err := canonicaliseCategory(c.Category)
			if err != nil {
				return plan, refuseChange(i, c, err.Error())
			}
			plan.Children = append(plan.Children, createReq{
				Title: title, Body: c.Body, AcceptanceCriteria: c.Acceptance, Category: category,
				ParentID: container.ID, Tags: canon,
			})
			childChecks = append(childChecks, childCheck{index: i, tags: canon})
		}
	}
	// depends-on edges are judged last, over the whole batch, so a cycle closed
	// by two changes together is found.
	for id, i := range touched {
		if err := checkDependsOnPending(ctx, store, id, state[id].ParentID, state[id].Tags, pending); err != nil {
			return plan, refuseChange(i, changes[i], err.Error())
		}
	}
	for _, cc := range childChecks {
		if err := checkDependsOnPending(ctx, store, "", container.ID, cc.tags, pending); err != nil {
			return plan, refuseChange(cc.index, changes[cc.index], err.Error())
		}
	}
	return plan, nil
}

// themesOfEpicParent is the epic:<theme> tag a child of an epic-parent carries
// to be one of its members; none for any other container, whose members are
// found by parent_id.
func themesOfEpicParent(container workitem.Item, themes []string) []string {
	if epicset.IsEpicParent(container) && len(themes) > 0 {
		return themes[:1]
	}
	return nil
}

// applyPerformerChanges applies the changes a dispatched performer reported on
// container, for the step entered at to, and returns the container as it now
// stands. It is the apply_changes counterpart of applyPlannedCriteria and writes
// through the same applyToRecord. A step that declares no apply_changes refuses a
// report; a batch with any violation applies nothing and the error names the
// offending change and its limit.
//
// Each applied change leaves exactly one row that records the change itself,
// whose actor is the performer and whose payload names the source step: a
// story_updated row for added tags, a story_created row for a created child. The
// other rows creating a child writes (creator model, workflow stamp, trunk check,
// create review) record other facts and keep their own actors.
func applyPerformerChanges(ctx context.Context, container workitem.Item, to string, res DispatchResult, changes []agentartifact.Change, now time.Time) (workitem.Item, error) {
	if len(changes) == 0 {
		return container, nil
	}
	step, _ := destinationStep(ctx, container, to)
	stepName := readinessEdge{To: to, Step: step}.name()
	if len(step.ApplyChangeOps) == 0 {
		return container, fmt.Errorf("step %s declares no apply_changes; performer reported %d change(s), none applied", stepName, len(changes))
	}
	store, err := requireWorkItem()
	if err != nil {
		return container, err
	}
	plan, err := planPerformerChanges(ctx, store, container, step.ApplyChangeOps, step.ApplyChangeTags, changes)
	if err != nil {
		return container, fmt.Errorf("performer changes refused, none applied: %w", err)
	}
	actor := res.Agent
	if actor == "" {
		actor = "performer"
	}
	source := performerSource(stepName)
	// The creator of a reported child is the performer: the model it reported, an
	// explicit unknown (with the adapter's reason) when it reported none. No
	// adapter reports a performer session id, so none is recorded — never the
	// driving session's.
	origin := createOrigin{Actor: actor, Source: source}
	if agentcli.IsModelUnavailable(res.ModelResolved) {
		origin.Reason = res.ModelResolved
		if origin.Reason == "" || origin.Reason == agentcli.ModelUnavailable {
			origin.Reason = "performer reported no model"
		}
	} else {
		origin.Model = res.ModelResolved
	}
	// All-or-nothing, in three phases:
	//  1. Every child is validated and create-reviewed before anything is stored, so
	//     a child the create rules refuse leaves nothing behind — not an earlier
	//     child, not a tag.
	//  2. The store writes: the tag updates (compare-and-set), then the children. A
	//     write that fails here (a status conflict on a target, a store error) is
	//     undone: tags go back to what they were and a child already stored is
	//     archived, so the failure leaves no tag and no live child.
	//  3. Only once every write has landed are the rows recorded, so a rolled-back
	//     batch leaves no story_updated or story_created row from the performer.
	prepared := make([]*preparedCreate, 0, len(plan.Children))
	for _, child := range plan.Children {
		p, err := prepareCreate(ctx, workitem.KindStory, child, origin)
		if err != nil {
			return container, fmt.Errorf("performer changes refused, none applied: creating child %q: %w", child.Title, err)
		}
		prepared = append(prepared, p)
	}
	type tagApplied struct {
		before workitem.Item
		row    recordedWrite
	}
	var tagsApplied []tagApplied
	var created []workitem.Item
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			_, _ = store.Archive(ctx, created[i].ID, now)
		}
		for i := len(tagsApplied) - 1; i >= 0; i-- {
			b := tagsApplied[i].before
			_, _ = store.Update(ctx, b.ID, workitem.UpdateInput{Tags: &b.Tags}, now)
		}
	}
	for _, w := range plan.Tags {
		cur, err := storyStateHealed(ctx, w.Target)
		if err != nil {
			rollback()
			return container, fmt.Errorf("performer changes: reading %s, none applied: %w", w.Target, err)
		}
		runAfterTagCASGet(ctx, cur.ID, cur.Status)
		canon, err := canonicaliseTags(applyTagMutation(cur.Tags, w.Tags, nil))
		if err != nil {
			rollback()
			return container, err
		}
		added := newTags(cur.Tags, canon)
		if len(added) == 0 {
			continue
		}
		if _, err := writeToRecord(ctx, cur, workitem.UpdateInput{Tags: &canon}, now); err != nil {
			rollback()
			return container, fmt.Errorf("performer changes: adding tags to %s, none applied: %w", w.Target, err)
		}
		// The one tag-change shape (sty_d84be8bc): the tags before and after, with the
		// performer's source.
		payload := tagChange(cur.Tags, canon, source)
		tagsApplied = append(tagsApplied, tagApplied{before: cur, row: recordedWrite{
			Kind: ledger.KindStoryUpdated, Actor: actor,
			Body:    fmt.Sprintf("tags added by %s (%s): %s", actor, source, strings.Join(added, ", ")),
			Payload: payload,
		}})
	}
	for _, p := range prepared {
		it, err := p.store(ctx)
		if err != nil {
			rollback()
			return container, fmt.Errorf("performer changes: creating child %q, none applied: %w", p.req.Title, err)
		}
		created = append(created, it)
	}
	for _, w := range tagsApplied {
		recordApplied(ctx, w.before.ID, w.row, now)
	}
	for i, p := range prepared {
		if _, err := p.finish(ctx, created[i]); err != nil {
			return container, fmt.Errorf("performer changes: recording child %q: %w", p.req.Title, err)
		}
	}
	if len(prepared) > 0 {
		syncReadyMark(ctx, container.ID)
	}
	notifyChange(panelTopic(container.Kind))
	return storyState(ctx, container.ID)
}

// newTags returns the tags of after that before lacks, in order.
func newTags(before, after []string) []string {
	have := map[string]bool{}
	for _, t := range before {
		have[t] = true
	}
	var out []string
	for _, t := range after {
		if !have[t] {
			out = append(out, t)
		}
	}
	return out
}
