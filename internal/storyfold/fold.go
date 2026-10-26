// Package storyfold derives a story's current state from its ledger chain
// (sty_d84be8bc). The chain is the source of truth for a story's status,
// definition (title, body, acceptance criteria, category, tags) and park origin;
// the work_items row is a cache of the fold, rebuilt from it and checked against
// it before anything is published.
//
// Fold is pure: it reads entries and a park classifier and touches no store.
// The payload shapes it reads are written by the same package (CreatedPayloadOf,
// SnapshotPayloadOf, TagsChange), so a field has one writer-side shape and one
// reader. Seed (seed.go) gives a chain that predates the fold its starting point;
// Replay (replay.go) checks a chain's transitions against the workflow versions
// it recorded.
package storyfold

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The folded fields, as State.Defined and a Drift name them.
const (
	FieldStatus     = "status"
	FieldTitle      = "title"
	FieldBody       = "body"
	FieldAcceptance = "acceptance_criteria"
	FieldCategory   = "category"
	FieldTags       = "tags"
	FieldParkOrigin = "park_origin"
)

// Fields lists the folded fields in the order a report names them.
var Fields = []string{FieldStatus, FieldTitle, FieldBody, FieldAcceptance, FieldCategory, FieldTags, FieldParkOrigin}

// State is what folding a story's chain yields. Defined records which fields some
// event set: a field no event set is not "empty", it is unknown, and a reader
// keeps the row's value for it rather than blanking it.
type State struct {
	Status             string
	Title              string
	Body               string
	AcceptanceCriteria string
	Category           string
	Tags               []string
	ParkOrigin         string
	// Head is the id of the last event folded.
	Head string
	// StatusEvent is the id of the event that last set the status.
	StatusEvent string
	// StaleTransitions are the status_transitions the fold skipped because their
	// from is not the status the chain had reached at their place. Such an event
	// is a chain break: it never becomes the latest status.
	StaleTransitions []ReplayBreak
	Defined          map[string]bool
}

// ParkFunc says whether a status is a park state of the story's governing route.
// A nil ParkFunc leaves ParkOrigin untouched by transitions.
type ParkFunc func(status string) bool

// CreatedPayload is the payload of a story's story_created event: the whole
// definition and the status the create committed, beside the optional source.
type CreatedPayload struct {
	Source             string   `json:"source,omitempty"`
	Title              string   `json:"title"`
	Body               string   `json:"body"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Category           string   `json:"category"`
	Tags               []string `json:"tags"`
	Status             string   `json:"status"`
	// ParkOrigin is where a story created parked came from; empty for the usual
	// create, and recorded so that the fold of every new story defines it.
	ParkOrigin string `json:"park_origin"`
}

// CreatedPayloadOf renders it as a story_created payload.
func CreatedPayloadOf(it workitem.Item, source string) json.RawMessage {
	tags := it.Tags
	if tags == nil {
		tags = []string{}
	}
	b, _ := json.Marshal(CreatedPayload{
		Source: source, Title: it.Title, Body: it.Body, AcceptanceCriteria: it.AcceptanceCriteria,
		Category: it.Category, Tags: tags, Status: it.Status, ParkOrigin: it.ParkOrigin,
	})
	return b
}

// SnapshotPayload is the payload of a definition_snapshot event: the row of a
// story whose chain predates the fold. The snapshot is the newest event of such a
// chain, so the fold applies what it carries as the state at that point, over
// whatever the older events said. It carries the status unless the seed was told
// the chain's own transitions decide it (see StatusSource).
type SnapshotPayload struct {
	Title              string   `json:"title"`
	Body               string   `json:"body"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Category           string   `json:"category"`
	Tags               []string `json:"tags"`
	Status             string   `json:"status,omitempty"`
	ParkOrigin         string   `json:"park_origin"`
}

// SnapshotPayloadOf renders the snapshot of it; withStatus says whether the
// snapshot carries the row's status.
func SnapshotPayloadOf(it workitem.Item, withStatus bool) json.RawMessage {
	tags := it.Tags
	if tags == nil {
		tags = []string{}
	}
	p := SnapshotPayload{
		Title: it.Title, Body: it.Body, AcceptanceCriteria: it.AcceptanceCriteria,
		Category: it.Category, Tags: tags, ParkOrigin: it.ParkOrigin,
	}
	if withStatus {
		p.Status = it.Status
	}
	b, _ := json.Marshal(p)
	return b
}

// TagsChange is the tags a story_updated event names: the set before and after.
type TagsChange struct {
	Before []string `json:"before"`
	After  []string `json:"after"`
}

