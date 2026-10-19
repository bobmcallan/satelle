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
// handling). Source is the surface the fire came through and Event what caused
// it, if anything; both are carried as the server sent them and never compared
// to a value here. The tags are the server's wire names.
type WakeRequest struct {
	TriggerID string     `json:"trigger_id"`
	RemoteID  string     `json:"remote_id"`
	Remote    string     `json:"remote"`
	StoryID   string     `json:"story_id,omitempty"`
	Trigger   string     `json:"trigger,omitempty"`
	Source    string     `json:"source,omitempty"`
	Event     *WakeEvent `json:"event,omitempty"`
}

// WakeEvent is the event a fire carries (null when it has none). Its fields are
// opaque strings, passed on to the run unchanged.
type WakeEvent struct {
	Kind   string `json:"kind"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
}

// Handoff is one story a wake handed to the remote: the remote, the declared
// trigger and its kind (both empty for an unnamed fire), the fire-record id and
// the story, with the fire's source and the event's fields as the server sent
// them (empty when it sent none).
type Handoff struct {
	Remote    string
	Trigger   string
	Kind      string
	FireID    string
	Story     string
	Source    string
	EventKind string
	Repo      string
	Branch    string
	SHA       string
}

// StoryReader is the stories a wake selects from.
type StoryReader interface {
	// Status is the story's current status, or an error when it has none.
	Status(ctx context.Context, id string) (string, error)
	// IDsAt lists the ids of the stories at status, in id order.
	IDsAt(ctx context.Context, status string) ([]string, error)
}

// HandoffRecorder records a hand-off, reporting whether it was new: a hand-off
// already recorded for the same fire and story is not recorded again.
type HandoffRecorder interface {
	Record(ctx context.Context, h Handoff) (bool, error)
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

// AcceptsEntry reports whether status is a segment entry status of this remote.
func (d *Definition) AcceptsEntry(status string) bool {
	for _, e := range d.EntryStatuses() {
		if e == status {
			return true
		}
	}
	return false
}

// Wake handles one delivered WakeRequest for the remote called name and returns
// the hand-offs it newly recorded. A named trigger must be declared by this
// definition — matched on its name, never on the fire-record id; an unnamed wake
// is the remote's default handling. A named story is handed off only while at an
// entry status; otherwise the wake is a sweep, handing off every story at one. The
// request's event never selects a story: it rides on each hand-off. A
// wake that is refused records nothing, and a redelivered wake adds nothing.
func Wake(ctx context.Context, name string, def *Definition, req WakeRequest, stories StoryReader, rec HandoffRecorder) ([]Handoff, error) {
	remote := strings.TrimSpace(req.Remote)
	if remote == "" {
		remote = name
	}
	if strings.TrimSpace(req.TriggerID) == "" {
		return nil, fmt.Errorf("wake request for remote %s carries no trigger_id (the fire-record id)", remote)
	}
	h := Handoff{Remote: remote, FireID: req.TriggerID, Source: req.Source}
	if e := req.Event; e != nil {
		h.EventKind, h.Repo, h.Branch, h.SHA = e.Kind, e.Repo, e.Branch, e.SHA
	}
	if req.Trigger != "" {
		found := false
		for _, t := range def.Triggers {
			if t.Name == req.Trigger {
				h.Trigger, h.Kind, found = t.Name, t.Kind, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown trigger %s for remote %s", req.Trigger, remote)
		}
	}
	entries := def.EntryStatuses()

	var ids []string
	if req.StoryID != "" {
		status, err := stories.Status(ctx, req.StoryID)
		if err != nil {
			return nil, err
		}
		if !def.AcceptsEntry(status) {
			return nil, fmt.Errorf("story %s is at %s; remote %s accepts %s", req.StoryID, status, remote, strings.Join(entries, ", "))
		}
		ids = []string{req.StoryID}
	} else {
		for _, e := range entries {
			at, err := stories.IDsAt(ctx, e)
			if err != nil {
				return nil, err
			}
			ids = append(ids, at...)
		}
		sort.Strings(ids)
	}

	var out []Handoff
	for _, id := range ids {
		h.Story = id
		fresh, err := rec.Record(ctx, h)
		if err != nil {
			return out, err
		}
		if fresh {
			out = append(out, h)
		}
	}
	return out, nil
}
