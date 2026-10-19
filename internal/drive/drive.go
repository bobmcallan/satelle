// Package drive walks one story along the route its repo declares, with nobody
// at a prompt: it presents each next edge through the same story-set verb a local
// drive uses, lets the step's configured performer run headless, lets the gates
// judge, relays what a rejection said back to the performer, and stops at the
// route's terminal state — or parks the story as blocked, with the question
// recorded on it, when a person has to decide.
//
// Story is the one entry point. It takes no harness: who performs a step is
// whatever the engine's performer rule (verb.PerformerResolver) and the repo's
// agents.toml bind, so the same drive serves a claude, a grok or a pi binding.
// It is called by `satelle story drive`, and is the hook a node's story command
// calls to run a story it was woken for (the remote-workflow stories
// sty_866c379b and sty_602746d8 sit on it).
//
// The drive decides no process. The route says what the steps are, the gates say
// whether an edge is accepted, the verbs say what a status move records; the
// drive only sequences them and stops where the workflow's own mechanisms stop.
package drive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Performers is what the drive asks about who performs a step, and how it runs
// the one performer the engine cannot dispatch on entry. *agentstep.Engine
// implements it.
type Performers interface {
	// StepPerformer is the engine's one rule for who performs a step.
	verb.PerformerResolver
	// ExecutorBinding is the [executor] binding as a one-shot run uses it.
	ExecutorBinding() (config.AgentBinding, bool)
	// PerformExecutorStep runs that binding headless over an executor-allocated
	// step, with notes (reviewer findings, or "") appended to its prompt.
	PerformExecutorStep(ctx context.Context, item workitem.Item, step, notes string) (verb.DispatchResult, error)
}

// Rework is the outcome of one run of a step's declared rework relay.
type Rework struct {
	// Converged is the consultant saying ready. Anything else ended the relay
	// without agreement (budget spent, or no progress).
	Converged     bool
	Outcome       string
	Rounds        int
	LastObjection string
}

// Reworker runs the story's current step's declared rework relay (a coder and a
// consulting reviewer, bounded by the step's round budget). The relay needs live
// sessions, which belong to the caller's process, so it is injected.
type Reworker func(ctx context.Context, id string) (Rework, error)

// Options carries what the drive cannot resolve itself.
type Options struct {
	Performers Performers
	// Rework runs a step's declared relay. Nil leaves a declared relay unrun: a
	// rejected edge is then relayed to the performer and re-presented.
	Rework Reworker
	// Out receives one line per move and per rejection; nil discards them.
	Out io.Writer
}

// Result is where the drive stopped.
type Result struct {
	ID     string
	Status string
	// Terminal: the story is at a terminal step of its route.
	Terminal bool
	// Parked: the story is parked (blocked) and a person must pick it up; Reason
	// is what was recorded on it.
	Parked bool
	Reason string
	// Moves are the status moves this drive made, in order.
	Moves []Move
}

// Move is one status move the drive made.
type Move struct{ From, To string }

// unbudgetedPresentations bounds the presentations of an edge whose destination
// step declares no reject_budget, so a reviewer that always rejects cannot hold
// a drive forever. A declared reject_budget is enforced by the verb and is the
// bound instead.
const unbudgetedPresentations = 10

// Story drives story id from its current status to a terminal step of its route
// or to a park, returning where it stopped. An error means the drive refused or
// could not continue, with the story left where it was: the route cannot be
// driven unattended (a step the driving session must perform), or a mechanism
// failed. A story that parked is a Result, not an error.
func Story(ctx context.Context, id string, opts Options) (Result, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Result{}, errors.New("drive: a story id is required")
	}
	if opts.Performers == nil {
		return Result{}, errors.New("drive: no performer resolver is wired")
	}
	d := &driver{id: id, opts: opts, out: opts.Out}
	if d.out == nil {
		d.out = io.Discard
	}
	return d.run(ctx)
}

type driver struct {
	id   string
	opts Options
	out  io.Writer

	route wfgovern.DerivedRoute
	spine []string
	res   Result
}

