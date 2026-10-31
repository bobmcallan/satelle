package cli

// The hooks judge an edit, commit or push by the tree it lands in (sty_9c782f0e):
// the story engaged from that tree — its latest working_on row at this location
// names the tree — and that story's committed status and route allocation. A
// transition running its gates or dispatch is the story's open
// transition_started row. Nothing is held: presence is read, never refused on.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// engagement is one story or task this repo is performing: its committed status
// is a performing step of its route, or a transition on it is in flight. Empty
// ItemID means none was relevant.
type engagement struct {
	ItemID string
	// State is the committed status when it is performing, else the target of
	// the transition in flight (messaging).
	State string
	// TargetState is the in-flight target, else the committed status: what a
	// dispatched performer is authenticated against.
	TargetState string
	StoryStatus string // committed work-item status (step policy; sty_c21490cc)
	StateAgent  string // agent the route allocates to TargetState
	// StateRework is true when the route step StateAgent performs declares a
	// rework loop (rework = { consult, rounds }). It decides whether the deny
	// text may name `satelle story rework` (sty_a7914904).
	StateRework bool
	// Worktree and SessionID are the latest working_on row's at this location:
	// the tree and session the item was engaged from. EngagedAt is that row's
	// time; empty when the item was never engaged here.
	Worktree  string
	SessionID string
	EngagedAt time.Time
	// Mine is true when SessionID is the resolving session's.
	Mine     bool
	InFlight bool
	// EditCapable is true only when the committed status is a spine performing
	// step the route allocates to agent=executor.
	EditCapable bool
	EditStates  []string
	// DispatchAgents is the route-authored performer identity by state.
	DispatchAgents map[string][]string
	// Engaged is true for every resolved engagement; the zero value is none.
	Engaged bool
	// Advance are route-declared forward transitions from the committed status —
	// terminal, park, and back-edges excluded (sty_e16a2cd7). Empty when the item
	// is engaged only by a transition in flight.
	Advance []wfdot.Advance
}

// engagementView is one read of the store: every performing item, and the items
// and workflows it was computed from.
type engagementView struct {
	Live  []engagement
	Items []workitem.Item
	Wfs   []docindex.Doc
}

// currentEngagement resolves the engagement this session's tree holds.
func currentEngagement() (info engagement, engaged bool, err error) {
	info, engaged, _, err = resolveEngagements(config.ResolveSession())
	return info, engaged, err
}

// resolveEngagements is resolveEngagementsFor the session's own tree.
func resolveEngagements(sessionID string) (info engagement, engaged bool, live []engagement, err error) {
	return resolveEngagementsFor(sessionID, "")
}

// resolveEngagementsFor is the ONE body behind every engagement lookup: the item
// a dispatched performer or rework relay was spawned for, else the item engaged
// from targetTree (the git root of the path being acted on; "" means the
// session's own tree). live is every performing item in the repo.
func resolveEngagementsFor(sessionID, targetTree string) (info engagement, engaged bool, live []engagement, err error) {
	a, openErr := app.Open()
	if openErr != nil {
		return engagementOpenFailed(openErr)
	}
	defer func() { _ = a.Close() }()
	return resolveEngagementsOn(a, sessionID, targetTree)
}

// engagementOpenFailed is the engagement answer for a store that would not open.
func engagementOpenFailed(openErr error) (info engagement, engaged bool, live []engagement, err error) {
	// An ungoverned repo has no engagement to determine — a session opened in an
	// ordinary non-satelle directory must go INERT, not error (sty_20a7824c).
	if errors.Is(openErr, app.ErrNotInitialised) {
		return engagement{}, false, nil, nil
	}
	return engagement{}, false, nil, fmt.Errorf("cannot determine engagement (store open failed: %w) — fix config and retry", openErr)
}

// resolveEngagementsOn is resolveEngagementsFor on an App the caller opened and
// closes.
func resolveEngagementsOn(a *app.App, sessionID, targetTree string) (info engagement, engaged bool, live []engagement, err error) {
	view, err := readEngagements(context.Background(), a)
	if err != nil {
		return engagement{}, false, nil, err
	}
	pick, ok := markedEngagement(view.Live, currentDispatchMarker().Item, currentRelayMarker().Item)
	if !ok {
		pick, ok = pickEngagementFor(view.Live, targetTree)
	}
	if !ok {
		return engagement{}, false, view.Live, nil
	}
	if sid := strings.TrimSpace(sessionID); sid != "" && pick.SessionID == sid {
		pick.Mine = true
	}
	return pick, true, view.Live, nil
}

