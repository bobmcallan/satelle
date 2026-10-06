package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

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

// WaveResult is the story-wave response.
type WaveResult struct {
	Container string        `json:"container"`
	Theme     string        `json:"theme"`
	Schedule  string        `json:"schedule"`
	Runnable  []string      `json:"runnable"`
	Omitted   []WaveOmitted `json:"omitted,omitempty"`
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
// step declares. No declaration, or two different ones, is a refusal — there is
// no default and no fallback to order: or to every child.
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
		return "", errNoUsableSchedule{fmt.Sprintf("no schedule is declared on the container's route (set schedule = %q or %q on its waits_on_children step in step.toml)", wfdot.SchedParallel, wfdot.SchedSequential)}
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
	container, err := store.Get(ctx, req.ID)
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
	return json.Marshal(res)
}

// errNoUsableSchedule marks a container whose route declares no single child
// schedule (none, or two with its status selecting neither). story wave refuses
// it; engagement does not, so an unscheduled epic keeps its existing behaviour.
type errNoUsableSchedule struct{ msg string }

func (e errNoUsableSchedule) Error() string { return e.msg }

// assessWave is the read-only core shared by story wave and engagement: the
// wave of an epic-parent container. A schedule the route does not declare
// surfaces as errNoUsableSchedule; an unresolvable set wraps epicset.ErrUndetermined.
func assessWave(ctx context.Context, store *workitem.Store, wfs []docindex.Doc, container workitem.Item) (WaveResult, error) {
	set, err := epicset.Resolve(ctx, store, container)
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
	for _, c := range set.Children {
		cache[c.ID], known[c.ID] = waveStateOf(wfs, c), true
	}
	state := func(id string) (waveState, bool) {
		if known[id] {
			return cache[id], true
		}
		it, gerr := store.Get(ctx, id)
		if gerr != nil {
			return waveState{}, false
		}
		cache[id], known[id] = waveStateOf(wfs, it), true
		return cache[id], true
	}
	runnable, omitted, err := computeWave(schedule, set.Children, state)
	if err != nil {
		// The omissions survive a refusal so engagement can quote a waiting
		// child's own reason; story wave discards the result on error.
		return WaveResult{Omitted: omitted}, err
	}
	if runnable == nil {
		runnable = []string{}
	}
	return WaveResult{Container: container.ID, Theme: set.Theme, Schedule: schedule, Runnable: runnable, Omitted: omitted}, nil
}
