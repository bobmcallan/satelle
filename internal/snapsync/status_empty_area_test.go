package snapsync

import (
	"context"
	"reflect"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// Tests for sty_09b6b0e4: an opted-in area with nothing local to push is never
// unpushed or never synced, because a push cannot change it. Only a hosted copy
// can make it differ, and then it is behind.

func statusOf(t *testing.T, d *Driver, local map[string]string) AreaStatus {
	t.Helper()
	st, err := d.Status(context.Background(), "skills", local)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func saveBase(t *testing.T, d *Driver, files map[string]string) {
	t.Helper()
	b := hosted.AreaBase{Version: 1, Files: map[string]string{}}
	for p, body := range files {
		b.Files[p] = SHA([]byte(body))
	}
	if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, "skills", b); err != nil {
		t.Fatal(err)
	}
}

func TestStatusEmptyLocalAreaNeverSuggestsAPush(t *testing.T) {
	const f = "skills/a.md"
	cases := []struct {
		name    string
		hosted  func(h *memHosted)
		prune   bool
		base    map[string]string // the base this checkout recorded, if any
		want    string
		pending []string
	}{
		{name: "nothing hosted", want: StateInStep},
		{name: "heads with no snapshot", hosted: func(h *memHosted) { h.blobs[f] = []byte("a\n") }, want: StateBehind, pending: []string{f}},
		{name: "heads with no snapshot under prune", prune: true, hosted: func(h *memHosted) { h.blobs[f] = []byte("a\n") }, want: StateBehind, pending: []string{f}},
		{name: "effective snapshot", hosted: func(h *memHosted) { h.snapshot(map[string]string{f: "a\n"}) }, want: StateBehind, pending: []string{f}},
		{name: "base with files and the snapshot unchanged", hosted: func(h *memHosted) { h.snapshot(map[string]string{f: "a\n"}) }, base: map[string]string{f: "a\n"}, want: StateBehind, pending: []string{f}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newMemHosted()
			if c.hosted != nil {
				c.hosted(h)
			}
			d := testDriver(t, h, nil)
			d.Prune = c.prune
			if c.base != nil {
				saveBase(t, d, c.base)
			}
			st := statusOf(t, d, map[string]string{})
			if st.State != c.want {
				t.Fatalf("state = %q, want %q", st.State, c.want)
			}
			if st.State == StateUnpushed || st.State == StateNeverSynced {
				t.Errorf("an area with nothing to push must not read %q, whose NEXT is a push", st.State)
			}
			// The refusal path (a snapshot this checkout never synced) names no
			// pending paths; every other row names the hosted paths.
			if c.name != "effective snapshot" && !reflect.DeepEqual(st.Pending, c.pending) {
				t.Errorf("pending = %v, want %v", st.Pending, c.pending)
			}
		})
	}
}

// A nil local map is the same empty area.
func TestStatusNilLocalIsEmpty(t *testing.T) {
	if st := statusOf(t, testDriver(t, newMemHosted(), nil), nil); st.State != StateInStep {
		t.Errorf("state = %q, want %q", st.State, StateInStep)
	}
}

// With a base and nothing local, a pull leaves the files deleted (the deletion
// is "pending push", which an empty area cannot publish). The area still reads
// behind: the hosted copy holds files this tree lacks.
func TestStatusEmptyLocalWithBaseRecordsWhatPullDoes(t *testing.T) {
	const f = "skills/a.md"
	sha := SHA([]byte("a\n"))
	rec := Record{Files: map[string]string{f: sha}}
	base := &hosted.AreaBase{Version: 1, Files: map[string]string{f: sha}}

	withBase := PlanPull(base, rec, 1, nil, map[string]string{f: sha})
	if len(withBase.Entries) != 1 || withBase.Entries[0].Action != LocalDeleted {
		t.Fatalf("pull with a base and nothing local = %+v, want one LocalDeleted", withBase.Entries)
	}
	noBase := PlanPull(nil, rec, 1, nil, map[string]string{f: sha})
	if len(noBase.Entries) != 1 || noBase.Entries[0].Action != Write {
		t.Fatalf("pull with no base and nothing local = %+v, want one Write", noBase.Entries)
	}
}

// An area that gains authored content is unpushed until pushed (AC3).
func TestStatusAuthoredContentIsNeverSyncedThenUnpushedThenInStep(t *testing.T) {
	const f = "skills/a.md"
	h := newMemHosted()
	d := testDriver(t, h, nil)
	local := map[string]string{f: SHA([]byte("a\n"))}

	if st := statusOf(t, d, local); st.State != StateNeverSynced {
		t.Fatalf("authored, never pushed: state = %q, want %q", st.State, StateNeverSynced)
	}

	h.snapshot(map[string]string{f: "a\n"})
	saveBase(t, d, map[string]string{f: "a\n"})
	d.heads = nil // the push put the blob; re-read the heads
	if st := statusOf(t, d, local); st.State != StateInStep {
		t.Fatalf("pushed: state = %q, want %q", st.State, StateInStep)
	}

	edited := map[string]string{f: SHA([]byte("edited\n"))}
	if st := statusOf(t, d, edited); st.State != StateUnpushed {
		t.Fatalf("edited after the push: state = %q, want %q", st.State, StateUnpushed)
	}
}
