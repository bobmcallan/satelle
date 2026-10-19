package remotedef

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// WakeRequest is what the server delivers to a remote's wake endpoint when a
// trigger fires: the fire-record id, the remote, the story the fire named (none
// starts a sweep) and the declared trigger's name (none is the remote's default
// handling). The tags are the server's wire names.
type WakeRequest struct {
	TriggerID string `json:"trigger_id"`
	RemoteID  string `json:"remote_id"`
	Remote    string `json:"remote"`
	StoryID   string `json:"story_id,omitempty"`
	Trigger   string `json:"trigger,omitempty"`
}

// Handoff is one story a wake handed to the remote: the remote, the declared
// trigger and its kind (both empty for an unnamed fire), the fire-record id and
// the story, the step it is handed over at (its status at hand-off) and the id of
// the run (empty until the wake mints one for a run).
type Handoff struct {
	Remote  string
	Trigger string
	Kind    string
	FireID  string
	Story   string
	Step    string
	RunID   string
}

// RunRecord is one step run on the remote's host, as recorded on the story: where
// (remote, pinned version, host name) and as whom (identity label) it ran, the
// step and run id, and when. It has no env, argv or credential field, so none can
// reach the ledger through it. Error is the runner's, empty on success.
type RunRecord struct {
	Handoff
	Version string
	Host    string
	Label   string
	Started time.Time
	Ended   time.Time
	Error   string
}

// Handback is a story the remote left somewhere the contract or the local
// workflow does not allow: the status it was found at, which of the two checks it
// passed, and why it failed. Only a failing hand-back is a Handback.
type Handback struct {
	Remote string
	FireID string
	Story  string
	Status string
	// InContract is whether Status is one of the contract's exit statuses.
	InContract bool
	// LocalKnown is whether the story's governing local route declares Status.
	LocalKnown bool
	// HoldsSeat is whether Status is an engaging state of the story's local route,
	// one that would hold an engagement seat there.
	HoldsSeat bool
	// Refused marks a story that was never handed off: the wake refused it before
	// the remote touched it, so Status is where the story still is, not where the
	// remote left it.
	Refused bool
	// Reason names which check failed.
	Reason string
}

// Report is the line a failing hand-back is reported as. A refused story reads
// as refused before hand-off, never as a status the remote left it at.
func (b Handback) Report() string {
	if b.Refused {
		return fmt.Sprintf("story %s %s", b.Story, b.Reason)
	}
	return fmt.Sprintf("story %s left at %s: %s", b.Story, b.Status, b.Reason)
}

// StoryRef is a story as a wake sees it: its id, status and category.
type StoryRef struct {
	ID       string
	Status   string
	Category string
}

// StoryReader is the stories a wake selects from and re-reads.
type StoryReader interface {
	// Story is the story's current state, or an error when it has none.
	Story(ctx context.Context, id string) (StoryRef, error)
	// At lists the stories at status, in id order.
	At(ctx context.Context, status string) ([]StoryRef, error)
}

// HandoffRecorder records what a wake did. A hand-off already recorded for the
// same fire and story is not recorded again.
type HandoffRecorder interface {
	// Record reports whether the hand-off was new.
	Record(ctx context.Context, h Handoff) (bool, error)
	// RecordHandback records a story left outside the contract or the local route.
	RecordHandback(ctx context.Context, b Handback) error
	// RecordRun records one step run, whether it succeeded or failed.
	RecordRun(ctx context.Context, r RunRecord) error
}

// Runner works one handed-off story on the host. command is the definition's
// host.story_command rendered for that story. This package runs nothing; the real
// Runner is remotenode.StoryRunner.
type Runner interface {
	Run(ctx context.Context, h Handoff, command string) error
}

// LocalRoute answers for the local workflow that governs a story.
type LocalRoute interface {
	// Declares reports whether the route governing story id declares status, and
	// the route's name for the report.
	Declares(ctx context.Context, id, status string) (declared bool, route string, err error)
	// Engages reports whether status is a non-terminal engaging state of the
	// route governing story id: a story at it holds an engagement seat. It is the
	// same classification the engagement seat uses, not a list of this package's.
	Engages(ctx context.Context, id, status string) (engages bool, route string, err error)
}

// refusedBeforeHandoff opens the reason of a story a wake refused to hand off.
const refusedBeforeHandoff = "refused before hand-off: "

// SeatReason is why handing story id off through seg would leave it holding an
// engagement seat on the local workflow, or "" when it would not: the story's
// current status (the entry) is checked first, then the segment's exit. A story
// resting at a status that holds no seat is what lets any number of stories wait
// there; one parked at an engaging status blocks every other. With no local
// route there is nothing to check.
func SeatReason(ctx context.Context, local LocalRoute, id string, seg Segment, status string) (string, error) {
	if local == nil {
		return "", nil
	}
	for _, s := range []string{status, seg.Exit} {
		engages, route, err := local.Engages(ctx, id, s)
		if err != nil {
			return "", err
		}
		if engages {
			return seatMessage(s, route), nil
		}
	}
	return "", nil
}