// readEngagements reads a's store into the items it is performing.
func readEngagements(ctx context.Context, a *app.App) (engagementView, error) {
	wfs, err := a.Store.DocIndex.List(ctx, "workflows")
	if err != nil {
		return engagementView{}, fmt.Errorf("cannot determine engagement (workflow list failed: %w) — fix config and retry", err)
	}
	items, err := a.Store.Stories.List(ctx, workitem.ListFilter{})
	if err != nil {
		return engagementView{}, fmt.Errorf("cannot determine engagement (story list failed: %w) — fix config and retry", err)
	}
	open, err := a.Store.Ledger.ListOpenTransitions(ctx, "", time.Now().UTC())
	if err != nil {
		return engagementView{}, fmt.Errorf("cannot determine engagement (transition read failed: %w) — fix config and retry", err)
	}
	self := hosted.KnownLocationID(a.RepoRoot)
	presence := func(id string) (verb.WorkingOnRow, bool) {
		rows, _, lerr := a.Store.Ledger.ListNewest(ctx, ledger.ListFilter{StoryID: id, Kind: ledger.KindWorkingOn, Limit: 50})
		if lerr != nil {
			return verb.WorkingOnRow{}, false
		}
		return verb.LatestWorkingOn(rows, self)
	}
	live, err := evaluateEngagements(items, wfs, open, presence)
	if err != nil {
		return engagementView{}, err
	}
	return engagementView{Live: live, Items: items, Wfs: wfs}, nil
}

// evaluateEngagements is the pure engagement predicate. An item is performing
// when its committed status is a non-terminal engaging state of its governing
// route and it is not a container waiting on open children, or when a transition
// on it is open. presence returns the item's latest working_on row here.
//
// It fails closed: an item in flight, or engaged here at its current status,
// whose route does not resolve or does not declare its state is an error, not a
// silent allow.
func evaluateEngagements(items []workitem.Item, wfs []docindex.Doc, open map[string]ledger.Transition, presence func(string) (verb.WorkingOnRow, bool)) ([]engagement, error) {
	var live []engagement
	for _, it := range items {
		t, inFlight := open[it.ID]
		// engagedHere is read only for an item the route cannot classify: a
		// presence lookup per settled item would read the ledger for every story.
		engagedHere := func() bool {
			w, ok := presence(it.ID)
			return ok && w.State == it.Status
		}
		route, wfName, serr := wfgovern.RouteFor(wfs, it)
		if serr != nil {
			if inFlight || engagedHere() {
				return nil, fmt.Errorf("item %s: %w — cannot determine engagement", it.ID, serr)
			}
			continue
		}
		spec := route.Spec
		if _, known := spec.StateAgent(it.Status); !known {
			if inFlight || engagedHere() {
				return nil, fmt.Errorf(
					"item %s status %q is not declared by workflow %s — cannot classify edit permission",
					it.ID, it.Status, wfName)
			}
			continue
		}
		engaging := slices.Contains(spec.NonTerminalEngagingStates(), it.Status)
		if !inFlight && (!engaging || waitsOnOpenChildren(it, it.Status, spec, items, wfs)) {
			continue
		}
		target := it.Status
		if inFlight && t.To != "" {
			target = t.To
		}
		agent, known := spec.StateAgent(target)
		if !known {
			return nil, fmt.Errorf(
				"item %s transition targets state %q not declared by workflow %s — cannot classify edit permission",
				it.ID, target, wfName)
		}
		e := engagement{
			ItemID: it.ID, State: target, TargetState: target, StoryStatus: it.Status,
			StateAgent: agent, StateRework: stepDeclaresRework(route.Reworks, target),
			InFlight: inFlight, EditCapable: spec.IsEditCapableState(it.Status),
			EditStates: spec.EditCapableStates(), DispatchAgents: dispatchAgents(spec),
			Engaged: true,
		}
		if engaging {
			e.State = it.Status
			e.Advance = spec.AdvanceOptions(it.Status)
		}
		if w, ok := presence(it.ID); ok {
			e.Worktree, e.SessionID, e.EngagedAt = w.Worktree, w.Session, w.At
		}
		live = append(live, e)
	}
	return live, nil
}

