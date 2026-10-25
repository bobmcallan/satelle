package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{Name: "story-wave", Description: "List the children of an epic-parent that may start now (read-only: engages, dispatches and writes nothing)", Invoke: storyWave})
}

// waveReq is the request for story-wave.
type waveReq struct {
	ID string `json:"id"`
}

// WaveBlocker is one unsatisfied dependency of an omitted child.
type WaveBlocker struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Cancelled marks a dependency that left the work without finishing it: its
	// commits are not there, so it does not satisfy the edge.
	Cancelled bool `json:"cancelled,omitempty"`
}

// WaveOmitted is a non-terminal child held back, and why.
type WaveOmitted struct {
	ID        string        `json:"id"`
	WaitingOn []WaveBlocker `json:"waiting_on,omitempty"`
	Reason    string        `json:"reason"`
}

// WaveDelivery is how one non-terminal child will be delivered: whether it runs
// alongside its siblings, its lane, and where the step that delivers it runs, as
// the engine's performer rule answers (PerformerResolver.StepPerformer) — the
// same answer a dispatch acts on. Reason is the orchestrator's, as the readiness
// round recorded it.
type WaveDelivery struct {
	ID string `json:"id"`
	// Order is "parallel", "sequential" (the container's schedule), or
	// "after <ids>" for a child with depends-on edges.
	Order string `json:"order"`
	// Lane is the value of the child's tag under the readiness step's lane_tag
	// prefix; empty when it carries none.
	Lane string `json:"lane,omitempty"`
	// Step is the child's delivering step: the first step from its status that
	// honours a placement pin (declares placement_tag) and has a performer, else
	// the first step with a performer. Empty with Placement "none" when no step
	// has one.
	Step string `json:"step,omitempty"`
	// Placement is the engine's resolution of Step, rendered by renderPlacement.
	Placement string `json:"placement,omitempty"`
	// Agent is the binding that performs Step.
	Agent string `json:"agent,omitempty"`
	// Note is the placement note (a remote declaration that fell back to local).
	Note string `json:"note,omitempty"`
	// Reason is the orchestrator's recorded reason for the choice.
	Reason string `json:"reason,omitempty"`
	// Error is why the placement could not be resolved (an invalid pin, a missing
	// binding); Placement is empty then.
	Error string `json:"error,omitempty"`
}

// WaveResult is the story-wave response.
type WaveResult struct {
	Container string         `json:"container"`
	Theme     string         `json:"theme"`
	Schedule  string         `json:"schedule"`
	Runnable  []string       `json:"runnable"`
	Omitted   []WaveOmitted  `json:"omitted,omitempty"`
	Delivery  []WaveDelivery `json:"delivery,omitempty"`
	// children are the container's members as assessWave read them, so story wave
	// can describe their delivery without resolving the set twice.
	children []workitem.Item
}

// waveState is what the route says about one story: finished (the route's
// terminal), cancelled (a non-resuming park), or neither. known is false when no
// route resolves for it.
type waveState struct {
	Status    string
	Done      bool
	Cancelled bool
	Known     bool
}

// waveStateOf reads a story's own governing route. Terminal-ness is the route's
// declaration, never a status literal.
func waveStateOf(workflows []docindex.Doc, it workitem.Item) waveState {
	spec, _, _, err := wfgovern.SpecFor(workflows, it)
	if err != nil {
		return waveState{Status: it.Status}
	}
	return waveState{
		Status:    it.Status,
		Done:      spec.IsTerminalState(it.Status),
		Cancelled: spec.IsParkState(it.Status) && !spec.IsResumePark(it.Status),
		Known:     true,
	}
}

