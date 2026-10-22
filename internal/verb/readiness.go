package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfdot"
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
	st, found := destinationStep(ctx, current, to)
	return found && st.Propose
}

// destinationStep resolves the step the story is moving INTO over the declared
// current→to edge. Not found when the route cannot be resolved or declares no
// such edge.
func destinationStep(ctx context.Context, current workitem.Item, to string) (wfdot.State, bool) {
	spec, _, _, ok := governingSpec(ctx, current)
	if !ok || !spec.HasEdge(current.Status, to) {
		return wfdot.State{}, false
	}
	return spec.StateNamed(to)
}

// planCriteriaActor is the actor on the definition_edited row written when an
// accepted document's criteria are applied on entry, so the trail tells that
// edit apart from one a person or the driving session made.
const planCriteriaActor = "accepted-plan"

// applyOut is where the notice that a plan's criteria were not applied is
// printed. Stderr, like the other transition-time notices, so `--json` stdout
// stays parseable.
var applyOut io.Writer = os.Stderr

// SetApplyOutput redirects the criteria-apply notice; nil restores stderr (tests).
func SetApplyOutput(w io.Writer) {
	if w == nil {
		w = os.Stderr
	}
	applyOut = w
}

// criteriaLastAmended reports whether the latest ledger row to change a story's
// acceptance criteria is an accepted amendment. Rows are read oldest first. A
// later definition_edited row (a re-presented readiness applying new plan text)
// puts the plan back in charge, so only the LAST change decides.
func criteriaLastAmended(entries []ledger.Entry) bool {
	amended := false
	for _, e := range entries {
		switch e.Kind {
		case ledger.KindDefinitionEdited:
			var p definitionEditPayload
			if json.Unmarshal(e.Payload, &p) == nil && p.Field == "acceptance_criteria" {
				amended = false
			}
		case ledger.KindDefinitionAmended:
			var p struct {
				Fields []AmendField `json:"fields"`
			}
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			for _, f := range p.Fields {
				if f.Field == "acceptance_criteria" {
					amended = true
				}
			}
		}
	}
	return amended
}

// applyPlannedCriteria applies, on entry to a step that declares
// `apply_criteria`, the acceptance criteria authored under the declared heading
// in the declared story document (sty_4d9df9a0). It returns the updated item
// when it writes and `current` unchanged otherwise, so the caller can rebind it
// before any performer or reviewer reads the story. The document, the heading
// and the step are all route data. A missing document, a missing or empty
// section, or text the story already carries writes nothing; an unreadable
// document refuses the transition rather than skipping the apply silently.
//
// An accepted `story amend` outranks the plan (sty_89804a0e): after readiness the
// amend gate is the authority over the definition, so when the criteria were last
// changed by an amendment the plan's differing text is not applied. The skip
// writes no row and prints a notice naming the drift.
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
	criteria := agentartifact.Section(string(data), st.ApplyCriteriaHeading)
	if criteria == "" || strings.Join(strings.Fields(criteria), " ") == strings.Join(strings.Fields(current.AcceptanceCriteria), " ") {
		return current, nil
	}
	if criteriaLastAmended(storyLedgerEntries(ctx, current.ID)) {
		fmt.Fprintf(applyOut, "satelle: %s: criteria not applied from the %q document (section %q) — the plan's criteria differ from the amended criteria (last changed by an accepted amend); the story keeps the amended text\n",
			current.ID, st.ApplyCriteriaDoc, st.ApplyCriteriaHeading)
		return current, nil
	}
	payload, err := json.Marshal(definitionEditPayload{
		Field: "acceptance_criteria", Old: current.AcceptanceCriteria, New: criteria,
		Actor: planCriteriaActor, Source: st.ApplyCriteriaDoc + "#" + st.ApplyCriteriaHeading,
	})
	if err != nil {
		return current, err
	}
	updated, err := applyToRecord(ctx, current, workitem.UpdateInput{AcceptanceCriteria: &criteria}, recordedWrite{
		Kind: ledger.KindDefinitionEdited, Actor: planCriteriaActor,
		Body:    fmt.Sprintf("definition applied from the accepted plan document %q (section %q)", st.ApplyCriteriaDoc, st.ApplyCriteriaHeading),
		Payload: payload,
	}, now)
	if err != nil {
		return current, fmt.Errorf("verb: transition %s→%s: applying the criteria from the %q document: %w", current.Status, to, st.ApplyCriteriaDoc, err)
	}
	return updated, nil
}

// recordedWrite is the one ledger row an applied write leaves on the record.
type recordedWrite struct {
	Kind    string
	Actor   string
	Body    string
	Payload json.RawMessage
}

// applyToRecord is the one place a route-declared apply writes to a story: a
// compare-and-set update against cur's status, then the single attributed row
// that records the write, then the ready-mark sync. The accepted plan's criteria
// (apply_criteria) and the changes a performer reports (apply_changes) both go
// through it, so they share one write, one attribution and one sync.
func applyToRecord(ctx context.Context, cur workitem.Item, in workitem.UpdateInput, row recordedWrite, now time.Time) (workitem.Item, error) {
	updated, err := writeToRecord(ctx, cur, in, now)
	if err != nil {
		return cur, err
	}
	recordApplied(ctx, cur.ID, row, now)
	return updated, nil
}

// writeToRecord is the store half of applyToRecord: the compare-and-set update
// against cur's status, with no row and no sync. A batch that must be
// all-or-nothing (the changes a performer reports) writes every update with it,
// and only once all have landed records them with recordApplied.
func writeToRecord(ctx context.Context, cur workitem.Item, in workitem.UpdateInput, now time.Time) (workitem.Item, error) {
	store, err := requireWorkItem()
	if err != nil {
		return cur, err
	}
	in.ExpectStatus = &cur.Status
	return store.Update(ctx, cur.ID, in, now)
}

