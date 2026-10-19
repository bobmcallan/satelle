package remotedef

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
// the story.
type Handoff struct {
	Remote  string
	Trigger string
	Kind    string
	FireID  string
	Story   string
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
	// Reason names which check failed.
	Reason string
}

// Report is the line a failing hand-back is reported as.
func (b Handback) Report() string {
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
}

// Wiring is what a wake needs beyond the definition. Runner nil makes a wake
// record-only: stories are handed off and nothing is run or checked. Local nil
// skips the local-route check.
type Wiring struct {
	Stories  StoryReader
	Recorder HandoffRecorder
	Runner   Runner
	Local    LocalRoute
}

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
// With a Runner, each new hand-off is then run once, in id order, with the
// rendered host.story_command; the story is re-read afterwards and checked
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

	var ids []string
	if req.StoryID != "" {
		st, err := w.Stories.Story(ctx, req.StoryID)
		if err != nil {
			return nil, nil, err
		}
		if _, err := def.Accepts(st.Status, st.Category); err != nil {
			return nil, nil, fmt.Errorf("remote %s: story %s %w", remote, st.ID, err)
		}
		ids = []string{req.StoryID}
	} else {
		for _, e := range def.EntryStatuses() {
			at, err := w.Stories.At(ctx, e)
			if err != nil {
				return nil, nil, err
			}
			for _, st := range at {
				if _, err := def.Accepts(st.Status, st.Category); err == nil {
					ids = append(ids, st.ID)
				}
			}
		}
		sort.Strings(ids)
	}

	var out []Handoff
	var backs []Handback
	for _, id := range ids {
		h.Story = id
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
		if err := w.Runner.Run(ctx, h, RenderStoryCommand(def, id)); err != nil {
			return out, backs, fmt.Errorf("running story %s on remote %s: %w", id, remote, err)
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
// status to the contract's exits and to the story's local route. failed is false
// when it passes both.
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
	}
	b.Reason = strings.Join(reasons, "; ")
	return b, len(reasons) > 0, nil
}