// markedEngagement selects the engagement a dispatched performer or rework relay
// runs under by the one identity it carries: the item it was spawned for. It
// only SELECTS; permission is still editPermitted's or
// dispatchedPerformerPermitted's.
func markedEngagement(live []engagement, items ...string) (engagement, bool) {
	for _, id := range items {
		if id == "" {
			continue
		}
		for _, e := range live {
			if e.ItemID == id {
				return e, true
			}
		}
	}
	return engagement{}, false
}

// pickEngagementFor chooses the item engaged from targetTree ("" means the
// session's own tree). Several engaged from one tree pick the one engaged most
// recently; none is no engagement. A caller outside any git tree matches an item
// engaged from outside one.
func pickEngagementFor(live []engagement, targetTree string) (engagement, bool) {
	tree := targetTree
	if tree == "" {
		tree = sessionWorktree()
	}
	var pick engagement
	found := false
	for _, e := range live {
		if !engagedFrom(e, tree) {
			continue
		}
		if !found || e.EngagedAt.After(pick.EngagedAt) {
			pick, found = e, true
		}
	}
	return pick, found
}

// engagedFrom reports whether e was engaged from tree.
func engagedFrom(e engagement, tree string) bool {
	if tree == "" {
		return e.Worktree == ""
	}
	return sameTree(e.Worktree, tree)
}

// formatEngagement renders a one-line engagement descriptor for deny reasons and
// session inject.
func formatEngagement(e engagement) string {
	if e.ItemID == "" {
		return ""
	}
	tree := e.Worktree
	if tree == "" {
		tree = "unrecorded"
	}
	s := fmt.Sprintf("%s engaged (%s, tree %s", e.ItemID, e.StoryStatus, tree)
	if e.SessionID != "" {
		s += ", session " + e.SessionID
	}
	s += ")"
	if e.InFlight {
		s += fmt.Sprintf(" — transition to %q in flight", e.TargetState)
	}
	return s
}

// unattributedEditReason is the deny when items are performing but none was
// engaged from the tree the edit lands in: it names each with its tree instead
// of attributing the edit to one.
func unattributedEditReason(live []engagement, tree string) string {
	parts := make([]string, 0, len(live))
	for _, e := range live {
		parts = append(parts, formatEngagement(e))
	}
	return fmt.Sprintf(
		"satelle: no performing story was engaged from this tree (%q) — %s. Act from that story's worktree, or record this tree for it with `satelle story set <id> --status <its status>` (same status) and retry.",
		tree, strings.Join(parts, "; "))
}

// editGateDenyReason picks the agent-facing text when the tree resolved no
// engagement: the performing items and their trees when there are any, else
// the generic "open a story" message.
func editGateDenyReason(live []engagement, tree string) string {
	if len(live) > 0 {
		return unattributedEditReason(live, tree)
	}
	return noEngagedStoryEditReason
}

// concurrencyLine states the concurrency rule a story engages under, so an agent
// meets it before a refusal does.
func concurrencyLine(limit int) string {
	if limit <= 0 {
		return "concurrency: unlimited — several stories may perform at once; a sequential epic admits one performing child at a time."
	}
	return fmt.Sprintf("concurrency: [engagement] limit = %d — at most %d performing stories at once, counted from their status; a sequential epic admits one performing child at a time.", limit, limit)
}