func seatMessage(status, route string) string {
	return fmt.Sprintf("status %s is an engaging state of the local %s route and would hold an engagement seat; hand off at a status that holds none", status, route)
}

// Wiring is what a wake needs beyond the definition. Runner nil makes a wake
// record-only: stories are handed off and nothing is run or checked. Local nil
// skips the local-route check. Now nil is time.Now.
type Wiring struct {
	Stories  StoryReader
	Recorder HandoffRecorder
	Runner   Runner
	Local    LocalRoute
	// AfterRun, when set, is called with the story's id after Runner.Run and
	// before the hand-back check, so a caller whose story is changed on the host
	// can bring that state home first. Its error fails the story as a run error
	// does. Nil does nothing.
	AfterRun func(ctx context.Context, storyID string) error
	Now      func() time.Time
}

// RefusedError is a wake that named a story the contract does not accept. It is
// the only refusal a caller may carry on past; nothing was recorded or run.
type RefusedError struct {
	Remote string
	Story  string
	Cause  error
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("remote %s: story %s %v", e.Remote, e.Story, e.Cause)
}

func (e *RefusedError) Unwrap() error { return e.Cause }

// StoryPlaceholder stands for a story's id in host.story_command.
const StoryPlaceholder = "{story}"

// RenderStoryCommand is host.story_command with the story's id in place of
// StoryPlaceholder. Only that placeholder is resolved; {command} and {label}
// belong to the run command that takes this string.
func RenderStoryCommand(def *Definition, storyID string) string {
	return strings.ReplaceAll(def.Host.StoryCommand, StoryPlaceholder, storyID)
}