// computeWave is the pure core: which children of a container may start now. A
// dependency is satisfied only when its story's own route says done — cancelled
// does not satisfy it. Parallel returns every non-terminal child whose
// dependencies are satisfied; sequential returns that set only when it holds
// exactly one id, and refuses otherwise rather than choosing one.
func computeWave(schedule string, children []workitem.Item, state func(id string) (waveState, bool)) (runnable []string, omitted []WaveOmitted, err error) {
	kids := append([]workitem.Item(nil), children...)
	sort.Slice(kids, func(i, j int) bool { return kids[i].ID < kids[j].ID })
	for _, c := range kids {
		own, ok := state(c.ID)
		if !ok || !own.Known {
			omitted = append(omitted, WaveOmitted{ID: c.ID, Reason: fmt.Sprintf("no route resolves for %s (status %s)", c.ID, c.Status)})
			continue
		}
		if own.Done || own.Cancelled {
			continue
		}
		var waiting []WaveBlocker
		for _, dep := range dependsOnTargets(c.Tags) {
			ds, ok := state(dep)
			if ok && ds.Known && ds.Done {
				continue
			}
			b := WaveBlocker{ID: dep, Status: "missing"}
			if ok {
				b.Status, b.Cancelled = ds.Status, ds.Cancelled
			}
			waiting = append(waiting, b)
		}
		if len(waiting) == 0 {
			runnable = append(runnable, c.ID)
			continue
		}
		parts := make([]string, 0, len(waiting))
		for _, b := range waiting {
			p := fmt.Sprintf("%s (%s)", b.ID, b.Status)
			if b.Cancelled {
				p = fmt.Sprintf("%s (cancelled — its commits are not there, so it does not satisfy the dependency)", b.ID)
			}
			parts = append(parts, p)
		}
		omitted = append(omitted, WaveOmitted{ID: c.ID, WaitingOn: waiting, Reason: "waiting on " + strings.Join(parts, ", ")})
	}
	if schedule == wfdot.SchedSequential && len(runnable) > 1 {
		return nil, omitted, errWaveTooWide{fmt.Sprintf("schedule is %s but %d children are runnable: %s — add %s edges so exactly one is eligible (a sequential wave never picks by order: or created_at)",
			wfdot.SchedSequential, len(runnable), strings.Join(runnable, ", "), DependsOnPrefix)}
	}
	return runnable, omitted, nil
}

// errWaveTooWide is the sequential refusal: more than one child is eligible, so
// the wave names none. Typed so engagement can tell it from a store failure.
type errWaveTooWide struct{ msg string }

func (e errWaveTooWide) Error() string { return e.msg }

// waveSchedule reads the child schedule the container's route declares: the one
// on the container's current step when it has one, else the single value any
// step declares. No declaration means the default, wfdot.DefaultSchedule
// (parallel); two different ones that the status does not select is a refusal —
// there is no fallback to order: or to every child.
func waveSchedule(spec wfdot.Spec, status string) (string, error) {
	if st, ok := spec.StateNamed(status); ok && st.Schedule != "" {
		return st.Schedule, nil
	}
	seen := map[string]bool{}
	var vals []string
	for _, st := range spec.States {
		if st.Schedule != "" && !seen[st.Schedule] {
			seen[st.Schedule] = true
			vals = append(vals, st.Schedule)
		}
	}
	switch len(vals) {
	case 0:
		return wfdot.DefaultSchedule(), nil
	case 1:
		return vals[0], nil
	}
	sort.Strings(vals)
	return "", errNoUsableSchedule{fmt.Sprintf("the container's route declares more than one schedule (%s) and its status %q selects none", strings.Join(vals, ", "), status)}
}

// ContainerSchedule is the child schedule an epic-parent's route declares, or ""
// when it declares no single one. Read-only mechanism for callers outside this
// package that must name the active schedule without deriving a wave.
func ContainerSchedule(wfs []docindex.Doc, container workitem.Item) string {
	spec, _, _, err := wfgovern.SpecFor(wfs, container)
	if err != nil {
		return ""
	}
	schedule, err := waveSchedule(spec, container.Status)
	if err != nil {
		return ""
	}
	return schedule
}

// EpicChildSchedule is the child schedule of the epic item is a child of: the
// schedule its container's route declares, found the way engagement finds the
// container (parent_id and epic:<theme>). It is "" for a story that is not an
// epic child, when a store or route lookup fails, when a container declares no
// single schedule, and when item's containers disagree — so a caller placing
// work by schedule never acts on a guess. Read-only mechanism.
func EpicChildSchedule(ctx context.Context, item workitem.Item) string {
	if item.ID == "" || item.Kind != workitem.KindStory || epicset.IsEpicParent(item) {
		return ""
	}
	store, err := requireWorkItem()
	if err != nil {
		return ""
	}
	idx, err := requireDocIndex()
	if err != nil {
		return ""
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return ""
	}
	schedule := ""
	for _, container := range waveContainersOf(ctx, store, item) {
		s := ContainerSchedule(wfs, container)
		if s == "" || (schedule != "" && s != schedule) {
			return ""
		}
		schedule = s
	}
	return schedule
}

