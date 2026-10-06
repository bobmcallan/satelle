package wfgovern

import (
	"errors"
	"fmt"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// ErrAuthoredProcessUnreadable reports that the repository's authored workflows
// dir EXISTS and cannot be read (sty_d6e209aa). It is deliberately distinct from
// ErrNoWorkflow and from an absent dir: an absent dir is a fresh repo that the
// embedded backstop may govern, whereas an unreadable one is a process the repo
// authored and satelle cannot see — silently governing by the binary's default
// would run someone else's lifecycle and call it the repository's.
var ErrAuthoredProcessUnreadable = errors.New("wfgovern: authored process is unreadable")

// embeddedLaneClause names the route that WOULD have governed had the authored
// one been readable, and says what that means for the story's gates. Built from
// the route's own constants so a rename cannot leave the message stale.
func embeddedLaneClause() string {
	return fmt.Sprintf("the embedded default route (binary-shipped %s.toml + %s.toml, lane %q) would otherwise govern; this story's gates would not be the repository's gates",
		RouteSourceDone, RouteSourceStep, DerivedRouteName)
}

// EmbeddedLaneNotice is the clause every refusal about a missing, unreadable or
// broken authored process appends: which embedded route would otherwise govern,
// and that its gates are not the repository's.
func EmbeddedLaneNotice() string { return embeddedLaneClause() }

// UnreadableMessage renders the refusal text for an unreadable workflows dir.
func UnreadableMessage(path, reason string) string {
	return fmt.Sprintf("authored workflows dir %s cannot be read (%s) — %s", path, reason, embeddedLaneClause())
}

// AbsentMessage renders the REPORT (not a refusal) for an absent workflows dir:
// the embedded backstop still governs, and the story's gates are the binary's
// defaults rather than authored ones.
func AbsentMessage(path string) string {
	return fmt.Sprintf("authored workflows dir %s does not exist — the embedded default route (binary-shipped %s.toml + %s.toml, lane %q) now governs; this story's gates are the binary's defaults, not authored ones",
		path, RouteSourceDone, RouteSourceStep, DerivedRouteName)
}

// Err is non-nil when the doc set this RouteSource was read from carried the
// unreadable-dir sentinel. A caller that gates MUST check it before Present():
// Present is false in that state too, and reading it as "no route authored"
// is the silent fallback this error exists to prevent.
func (rs RouteSource) Err() error {
	if rs.Unreadable.Path == "" && rs.Unreadable.Reason == "" {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrAuthoredProcessUnreadable, UnreadableMessage(rs.Unreadable.Path, rs.Unreadable.Reason))
}

// RouteGovernsErr is the ONE precedence rule for a derived route, and every
// surface that resolves, displays or stamps a lifecycle asks it rather than
// re-deriving its own answer.
//
// An AUTHORED route (the repo's own done.toml + step.toml) governs the
// categories it claims — a repo that converted is governed by what it wrote. The
// route the BINARY ships is order zero instead: it governs a category only when
// no authored workflow claims that category. Without that distinction, shipping
// the defaults as a route would silently shadow every repo's authored graph on
// the next binary upgrade, because the doc index overlays an embedded default
// wherever the repo has no file of that name.
//
// An UNREADABLE authored dir is a third state, returned as an error and never
// as "no route governs": the embedded route is not consulted at all
// (sty_d6e209aa). An empty category means the wildcard view, which a route
// answers with its `*` section.
func RouteGovernsErr(workflows []docindex.Doc, category string) (RouteSource, bool, error) {
	rs := RouteSourceOf(workflows)
	if err := rs.Err(); err != nil {
		return RouteSource{}, false, err
	}
	if !rs.Present() || !routeClaims(rs, category) {
		return RouteSource{}, false, nil
	}
	if rs.Embedded && len(OrderedWorkflows(LifecycleWorkflows(workflows), category)) > 0 {
		return RouteSource{}, false, nil // an authored workflow outranks the shipped route
	}
	return rs, true, nil
}

// RouteProvenance reports whether the route governing item comes entirely from
// the binary's shipped defaults, and the lane's name. It is how a read surface
// says "these gates are the binary's" without re-deriving the precedence rule.
func RouteProvenance(workflows []docindex.Doc, item workitem.Item) (embedded bool, lane string) {
	rs, ok, err := RouteGovernsErr(workflows, WorkflowCategory(item))
	if err != nil || !ok {
		return false, ""
	}
	return rs.Embedded, DerivedRouteName
}
