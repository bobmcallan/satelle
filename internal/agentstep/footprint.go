package agentstep

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

// SeatPromptInput is what SeatSystemPrompt assembles a seat's prompt from. The
// caller resolves the constitution and the resident principles (empty when the
// seat's binding injects none); everything else is the fixed text every dispatch
// of that kind carries.
type SeatPromptInput struct {
	Kind     SeatKind
	Section  string // agents.toml section the seat runs under
	Step     string // step or status the seat serves; only names the charter
	Workflow string // workflow name; only names the performer charter
	// Constitution and Resident are the project constitution body and the resolved
	// principle bodies. Empty means the binding injects none.
	Constitution string
	Resident     string
	// Rubric is the seat's skill body (its largest one, for a size estimate).
	Rubric string
}

// SeatSystemPrompt returns the system prompt a dispatched seat would receive,
// built by the same composeSystemPrompt buildRequest uses. It leaves out the
// per-dispatch scratch briefing, which names a directory that only exists for a
// live dispatch, so the length is stable across runs. The injected-size report
// measures this; it is estimation, never dispatch.
func SeatSystemPrompt(in SeatPromptInput) string {
	var charter string
	switch in.Kind {
	case SeatReviewer:
		charter = reviewerCharter()
	case SeatConsult:
		charter = consultCharter(in.Section, "consultant", in.Step)
	default:
		charter = executorCharter(in.Section, in.Step, in.Workflow)
	}
	return composeSystemPrompt(promptParts{
		constitution: in.Constitution,
		resident:     in.Resident,
		charter:      charter,
		rubric:       in.Rubric,
	})
}