// storyWave assesses an epic-parent's children. It reads the store and the
// workflow index and writes nothing: no status, no lease, no tag, no dispatch.
func storyWave(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req waveReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("story-wave: id required")
	}
	container, err := storyState(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if container.Kind != workitem.KindStory || !epicset.IsEpicParent(container) {
		return nil, fmt.Errorf("story-wave: %s is not an epic-parent (category %q) — a wave is the runnable children of an epic's container", container.ID, container.Category)
	}
	idx, err := requireDocIndex()
	if err != nil {
		return nil, err
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return nil, fmt.Errorf("story-wave: list workflows: %w", err)
	}
	res, err := assessWave(ctx, store, wfs, container)
	if err != nil {
		if errors.Is(err, epicset.ErrUndetermined) {
			return nil, fmt.Errorf("story-wave: %s: %v — no child is runnable", container.ID, err)
		}
		return nil, fmt.Errorf("story-wave: %s: %w", container.ID, err)
	}
	res.Delivery = waveDelivery(ctx, wfs, container, res.Schedule, res.children)
	return json.Marshal(res)
}

// errNoUsableSchedule marks a container whose route declares two different child
// schedules with its status selecting neither. story wave refuses it; engagement
// does not, so such an epic keeps its existing behaviour.
type errNoUsableSchedule struct{ msg string }

func (e errNoUsableSchedule) Error() string { return e.msg }

// assessWave is the read-only core shared by story wave and engagement: the
// wave of an epic-parent container. A schedule the route does not declare
// surfaces as errNoUsableSchedule; an unresolvable set wraps epicset.ErrUndetermined.
func assessWave(ctx context.Context, store *workitem.Store, wfs []docindex.Doc, container workitem.Item) (WaveResult, error) {
	// Every story the wave classifies — the container, each child and each
	// dependency — is read as its chain folds, not as its cached row says
	// (sty_d84be8bc).
	container, err := foldedItem(ctx, container)
	if err != nil {
		return WaveResult{}, err
	}
	set, err := epicset.Resolve(ctx, store, container)
	if err != nil {
		return WaveResult{}, err
	}
	children, err := overlayFold(ctx, set.Children)
	if err != nil {
		return WaveResult{}, err
	}
	spec, _, _, err := wfgovern.SpecFor(wfs, container)
	if err != nil {
		return WaveResult{}, fmt.Errorf("container route: %w", err)
	}
	schedule, err := waveSchedule(spec, container.Status)
	if err != nil {
		return WaveResult{}, err
	}

	cache := map[string]waveState{}
	known := map[string]bool{}
	for _, c := range children {
		cache[c.ID], known[c.ID] = waveStateOf(wfs, c), true
	}
	siblings := &siblingReader{}
	state := func(id string) (waveState, bool) {
		if repo, sid, cross := splitDependsOnTarget(id); cross {
			return siblings.state(ctx, repo, sid), true
		}
		if known[id] {
			return cache[id], true
		}
		it, gerr := store.Get(ctx, id)
		if gerr == nil {
			it, gerr = foldedItem(ctx, it)
		}
		if gerr != nil {
			return waveState{}, false
		}
		cache[id], known[id] = waveStateOf(wfs, it), true
		return cache[id], true
	}
	runnable, omitted, err := computeWave(schedule, children, state)
	if err != nil {
		// The omissions survive a refusal so engagement can quote a waiting
		// child's own reason; story wave discards the result on error.
		return WaveResult{Omitted: omitted}, err
	}
	if runnable == nil {
		runnable = []string{}
	}
	return WaveResult{Container: container.ID, Theme: set.Theme, Schedule: schedule, Runnable: runnable, Omitted: omitted, children: children}, nil
}

