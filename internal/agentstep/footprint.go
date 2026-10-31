package agentstep

import (
	"context"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/compact"
	"github.com/bobmcallan/satelle/internal/condense"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

// SeatKind names which role charter a dispatched seat's system prompt opens with.
type SeatKind string

const (
	// SeatReviewer is an isolated gate reviewer (and the step summariser).
	SeatReviewer SeatKind = "reviewer"
	// SeatPerformer is a named isolated executor performing a step (planner, coder).
	SeatPerformer SeatKind = "performer"
	// SeatConsult is a binding opened as a consultant by the rework relay.
	SeatConsult SeatKind = "consult"
)

// SeatPayloadInput is everything one dispatched seat's system prompt is built
// from. The caller resolves the documents (the constitution body, the resident
// principle documents — empty when the seat's binding injects none) and the
// budget; the text of the prompt is BuildSeatPayload's.
type SeatPayloadInput struct {
	Constitution string
	Principles   []docindex.Doc // resident principle documents, in delivery order
	Charter      string         // role charter
	Scratch      string         // this dispatch's scratch dir; "" omits the briefing
	Rubric       string         // the skill body
	Judging      bool           // the judging briefing replaces the pull-context call to action
	Inline       bool           // judging with the work item inline: the pull-context call to action stays
	Shell        bool           // the judging briefing also names the CLI lines
	Verdict      string         // the shared verdict rules (ReviewerVerdictDoc); delivered only when Judging

	// Section names the seat in the pointer directive. BudgetBytes is the size the
	// whole prompt is fitted to; zero or less leaves the ladder off, and every
	// resident document is delivered as written.
	Section     string
	BudgetBytes int
	// PayloadBytes is the size of the stdin payload sent beside the prompt. The
	// seat's budget covers both (sty_73a2dc68), so the fitting leaves room for it.
	PayloadBytes int
	// Rules is how a resident document is condensed (zero means the embedded
	// defaults). Offload keeps the originals of condensed documents; nil sizes the
	// markers without storing anything, which makes the result an estimate.
	Rules   condense.Rules
	Offload compact.Offloader
}

// SeatPayload is a built system prompt and the rung each resident document was
// delivered at.
type SeatPayload struct {
	SystemPrompt string
	Rungs        []condense.Rung
}

// BuildSeatPayload builds the system prompt a dispatched seat receives, in the
// one canonical order: constitution and principles, role charter, scratch
// briefing, pull-context call to action, skill rubric. The constitution and the
// principles are the part that is fitted: condense.Fit steps them down the ladder
// until the whole prompt is within BudgetBytes, so the charter, briefing and
// rubric — the instructions the seat acts on — are always delivered as written.
func BuildSeatPayload(in SeatPayloadInput) (SeatPayload, error) {
	tail := composeSystemPrompt(promptParts{
		charter: in.Charter, scratch: in.Scratch, rubric: in.Rubric, judging: in.Judging, inline: in.Inline, shell: in.Shell, verdict: in.Verdict,
	})
	var items []condense.Item
	if c := strings.TrimSpace(in.Constitution); c != "" {
		items = append(items, condense.Item{Name: "constitution", Heading: "# Project constitution", Body: c})
	}
	for _, d := range in.Principles {
		body := strings.TrimSpace(stripFrontmatter(d.Body))
		if body == "" {
			continue
		}
		// A judging seat is handed the verdict rules in the tail; a selector that
		// also picks them up (system, all) must not deliver them a second time.
		if in.Judging && strings.TrimSpace(in.Verdict) != "" && d.Name == config.ReviewerVerdictPrinciple {
			continue
		}
		items = append(items, condense.Item{
			Name: d.Name, Group: "# Always-resident principles (satelle)", Body: body, Original: d.Body,
			Pointer: "- `" + d.Name + "` — read: `satelle doc get principles " + d.Name + "`",
		})
	}
	if len(items) == 0 {
		return SeatPayload{SystemPrompt: tail}, nil
	}
	rules := in.Rules
	if rules.Unset() {
		var err error
		if rules, err = condense.ParseRules(config.EmbeddedCondense()); err != nil {
			return SeatPayload{}, err
		}
	}
	budget := 0
	if in.BudgetBytes > 0 {
		budget = max(in.BudgetBytes-in.PayloadBytes-len(tail), 1)
	}
	frame := condense.Frame{PointerHeader: "OMITTED FOR THE " + strings.ToUpper(in.Section) + " CONTEXT BUDGET (" +
		strconv.Itoa(in.BudgetBytes) + " bytes): the following are not inlined — read each now with the command shown, before doing any work."}
	head, rungs, err := condense.Fit(items, frame, budget, func(s string) int { return len(s) }, rules, in.Offload)
	if err != nil {
		return SeatPayload{}, err
	}
	return SeatPayload{SystemPrompt: head + tail, Rungs: rungs}, nil
}

// SeatPromptInput is what SeatPayloadFor assembles a seat's prompt from for the
// injected-size report: the role the seat plays, and the documents it is handed.
type SeatPromptInput struct {
	Kind     SeatKind
	Section  string // agents.toml section the seat runs under
	Step     string // step or status the seat serves; only names the charter
	Workflow string // workflow name; only names the performer charter
	// Constitution and Principles are the project constitution body and the
	// resolved principle documents. Empty means the binding injects none.
	Constitution string
	Principles   []docindex.Doc
	// Rubric is the seat's skill body (its largest one, for a size estimate).
	Rubric string
	// Judging is whether the seat judges a gate — the dispatch that is handed the
	// judging briefing and Verdict. A summary seat of a reviewer kind does not
	// judge, so the estimate keys on this and not on Kind.
	Judging bool
	// Verdict is ReviewerVerdictDoc's text; delivered only when Judging.
	Verdict string
	// Shell is whether the seat's grant admits a shell, which a judging
	// reviewer's briefing then spells three more CLI lines for.
	Shell bool
	// BudgetBytes, Rules and Offload are SeatPayloadInput's.
	BudgetBytes int
	Rules       condense.Rules
	Offload     compact.Offloader
}

// estimateScratch stands in for a dispatch's scratch directory, which only exists
// for a live dispatch: a path of the length a real one has (<tmp>/satelle/<repo
// key>/<story>/<dispatch id>), so the estimate carries the briefing's weight and
// its length is stable across runs.
const estimateScratch = "/tmp/satelle/000000000000/sty_00000000/00000000"

// SeatPayloadFor returns the system prompt a dispatched seat would receive, built
// by the same BuildSeatPayload buildRequest uses, with a stand-in scratch
// directory. The injected-size report and the context audit measure this; it is
// estimation, never dispatch.
func SeatPayloadFor(in SeatPromptInput) (SeatPayload, error) {
	var charter string
	switch in.Kind {
	case SeatReviewer:
		charter = reviewerCharter()
	case SeatConsult:
		charter = consultCharter(in.Section, "consultant", in.Step)
	default:
		charter = executorCharter(in.Section, in.Step, in.Workflow)
	}
	return BuildSeatPayload(SeatPayloadInput{
		Constitution: in.Constitution, Principles: in.Principles, Charter: charter, Rubric: in.Rubric,
		Scratch: estimateScratch, Judging: in.Judging, Verdict: in.Verdict, Shell: in.Shell,
		Section: in.Section, BudgetBytes: in.BudgetBytes, Rules: in.Rules, Offload: in.Offload,
	})
}

// offloadFunc adapts the engine's story-keyed offloader to compact.Offloader.
type offloadFunc func(content []byte) (string, error)

func (f offloadFunc) Put(content []byte) (string, error) { return f(content) }

// seatOffloader is the compact.Offloader a dispatch's originals go through, keyed
// to the story so retention covers them; nil when no store is wired.
func (g *Engine) seatOffloader(ctx context.Context, storyID string) compact.Offloader {
	if g.diffOffload == nil {
		return nil
	}
	return offloadFunc(func(content []byte) (string, error) { return g.diffOffload(ctx, storyID, content) })
}