// EntryStatuses is the union of the segments' entry statuses, sorted: the
// statuses at which a story may be handed to this remote.
func (d *Definition) EntryStatuses() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range d.Segments {
		if e := strings.TrimSpace(s.Entry); e != "" && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// Contract is the segments' entry side as prose: each entry status with the
// categories its segment accepts.
func (d *Definition) Contract() string {
	parts := make([]string, 0, len(d.Segments))
	for _, s := range d.Segments {
		cats := "any category"
		if len(s.Categories) > 0 {
			cats = "category " + strings.Join(s.Categories, ", ")
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", s.Entry, cats))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// Accepts is the segment that takes a story at status with category, or an error
// naming the status, the category and the contract it missed. It is the one
// owner of the entry rule: a wake asks it, and holds no rule of its own.
func (d *Definition) Accepts(status, category string) (Segment, error) {
	for _, s := range d.Segments {
		if s.Entry != status {
			continue
		}
		if len(s.Categories) == 0 {
			return s, nil
		}
		for _, c := range s.Categories {
			if c == category {
				return s, nil
			}
		}
	}
	return Segment{}, fmt.Errorf("is at %s with category %q, which no segment accepts (accepts %s)", status, category, d.Contract())
}

// ExitStatuses is the union of the segments' exit statuses, sorted: the statuses
// at which the remote may leave a story.
func (d *Definition) ExitStatuses() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range d.Segments {
		if e := strings.TrimSpace(s.Exit); e != "" && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// ReturnsAt is nil when status is one of the contract's exit statuses, else an
// error naming them. It is the one owner of the exit rule.
func (d *Definition) ReturnsAt(status string) error {
	for _, e := range d.ExitStatuses() {
		if e == status {
			return nil
		}
	}
	return fmt.Errorf("outside the contract's exit statuses (%s)", strings.Join(d.ExitStatuses(), ", "))
}

// Wake handles one delivered WakeRequest for the remote called name and returns
// the hand-offs it newly recorded, and the stories it found left where the
// contract or the local workflow does not allow. A named trigger must be declared
// by this definition — matched on its name, never on the fire-record id; an
// unnamed wake is the remote's default handling. A named story is handed off only
// when the contract accepts its status and category; otherwise the wake is a
// sweep, handing off exactly the stories the contract accepts. A wake that is
// refused records nothing, and a redelivered wake adds nothing.
//
// No story is handed off whose entry status, or whose segment's exit, would hold
// an engagement seat on the local workflow (SeatReason). A named story is refused
// with an error; a sweep skips it and reports and records it as a Handback marked
// Refused, and still hands off the rest.
//
// With a Runner, each new hand-off is then run once, in id order, with the
// rendered host.story_command; AfterRun, when wired, then runs; the story is
// re-read afterwards and checked
// against the contract's exit statuses and against the local route. A failed
// check is reported and recorded and does not stop the sweep; a run error does.
// The wake returns once the last story is checked: it never polls.
func Wake(ctx context.Context, name string, def *Definition, req WakeRequest, w Wiring) ([]Handoff, []Handback, error) {
	remote := strings.TrimSpace(req.Remote)
	if remote == "" {
		remote = name
	}
	if strings.TrimSpace(req.TriggerID) == "" {
		return nil, nil, fmt.Errorf("wake request for remote %s carries no trigger_id (the fire-record id)", remote)
	}
	h := Handoff{Remote: remote, FireID: req.TriggerID}
	if req.Trigger != "" {
		found := false
		for _, t := range def.Triggers {
			if t.Name == req.Trigger {
				h.Trigger, h.Kind, found = t.Name, t.Kind, true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("unknown trigger %s for remote %s", req.Trigger, remote)
		}
	}

	var picked []StoryRef
	var refused []Handback
	if req.StoryID != "" {
		st, err := w.Stories.Story(ctx, req.StoryID)
		if err != nil {
			return nil, nil, err
		}
		seg, err := def.Accepts(st.Status, st.Category)
		if err != nil {
			return nil, nil, &RefusedError{Remote: remote, Story: st.ID, Cause: err}
		}
		reason, err := SeatReason(ctx, w.Local, st.ID, seg, st.Status)
		if err != nil {
			return nil, nil, err
		}
		if reason != "" {
			return nil, nil, fmt.Errorf("remote %s: story %s %s", remote, st.ID, reason)
		}
		picked = []StoryRef{st}
	} else {
		for _, e := range def.EntryStatuses() {
			at, err := w.Stories.At(ctx, e)
			if err != nil {
				return nil, nil, err
			}
			for _, st := range at {
				seg, err := def.Accepts(st.Status, st.Category)
				if err != nil {
					continue
				}
				reason, err := SeatReason(ctx, w.Local, st.ID, seg, st.Status)
				if err != nil {
					return nil, nil, err
				}
				if reason != "" {
					refused = append(refused, Handback{Remote: remote, FireID: req.TriggerID, Story: st.ID, Status: st.Status,
						InContract: true, LocalKnown: true, HoldsSeat: true, Refused: true, Reason: refusedBeforeHandoff + reason})
					continue
				}
				picked = append(picked, st)
			}
		}
		sort.Slice(picked, func(i, j int) bool { return picked[i].ID < picked[j].ID })
		sort.Slice(refused, func(i, j int) bool { return refused[i].Story < refused[j].Story })
	}

	now := w.Now
	if now == nil {
		now = time.Now
	}
	var out []Handoff
	var backs []Handback
	for _, b := range refused {
		if err := w.Recorder.RecordHandback(ctx, b); err != nil {
			return out, backs, err
		}
		backs = append(backs, b)
	}
	for _, st := range picked {
		id := st.ID
		h.Story, h.Step, h.RunID = id, st.Status, ""
		fresh, err := w.Recorder.Record(ctx, h)
		if err != nil {
			return out, backs, err
		}
		if !fresh {
			continue
		}
		out = append(out, h)
		if w.Runner == nil {
			continue
		}
		h.RunID = ledger.NewRunID()
		rec := RunRecord{Handoff: h, Version: def.SatelleVersion, Host: def.Host.Name, Label: def.Identity.Label, Started: now()}
		runErr := w.Runner.Run(ctx, h, RenderStoryCommand(def, id))
		rec.Ended = now()
		if runErr != nil {
			rec.Error = runErr.Error()
		}
		if err := w.Recorder.RecordRun(ctx, rec); err != nil {
			return out, backs, err
		}
		if runErr != nil {
			return out, backs, fmt.Errorf("running story %s on remote %s: %w", id, remote, runErr)
		}
		if w.AfterRun != nil {
			if err := w.AfterRun(ctx, id); err != nil {
				return out, backs, fmt.Errorf("after running story %s on remote %s: %w", id, remote, err)
			}
		}
		b, failed, err := checkHandback(ctx, def, h, w)
		if err != nil {
			return out, backs, err
		}
		if failed {
			if err := w.Recorder.RecordHandback(ctx, b); err != nil {
				return out, backs, err
			}
			backs = append(backs, b)
		}
	}
	return out, backs, nil
}

// checkHandback re-reads the story the remote just worked and holds its actual
// status to the contract's exits, to the story's local route, and to the rule
// that it must not rest at a status that holds an engagement seat. failed is
// false when it passes all three.
func checkHandback(ctx context.Context, def *Definition, h Handoff, w Wiring) (b Handback, failed bool, err error) {
	st, err := w.Stories.Story(ctx, h.Story)
	if err != nil {
		return Handback{}, false, err
	}
	b = Handback{Remote: h.Remote, FireID: h.FireID, Story: h.Story, Status: st.Status, InContract: true, LocalKnown: true}
	var reasons []string
	if cerr := def.ReturnsAt(st.Status); cerr != nil {
		b.InContract = false
		reasons = append(reasons, cerr.Error())
	}
	if w.Local != nil {
		declared, route, lerr := w.Local.Declares(ctx, h.Story, st.Status)
		if lerr != nil {
			return Handback{}, false, lerr
		}
		if !declared {
			b.LocalKnown = false
			reasons = append(reasons, fmt.Sprintf("not a status of the local %s route", route))
		}
		engages, route, lerr := w.Local.Engages(ctx, h.Story, st.Status)
		if lerr != nil {
			return Handback{}, false, lerr
		}
		if engages {
			b.HoldsSeat = true
			reasons = append(reasons, seatMessage(st.Status, route))
		}
	}
	b.Reason = strings.Join(reasons, "; ")
	return b, len(reasons) > 0, nil
}
