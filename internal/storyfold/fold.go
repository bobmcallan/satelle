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
	"strconv"
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
	// FieldArchived is the record disposition (sty_28665836): "true" or "false".
	// Only a story_updated field change sets it, so a chain that never archived
	// the story leaves it undefined and the row's value stands; it is therefore
	// not in Fields, which names what every chain defines.
	FieldArchived = "archived"
)

// Fields lists the fields every story's chain defines, in the order a report
// names them.
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
	Archived           bool
	// Head is the id of the last event folded.
	Head string
	// StatusEvent is the id of the event that last set the status.
	StatusEvent string
	// StaleTransitions are the status_transitions whose from is not the status the
	// chain had reached at their place. Each is a chain break, always reported;
	// ReplayBreak.Skipped says whether the fold ignored it (the story had already
	// ended) or applied it out of order.
	StaleTransitions []ReplayBreak
	Defined          map[string]bool
}

// Route is what the fold needs of the story's governing route. A zero Route is no
// route resolved.
type Route struct {
	// Park says whether a status is a park state. Nil leaves ParkOrigin untouched
	// by transitions.
	Park func(status string) bool
	// Final says whether a status ends the story for good (terminal, or a park
	// that does not resume). Nil means nothing is known to be final, so no stale
	// transition is skipped.
	Final func(status string) bool
}

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
// are returned unchanged. route classifies park and final states; a zero Route
// leaves park origin to the events that carry one and skips no stale transition.
func Fold(entries []ledger.Entry, route Route) (State, []ledger.ChainBreak) {
	st := newState()
	for _, e := range chainOrder(entries) {
		st.apply(e, route)
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
func (st *State) apply(e ledger.Entry, route Route) {
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
		st.transition(e, route)
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
	case FieldArchived:
		st.Archived = v == "true"
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
// A transition that names a from other than the status the fold has reached is a
// chain break — an event made before the chain moved on, or linked out of time
// order — and is always recorded in StaleTransitions. It is skipped only when the
// status the fold has reached is final on the route: a story that ended is never
// moved by a late event. Anywhere else it applies, so a legacy chain linked once
// in insertion order still folds to the status its transitions reached.
func (st *State) transition(e ledger.Entry, route Route) {
	to := TransitionTo(e)
	if to == "" {
		return
	}
	from := TransitionFrom(e)
	if from == "" {
		from = st.Status
	}
	if st.Status != "" && from != st.Status {
		skip := route.Final != nil && route.Final(st.Status)
		detail := fmt.Sprintf("the story was %s at this place in the chain, so a transition from %s applied out of order", st.Status, from)
		if skip {
			detail = fmt.Sprintf("the story was %s at this place in the chain, so a transition from %s does not apply", st.Status, from)
		}
		st.StaleTransitions = append(st.StaleTransitions, ReplayBreak{
			StoryID: e.StoryID, EventID: e.ID, From: from, To: to, WorkflowHash: e.WorkflowHash,
			Reason: ReplayStaleFrom, Skipped: skip, Detail: detail,
		})
		if skip {
			return
		}
	}
	if park := route.Park; park != nil {
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
	add(FieldArchived, strconv.FormatBool(row.Archived), strconv.FormatBool(st.Archived))
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
	if st.Defined[FieldArchived] {
		row.Archived = st.Archived
	}
	return row
}