// scheduleLines names the schedule of every scheduled epic container in
// context: an engaged item that is itself an epic-parent, or whose parent is
// one. Silent (empty) when none is in an epic container.
func scheduleLines(engaged []string, items []workitem.Item, wfs []docindex.Doc) string {
	byID := make(map[string]workitem.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	seen := map[string]bool{}
	var lines []string
	for _, id := range engaged {
		c, ok := byID[id]
		if ok && !epicset.IsEpicParent(c) {
			c, ok = byID[c.ParentID]
		}
		if !ok || !epicset.IsEpicParent(c) || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		if l := scheduleLine(c.ID, verb.ContainerSchedule(wfs, c)); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// sessionEngagementBlock is the SessionStart inject: every item this repo is
// performing, this tree's first, and the concurrency rule. "" when nothing
// performs and no limit is configured. Fail-open — a read error injects nothing,
// except an unreadable authored process, which is said (sty_d6e209aa).
func sessionEngagementBlock(a *app.App) string {
	if a == nil || a.Store == nil {
		return ""
	}
	limit := a.PlaneConfig().ResolveEngagementLimit()
	view, err := readEngagements(context.Background(), a)
	if errors.Is(err, wfgovern.ErrAuthoredProcessUnreadable) {
		return "## Engaged stories\n" + err.Error()
	}
	if err != nil || (len(view.Live) == 0 && limit == 0) {
		return ""
	}
	lines := []string{"## Engaged stories"}
	if len(view.Live) == 0 {
		lines = append(lines, "none performing.")
	}
	mine, _ := pickEngagementFor(view.Live, "")
	if mine.ItemID != "" {
		lines = append(lines, formatEngagement(mine)+" — this tree")
	}
	ids := make([]string, 0, len(view.Live))
	for _, e := range view.Live {
		ids = append(ids, e.ItemID)
		if e.ItemID != mine.ItemID {
			lines = append(lines, formatEngagement(e))
		}
	}
	lines = append(lines, concurrencyLine(limit))
	block := strings.Join(lines, "\n")
	if sched := scheduleLines(ids, view.Items, view.Wfs); sched != "" {
		block += "\n\n" + sched
	}
	return block
}

// appendBlockToContext merges a block into SessionStart content when room
// remains under ceiling. Pure — unit-tested so removing the inject path fails a
// test rather than leaving the suite green.
func appendBlockToContext(content, block string, ceiling int) string {
	if strings.TrimSpace(block) == "" {
		return content
	}
	if content == "" {
		return block
	}
	if ceiling > 0 && len(content)+2+len(block) > ceiling {
		return content // prefer principles over a truncated block
	}
	return content + "\n\n" + block
}

// appendEngagementToPrompt appends a one-line engagement descriptor to the
// UserPromptSubmit reminder when one resolved. Pure for unit tests.
func appendEngagementToPrompt(msg string, info engagement) string {
	if info.ItemID == "" {
		return msg
	}
	return msg + "\n" + formatEngagement(info)
}

// stopcheckEngaged reports whether an item is engaged from this session's tree:
// a dirty tree is then that item's work. It never writes.
func stopcheckEngaged() (bool, error) {
	_, engaged, _, err := resolveEngagements(config.ResolveSession())
	return engaged, err
}

// relayDenyReason names why a marked rework-relay coder was refused. It never
// uses the unmarked "without a performing story" or "allocated to
// agent=executor" wording — those mis-diagnose a relay.
func relayDenyReason(info engagement, rm relayMarker) string {
	pre := readOnlyPreflightReason()
	item := strings.TrimSpace(rm.Item)
	binding := strings.TrimSpace(rm.Binding)
	if info.ItemID == "" || !info.Engaged || info.ItemID != item {
		return fmt.Sprintf(
			"satelle: rework relay for story %s requires that story performing at a committed status allocated to the binding — it is not performing. %s",
			item, pre)
	}
	if info.InFlight {
		return fmt.Sprintf(
			"satelle: rework relay coder cannot edit while story %s has a transition to %q in flight. %s",
			info.ItemID, info.TargetState, pre)
	}
	status := info.StoryStatus
	if status == "" {
		status = info.State
	}
	agents := info.DispatchAgents[status]
	if len(agents) == 0 && status == info.State && info.StateAgent != "" {
		agents = []string{info.StateAgent}
	}
	allocated := strings.Join(agents, ", ")
	if allocated == "" {
		allocated = "(none)"
	}
	return fmt.Sprintf(
		"satelle: story %s is at %q, which its route allocates to [%s]; the rework relay permits edits only from the binding allocated to the committed status (this session is marked %q). %s",
		info.ItemID, status, allocated, binding, pre)
}