// recordApplied is the record half of applyToRecord: the single attributed row
// for a write that landed, then the ready-mark sync.
func recordApplied(ctx context.Context, id string, row recordedWrite, now time.Time) {
	appendLedgerEntry(ctx, id, row.Kind, row.Actor, row.Body, row.Payload, now)
	syncReadyMark(ctx, id)
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
	return &RejectBudgetSpent{
		From: current.Status, To: to, Rounds: rounds.Rounds, Budget: st.RejectBudget, Objection: rounds.ObjectionLine(),
	}
}

// RejectBudgetSpent is the refusal for an edge whose destination step's
// reject_budget rejected rounds are spent. It is the blocked-reason format, and
// typed so a caller that parks on it (the headless drive) reads the objection.
type RejectBudgetSpent struct {
	From, To  string
	Rounds    int
	Budget    int
	Objection string
}

func (e *RejectBudgetSpent) Error() string {
	return fmt.Sprintf(
		"transition %s→%s refused: edge rejected %d times (reject budget %d) — last objection: %q — decision needed: park the story to blocked with this reason and message the developer; a resume restarts the budget",
		e.From, e.To, e.Rounds, e.Budget, e.Objection)
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
	EditorIdentity
}

// EditorIdentity is the session and executable behind a definition edit — the
// part of "who" the person actor does not say (sty_a8885220). Every field is
// optional so a row written before this identity was recorded still parses; a
// row written after it carries either the pair or IdentityUnavailable, never a
// silent empty.
type EditorIdentity struct {
	SessionID  string `json:"session_id,omitempty"`
	Executable string `json:"executable,omitempty"`
	// IdentityUnavailable names, adapter by adapter, what could not be resolved.
	IdentityUnavailable string `json:"identity_unavailable,omitempty"`
}

// resolveEditorIdentity names the session making this edit and the harness it
// runs under. What cannot be resolved is stated, not left empty.
func resolveEditorIdentity() EditorIdentity {
	sid := config.ResolveSession()
	exe := agentcli.HarnessUnknown
	if sid != "" {
		exe = driverSessionHarness(sid)
	} else if h, ok := agentcli.InLoopHarnessFromEnv(os.Environ()); ok {
		exe = h
	}
	id := EditorIdentity{SessionID: sid}
	var gaps []string
	if sid == "" {
		gaps = append(gaps, "session: no session id in SATELLE_SESSION, the harness environment or a published session")
	}
	if exe == agentcli.HarnessUnknown {
		gaps = append(gaps, fmt.Sprintf("executable: no adapter (%s, %s, %s, %s) recognised the session",
			agentcli.HarnessClaude, agentcli.HarnessGrok, agentcli.HarnessPi, agentcli.HarnessCursor))
	} else {
		id.Executable = exe
	}
	id.IdentityUnavailable = strings.Join(gaps, "; ")
	return id
}

// storyUpdatedPayload is a story_updated row's payload: the editor and, for a
// definition edit, the before and after of each changed field. A row written
// before this payload existed has none and decodes to the zero value.
type storyUpdatedPayload struct {
	EditorIdentity
	Fields []AmendField `json:"fields,omitempty"`
}

// storyUpdatedPayloadFor renders the story_updated payload for one committed
// story-set. It is always present, so even a tag or priority edit names its editor.
func storyUpdatedPayloadFor(id EditorIdentity, edits []AmendField) json.RawMessage {
	b, err := json.Marshal(storyUpdatedPayload{EditorIdentity: id, Fields: edits})
	if err != nil {
		return nil
	}
	return b
}

// recordDefinitionEdits appends one definition_edited row per field a committed
// story-set changed while the route still leaves the definition editable. The
// actor is the satelle user (account principal, or the git user local-only),
// falling back to the executor role the other definition rows use. Best-effort
// like every other trail row: a failed append never reverts the edit that
// already committed.
func recordDefinitionEdits(ctx context.Context, it workitem.Item, edits []AmendField, id EditorIdentity, now time.Time) {
	if len(edits) == 0 {
		return
	}
	actor := resolveActor()
	if actor == "" {
		actor = "executor"
	}
	for _, f := range edits {
		payload, err := json.Marshal(definitionEditPayload{Field: f.Field, Old: f.Old, New: f.New, Actor: actor, EditorIdentity: id})
		if err != nil {
			continue
		}
		appendLedgerEntry(ctx, it.ID, ledger.KindDefinitionEdited, actor,
			fmt.Sprintf("definition edited while editable: %s (before and after are on the row)", f.Field), payload, now)
	}
	// An edit after an accepted readiness round voids it (sty_a32bd6a9).
	syncReadyMark(ctx, it.ID)
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
	EditorIdentity
}

// DefinitionEditsFrom enumerates the definition edits among a story's ledger
// rows, oldest first, so a replay can rebuild what a gate injected from rows alone.
func DefinitionEditsFrom(entries []ledger.Entry) []DefinitionEdit { return editsFrom(entries) }

func editsFrom(entries []ledger.Entry) []DefinitionEdit {
	var out []DefinitionEdit
	for _, e := range entries {
		var p definitionEditPayload
		if e.Kind != ledger.KindDefinitionEdited || json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		out = append(out, DefinitionEdit{
			Field: p.Field, Old: p.Old, New: p.New, Actor: p.Actor, Source: p.Source,
			EditorIdentity: p.EditorIdentity,
			At:             e.CreatedAt.UTC().Format(time.RFC3339),
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