func (d *driver) run(ctx context.Context) (Result, error) {
	item, err := d.load(ctx)
	if err != nil {
		return Result{}, err
	}
	if item.Kind != workitem.KindStory {
		return Result{}, fmt.Errorf("drive: %s is not a story", d.id)
	}
	d.route, err = verb.GoverningRoute(ctx, item)
	if err != nil {
		return Result{}, fmt.Errorf("drive: %s has no route to walk: %w", d.id, err)
	}
	d.spine = d.route.Spec.SpineSteps()
	d.res.ID = d.id
	if !d.stopped(item) {
		if err := d.preflight(ctx, item); err != nil {
			return Result{}, err
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if d.stopped(item) {
			return d.finish(ctx, item), nil
		}
		next, err := d.successor(item.Status)
		if err != nil {
			return Result{}, err
		}
		item, err = d.advance(ctx, item, next)
		if err != nil {
			return Result{}, err
		}
		if d.res.Parked {
			return d.res, nil
		}
	}
}

// stopped reports whether the story is somewhere the drive does not move it from:
// a terminal step, or a park.
func (d *driver) stopped(item workitem.Item) bool {
	return d.route.Spec.IsTerminalState(item.Status) || d.route.Spec.IsParkState(item.Status)
}

// finish reports a story that is already stopped.
func (d *driver) finish(ctx context.Context, item workitem.Item) Result {
	d.res.Status = item.Status
	if d.route.Spec.IsTerminalState(item.Status) {
		d.res.Terminal = true
		return d.res
	}
	d.res.Parked = true
	d.res.Reason = d.recordedReason(ctx)
	return d.res
}

// successor is the spine step after status.
func (d *driver) successor(status string) (string, error) {
	for i, name := range d.spine {
		if name != status {
			continue
		}
		if i+1 >= len(d.spine) {
			break
		}
		next := d.spine[i+1]
		if !d.route.Spec.HasEdge(status, next) {
			return "", fmt.Errorf("drive: the route declares no edge %s → %s", status, next)
		}
		return next, nil
	}
	return "", fmt.Errorf("drive: status %q is not a step on the route's spine, or has no step after it", status)
}

// preflight refuses, before any transition, a route this drive cannot walk
// unattended: a step the driving session itself would have to perform. It asks
// the engine's performer rule about the current step and every step after it, so
// placement and in-loop bindings are respected, not re-derived here.
func (d *driver) preflight(ctx context.Context, item workitem.Item) error {
	at := -1
	for i, name := range d.spine {
		if name == item.Status {
			at = i
		}
	}
	if at < 0 {
		return fmt.Errorf("drive: status %q is not a step on the route's spine", item.Status)
	}
	executorChecked := false
	for _, step := range d.spine[at:] {
		perf, err := d.opts.Performers.StepPerformer(ctx, item, step)
		if err != nil {
			return fmt.Errorf("drive: step %q: %w", step, err)
		}
		switch perf.Kind {
		case verb.PerformerInLoop:
			return fmt.Errorf(
				"drive: step %q is allocated to agent %q, which .satelle/workflows/agents.toml [%s] binds in-loop — bind a headless CLI to drive unattended",
				step, perf.Agent, perf.Agent)
		case verb.PerformerExecutor:
			if executorChecked {
				continue
			}
			executorChecked = true
			b, ok := d.opts.Performers.ExecutorBinding()
			if !ok || config.IsInLoopCommand(b.CommandTemplate()) {
				return fmt.Errorf(
					"drive: step %q is allocated to agent=executor and .satelle/workflows/agents.toml [executor] command is in-loop — bind a headless CLI to drive unattended",
					step)
			}
		}
	}
	return nil
}

// advance presents the edge item.Status → next until it is accepted, relaying each
// rejection back to the step's performer in between, then performs the step if it
// is executor-allocated. It returns the story as it then stands; Result.Parked is
// set when the story was parked instead.
func (d *driver) advance(ctx context.Context, item workitem.Item, next string) (workitem.Item, error) {
	from := item.Status
	limit := 0
	if st, ok := d.route.Spec.StateNamed(next); ok && st.RejectBudget < 1 {
		limit = unbudgetedPresentations
	}
	for attempt := 1; ; attempt++ {
		fmt.Fprintf(d.out, "drive: %s presenting %s → %s\n", d.id, from, next)
		_, err := d.set(ctx, next)
		if err == nil {
			break
		}
		var spent *verb.RejectBudgetSpent
		var rejected *verb.GateRejection
		switch {
		case errors.As(err, &spent):
			return item, d.park(ctx, item, fmt.Sprintf(
				"the %s → %s edge was rejected %d times (reject budget %d); last objection: %s",
				from, next, spent.Rounds, spent.Budget, spent.Objection))
		case errors.As(err, &rejected):
			fmt.Fprintf(d.out, "drive: %s %s → %s rejected\n%s\n", d.id, from, next, rejected.Notes())
			if limit > 0 && attempt >= limit {
				return item, d.park(ctx, item, fmt.Sprintf(
					"the %s → %s edge was rejected %d times and its step declares no reject_budget; last objection: %s",
					from, next, attempt, rejected.Notes()))
			}
			if err := d.answerRejection(ctx, item, next, rejected.Notes()); err != nil {
				return item, err
			}
			if d.res.Parked {
				return item, nil
			}
		default:
			return item, fmt.Errorf("drive: %s → %s refused: %w", from, next, err)
		}
	}
	moved, err := d.load(ctx)
	if err != nil {
		return item, err
	}
	switch {
	case moved.Status == next:
	case d.route.Spec.IsParkState(moved.Status):
		// The set's own mechanism parked it: a performer rejected the premise, or a
		// spend bound was broken where the story would park.
		d.res.Parked, d.res.Status, d.res.Reason = true, moved.Status, d.recordedReason(ctx)
		return moved, nil
	default:
		return moved, fmt.Errorf("drive: presenting %s → %s left the story at %q", from, next, moved.Status)
	}
	d.res.Moves = append(d.res.Moves, Move{From: from, To: next})
	d.res.Status = next
	d.res.Terminal = d.route.Spec.IsTerminalState(next)
	return moved, d.performEntered(ctx, moved, next)
}

// performEntered runs the headless executor over a step the story has just
// entered, when the workflow allocates that step to agent=executor. A step a
// named agent performs ran during its entry, and an agent-less step has nothing
// to run.
func (d *driver) performEntered(ctx context.Context, item workitem.Item, step string) error {
	perf, err := d.opts.Performers.StepPerformer(ctx, item, step)
	if err != nil {
		return fmt.Errorf("drive: step %q: %w", step, err)
	}
	if perf.Kind != verb.PerformerExecutor {
		return nil
	}
	return d.perform(ctx, item, step, "")
}

// perform runs the executor binding over step. A performer that rejects parks the
// story with its notes; a run that fails is an error and leaves the story at the
// step, where a re-run of the drive finds the exit gates judging what was done.
func (d *driver) perform(ctx context.Context, item workitem.Item, step, notes string) error {
	fmt.Fprintf(d.out, "drive: %s performing %s with the executor binding\n", d.id, step)
	run, err := d.opts.Performers.PerformExecutorStep(ctx, item, step, notes)
	var rejected *verb.PerformerReject
	if errors.As(err, &rejected) {
		verb.RecordStepPerformance(ctx, item, step, run, nil)
		return d.park(ctx, item, fmt.Sprintf("the executor rejected step %s: %s", step, rejected.Error()))
	}
	verb.RecordStepPerformance(ctx, item, step, run, err)
	if err != nil {
		return fmt.Errorf("drive: step %q: %w", step, err)
	}
	return nil
}

// answerRejection carries a rejected edge's findings to whoever must act on them
// before the edge is presented again. The performer is the one the engine names
// for the edge's destination step, when entering it dispatches a performer (it is
// re-dispatched with the findings on re-presentation); otherwise the step being
// left, whose work the reviewers judged. A named performer gets the findings as a
// message and, when the step it performed declares a rework relay, that relay
// runs; the executor binding is re-run with them appended to its prompt. A relay
// that ends without agreement, or an executor that rejects, parks the story
// (Result.Parked) instead.
func (d *driver) answerRejection(ctx context.Context, item workitem.Item, to, notes string) error {
	from := item.Status
	toPerf, err := d.opts.Performers.StepPerformer(ctx, item, to)
	if err != nil {
		return fmt.Errorf("drive: step %q: %w", to, err)
	}
	fromPerf, err := d.opts.Performers.StepPerformer(ctx, item, from)
	if err != nil {
		return fmt.Errorf("drive: step %q: %w", from, err)
	}
	target := toPerf
	if target.Kind != verb.PerformerDispatched {
		target = fromPerf
	}
	switch target.Kind {
	case verb.PerformerDispatched:
		if err := d.message(ctx, target.Agent, notes); err != nil {
			return err
		}
	case verb.PerformerExecutor:
		return d.perform(ctx, item, from, notes)
	}
	if fromPerf.Kind != verb.PerformerDispatched || d.opts.Rework == nil || !d.declaresRework(from) {
		return nil
	}
	rw, err := d.opts.Rework(ctx, d.id)
	if err != nil {
		return fmt.Errorf("drive: rework of step %q: %w", from, err)
	}
	if rw.Converged {
		return nil
	}
	objection := rw.LastObjection
	if objection == "" {
		objection = notes
	}
	return d.park(ctx, item, fmt.Sprintf(
		"the %s rework relay ended %s after %d round(s) without agreement; last objection: %s",
		from, rw.Outcome, rw.Rounds, objection))
}

func (d *driver) declaresRework(step string) bool {
	for _, rw := range d.route.Reworks {
		if rw.Step == step {
			return true
		}
	}
	return false
}

// message records the findings as a directed message to the performer, through
// the same verb `satelle story message` uses, so its next payload carries them.
func (d *driver) message(ctx context.Context, to, body string) error {
	req, _ := json.Marshal(map[string]string{"id": d.id, "from": "orchestrator", "to": to, "body": body})
	if _, err := verb.Dispatch(ctx, "story-message", req); err != nil {
		return fmt.Errorf("drive: relay to %s: %w", to, err)
	}
	return nil
}

// park moves the story to the route's park state with reason recorded on its
// timeline first, which is what the park gate reads. A workflow with no park
// state leaves the drive nowhere to put the question, so it refuses.
func (d *driver) park(ctx context.Context, item workitem.Item, reason string) error {
	state := verb.ResumeParkName(ctx, item)
	if state == "" {
		return fmt.Errorf("drive: %s needs a person (%s) but the workflow offers no park state to hold the question", d.id, reason)
	}
	payload, _ := json.Marshal(map[string]string{"from": item.Status, "to": state, "source": "drive"})
	entry, _ := json.Marshal(map[string]any{
		"story_id": d.id, "kind": ledger.KindComment, "actor": "orchestrator", "body": reason, "payload": json.RawMessage(payload),
	})
	if _, err := verb.Dispatch(ctx, "ledger-append", entry); err != nil {
		return fmt.Errorf("drive: record the question on %s: %w", d.id, err)
	}
	if _, err := d.set(ctx, state); err != nil {
		return fmt.Errorf("drive: park %s at %s: %w", d.id, state, err)
	}
	fmt.Fprintf(d.out, "drive: %s parked at %s\n", d.id, state)
	d.res.Parked, d.res.Status, d.res.Reason = true, state, reason
	return nil
}

// set presents a status move through the story-set verb.
func (d *driver) set(ctx context.Context, status string) (json.RawMessage, error) {
	req, _ := json.Marshal(map[string]string{"id": d.id, "status": status})
	return verb.Dispatch(ctx, "story-set", req)
}

func (d *driver) load(ctx context.Context) (workitem.Item, error) {
	req, _ := json.Marshal(map[string]string{"id": d.id})
	raw, err := verb.Dispatch(ctx, "story-get", req)
	if err != nil {
		return workitem.Item{}, fmt.Errorf("drive: %w", err)
	}
	var item workitem.Item
	if err := json.Unmarshal(raw, &item); err != nil {
		return workitem.Item{}, fmt.Errorf("drive: decode %s: %w", d.id, err)
	}
	return item, nil
}

// recordedReason is the newest comment on the story's timeline — what a park
// mechanism, or the drive, wrote down as the reason.
func (d *driver) recordedReason(ctx context.Context) string {
	req, _ := json.Marshal(map[string]any{"story_id": d.id, "kind": ledger.KindComment, "limit": 1})
	raw, err := verb.Dispatch(ctx, "ledger-list", req)
	if err != nil {
		return ""
	}
	var rows []ledger.Entry
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return ""
	}
	return strings.TrimSpace(rows[len(rows)-1].Body)
}