// FieldChange is one definition field a story_updated, definition_edited or
// definition_amended event names, before and after.
type FieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// Fold folds a story's chain. Entries come in insertion order; the fold follows
// the chain's base links when they form one line and the given order otherwise
// (a fork is reported, never merged by guessing). The ledger's own chain breaks
// are returned unchanged. park classifies park states for ParkOrigin; nil leaves
// it to the events that carry one.
func Fold(entries []ledger.Entry, park ParkFunc) (State, []ledger.ChainBreak) {
	st := newState()
	for _, e := range chainOrder(entries) {
		st.apply(e, park)
	}
	return st, ledger.VerifyChain(entries)
}

func newState() State { return State{Tags: []string{}, Defined: map[string]bool{}} }

// chainOrder returns entries in chain order when their base links form one line
// from a single root, and as given otherwise.
func chainOrder(entries []ledger.Entry) []ledger.Entry {
	if len(entries) < 2 {
		return entries
	}
	ids := make(map[string]bool, len(entries))
	for _, e := range entries {
		ids[e.ID] = true
	}
	byBase := make(map[string][]int, len(entries))
	root := -1
	for i, e := range entries {
		if e.BaseID == "" || !ids[e.BaseID] {
			if root != -1 {
				return entries
			}
			root = i
			continue
		}
		byBase[e.BaseID] = append(byBase[e.BaseID], i)
	}
	if root == -1 {
		return entries
	}
	out := make([]ledger.Entry, 0, len(entries))
	for cur := root; ; {
		out = append(out, entries[cur])
		next := byBase[entries[cur].ID]
		if len(next) == 0 {
			break
		}
		if len(next) > 1 || len(out) >= len(entries) {
			return entries
		}
		cur = next[0]
	}
	if len(out) != len(entries) {
		return entries
	}
	return out
}

// apply folds one event into the state.
func (st *State) apply(e ledger.Entry, park ParkFunc) {
	st.Head = e.ID
	switch e.Kind {
	case ledger.KindStoryCreated, ledger.KindDefinitionSnapshot:
		if st.applyFields(e.Payload) {
			st.StatusEvent = e.ID
		}
	case ledger.KindDefinitionEdited:
		var c FieldChange
		if json.Unmarshal(e.Payload, &c) == nil {
			st.setField(c.Field, c.New)
		}
	case ledger.KindDefinitionAmended:
		var p struct {
			Fields []FieldChange `json:"fields"`
		}
		if json.Unmarshal(e.Payload, &p) == nil {
			for _, c := range p.Fields {
				st.setField(c.Field, c.New)
			}
		}
	case ledger.KindStoryUpdated:
		var p struct {
			Fields    []FieldChange `json:"fields"`
			Tags      *TagsChange   `json:"tags"`
			TagsAdded []string      `json:"tags_added"`
		}
		if json.Unmarshal(e.Payload, &p) != nil {
			return
		}
		for _, c := range p.Fields {
			st.setField(c.Field, c.New)
		}
		if p.Tags != nil {
			st.setTags(p.Tags.After)
		}
		if len(p.TagsAdded) > 0 {
			st.setTags(appendMissing(st.Tags, p.TagsAdded))
		}
	case ledger.KindStatusTransition:
		st.transition(e, park)
	}
}