// waveDelivery describes how each non-terminal child of container will be
// delivered. It reads, and writes nothing. The placement of a child is its
// delivering step's StepPerformer answer; with no resolver wired the placement
// fields stay empty.
func waveDelivery(ctx context.Context, wfs []docindex.Doc, container workitem.Item, schedule string, children []workitem.Item) []WaveDelivery {
	var laneTag string
	var reasons map[string]string
	edge, err := readinessEdgeFor(ctx, container)
	if err == nil {
		laneTag = edge.Step.LaneTag
		reasons = deliveryReasons(ctx, container, edge)
	}
	resolver, _ := executorDispatcher.(PerformerResolver)
	kids := append([]workitem.Item(nil), children...)
	sort.Slice(kids, func(i, j int) bool { return kids[i].ID < kids[j].ID })
	var out []WaveDelivery
	for _, c := range kids {
		if own := waveStateOf(wfs, c); own.Done || own.Cancelled {
			continue
		}
		d := WaveDelivery{ID: c.ID, Order: waveOrder(schedule, c), Reason: reasons[c.ID]}
		if laneTag != "" {
			d.Lane = tagValue(c.Tags, laneTag)
		}
		if resolver != nil {
			resolveDelivery(ctx, resolver, wfs, c, &d)
		}
		out = append(out, d)
	}
	return out
}

// waveOrder says how a child is sequenced: after its depends-on targets, else by
// the container's schedule.
func waveOrder(schedule string, c workitem.Item) string {
	if deps := dependsOnTargets(c.Tags); len(deps) > 0 {
		return "after " + strings.Join(deps, ", ")
	}
	if schedule == wfdot.SchedSequential {
		return wfdot.SchedSequential
	}
	return wfdot.SchedParallel
}

// tagValue is the text after prefix on the first tag of tags that starts with it.
func tagValue(tags []string, prefix string) string {
	for _, t := range tags {
		if v, ok := strings.CutPrefix(t, prefix); ok {
			return v
		}
	}
	return ""
}

// resolveDelivery fills d's step and placement fields from the engine's
// performer rule, over c's route from its current status on.
func resolveDelivery(ctx context.Context, resolver PerformerResolver, wfs []docindex.Doc, c workitem.Item, d *WaveDelivery) {
	spec, _, _, err := wfgovern.SpecFor(wfs, c)
	if err != nil {
		d.Error = err.Error()
		return
	}
	steps := spec.SpineSteps()
	if at := slices.Index(steps, c.Status); at > 0 {
		steps = steps[at:]
	}
	var chosen *PerformerResolution
	step := ""
	for _, name := range steps {
		perf, perr := resolver.StepPerformer(ctx, c, name)
		if perr != nil {
			d.Step, d.Error = name, perr.Error()
			return
		}
		if perf.Kind != PerformerDispatched && perf.Kind != PerformerInLoop && perf.Kind != PerformerExecutor {
			continue
		}
		if chosen == nil {
			chosen, step = &perf, name
		}
		if st, ok := spec.StateNamed(name); ok && st.PlacementTag != "" {
			chosen, step = &perf, name
			break
		}
	}
	if chosen == nil {
		d.Placement = "none"
		return
	}
	d.Step, d.Placement, d.Agent, d.Note = step, renderPlacement(*chosen), chosen.Agent, chosen.PlacementNote
}

// renderPlacement is the one rendering of an engine resolution for display:
// "team:<path>" for a team remote, "remote" for a remote performer, else the
// Kind itself, with an executor step saying whether the [executor] binding is
// the driving session or headless. No Kind is mapped to another.
func renderPlacement(p PerformerResolution) string {
	switch {
	case p.Team != "":
		return "team:" + p.Team
	case p.Remote:
		return "remote"
	case p.Kind == PerformerExecutor && p.ExecutorInLoop:
		return string(p.Kind) + " (in-loop)"
	case p.Kind == PerformerExecutor:
		return string(p.Kind) + " (headless)"
	}
	return string(p.Kind)
}

// deliveryReasons reads the orchestrator's per-child reasons the readiness round
// recorded: one "<child id>: <reason>" line per child under the readiness step's
// delivery_heading in its performer's document. An absent heading, document or
// line gives no reason and no error — the round may not have run.
func deliveryReasons(ctx context.Context, container workitem.Item, edge readinessEdge) map[string]string {
	heading := edge.Step.DeliveryHeading
	if heading == "" {
		return nil
	}
	doc := readinessDocName(ctx, container, edge.To)
	file, dir := safeName(doc), attachmentDir(container)
	if doc == "" || file == "" || dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return nil
	}
	reasons := map[string]string{}
	for _, line := range strings.Split(agentartifact.Section(string(data), heading), "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "-*• \t")
		if id, reason, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(reason) != "" {
			reasons[strings.TrimSpace(id)] = strings.TrimSpace(reason)
		}
	}
	return reasons
}