// applyFields sets every field a story_created or definition_snapshot payload
// carries; a field the payload omits is left as it is. It reports whether the
// payload carried a status.
func (st *State) applyFields(payload json.RawMessage) bool {
	var p struct {
		Title      *string   `json:"title"`
		Body       *string   `json:"body"`
		Acceptance *string   `json:"acceptance_criteria"`
		Category   *string   `json:"category"`
		Tags       *[]string `json:"tags"`
		Status     *string   `json:"status"`
		ParkOrigin *string   `json:"park_origin"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return false
	}
	set := func(field string, v *string) {
		if v != nil {
			st.setField(field, *v)
		}
	}
	set(FieldTitle, p.Title)
	set(FieldBody, p.Body)
	set(FieldAcceptance, p.Acceptance)
	set(FieldCategory, p.Category)
	set(FieldStatus, p.Status)
	set(FieldParkOrigin, p.ParkOrigin)
	if p.Tags != nil {
		st.setTags(*p.Tags)
	}
	return p.Status != nil
}

// setField sets one named field; a name the fold does not track is ignored.
func (st *State) setField(field, v string) {
	switch field {
	case FieldStatus:
		st.Status = v
	case FieldTitle:
		st.Title = v
	case FieldBody:
		st.Body = v
	case FieldAcceptance:
		st.AcceptanceCriteria = v
	case FieldCategory:
		st.Category = v
	case FieldParkOrigin:
		st.ParkOrigin = v
	default:
		return
	}
	st.Defined[field] = true
}

func (st *State) setTags(tags []string) {
	if tags == nil {
		tags = []string{}
	}
	st.Tags = append([]string{}, tags...)
	st.Defined[FieldTags] = true
}

// transition folds a status_transition: the destination becomes the status, and a
// move into a park state records where it came from, a move out of one clears it.
// A transition that names a from other than the status the fold has reached is
// stale — an event made before the chain moved on, linked after it — and changes
// nothing: it is recorded in StaleTransitions instead.
func (st *State) transition(e ledger.Entry, park ParkFunc) {
	to := TransitionTo(e)
	if to == "" {
		return
	}
	from := TransitionFrom(e)
	if from == "" {
		from = st.Status
	}
	if st.Status != "" && from != st.Status {
		st.StaleTransitions = append(st.StaleTransitions, ReplayBreak{
			StoryID: e.StoryID, EventID: e.ID, From: from, To: to, WorkflowHash: e.WorkflowHash,
			Reason: ReplayStaleFrom,
			Detail: fmt.Sprintf("the story was %s at this place in the chain, so a transition from %s does not apply", st.Status, from),
		})
		return
	}
	if park != nil {
		switch {
		case park(to) && !park(from):
			st.setField(FieldParkOrigin, from)
		case park(from) && !park(to):
			st.setField(FieldParkOrigin, "")
		}
	}
	st.setField(FieldStatus, to)
	st.StatusEvent = e.ID
}

func appendMissing(have, add []string) []string {
	seen := make(map[string]bool, len(have))
	for _, t := range have {
		seen[t] = true
	}
	out := append([]string{}, have...)
	for _, t := range add {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// TransitionTo reads the destination status from a status_transition payload.
// Falls back to the "from → to" body only when the payload has no `to`.
func TransitionTo(e ledger.Entry) string {
	var p struct {
		To string `json:"to"`
	}
	if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &p) == nil {
		if to := strings.TrimSpace(p.To); to != "" {
			return to
		}
	}
	if i := strings.LastIndex(e.Body, " → "); i >= 0 {
		return strings.TrimSpace(e.Body[i+len(" → "):])
	}
	return ""
}

// TransitionFrom reads the source status from a status_transition payload.
// Falls back to the "from → to" body only when the payload has no `from`.
func TransitionFrom(e ledger.Entry) string {
	var p struct {
		From string `json:"from"`
	}
	if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &p) == nil {
		if from := strings.TrimSpace(p.From); from != "" {
			return from
		}
	}
	if i := strings.Index(e.Body, " → "); i >= 0 {
		return strings.TrimSpace(e.Body[:i])
	}
	return ""
}

// Drift is one folded field on which a story's cached row disagrees with its
// chain: the row's value, and the chain's, which is the one that is right.
type Drift struct {
	ID    string `json:"id"`
	Field string `json:"field"`
	Row   string `json:"row"`
	Chain string `json:"chain"`
}

// Compare lists the fields on which row disagrees with st. A field the fold does
// not define is not compared: no event says what it should be.
func Compare(row workitem.Item, st State) []Drift {
	var out []Drift
	add := func(field, rowV, chainV string) {
		if st.Defined[field] && rowV != chainV {
			out = append(out, Drift{ID: row.ID, Field: field, Row: rowV, Chain: chainV})
		}
	}
	add(FieldStatus, row.Status, st.Status)
	add(FieldTitle, row.Title, st.Title)
	add(FieldBody, row.Body, st.Body)
	add(FieldAcceptance, row.AcceptanceCriteria, st.AcceptanceCriteria)
	add(FieldCategory, row.Category, st.Category)
	add(FieldTags, strings.Join(row.Tags, ","), strings.Join(st.Tags, ","))
	add(FieldParkOrigin, row.ParkOrigin, st.ParkOrigin)
	return out
}

// Overlay returns row with every field st defines replaced by the chain's value.
// Fields the chain does not carry (priority, parent, assignee, timestamps) and
// fields no event defined stay the row's.
func Overlay(row workitem.Item, st State) workitem.Item {
	if st.Defined[FieldStatus] {
		row.Status = st.Status
	}
	if st.Defined[FieldTitle] {
		row.Title = st.Title
	}
	if st.Defined[FieldBody] {
		row.Body = st.Body
	}
	if st.Defined[FieldAcceptance] {
		row.AcceptanceCriteria = st.AcceptanceCriteria
	}
	if st.Defined[FieldCategory] {
		row.Category = st.Category
	}
	if st.Defined[FieldTags] {
		row.Tags = append([]string{}, st.Tags...)
	}
	if st.Defined[FieldParkOrigin] {
		row.ParkOrigin = st.ParkOrigin
	}
	return row
}
