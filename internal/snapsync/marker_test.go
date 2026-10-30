package snapsync

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// Tests for sty_1481ac31 AC4: a base marker no push or pull recorded, naming the
// hosted snapshot while its files differ from that snapshot's, is diverged — never
// in step, never behind — and the bases the driver records itself keep their
// states.

const markerPath = "skills/a.md"

// movedMarker is a hosted snapshot 1 holding "hosted" and a checkout whose files
// and base both say "local" at version 1, recorded with the given seal.
func movedMarker(t *testing.T, seal func(b hosted.AreaBase) string) (*Driver, map[string]string) {
	t.Helper()
	h := newMemHosted()
	h.snapshot(map[string]string{markerPath: "hosted\n"})
	d := testDriver(t, h, nil)
	d.PullCmd, d.DivergedCmd = "pull", "reconcile"
	local := map[string]string{markerPath: SHA([]byte("local\n"))}
	b := hosted.AreaBase{Version: 1, Files: local}
	b.Seal = seal(b)
	if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, "skills", b); err != nil {
		t.Fatal(err)
	}
	return d, local
}

func TestStatusUnrecordedBaseIsDiverged(t *testing.T) {
	for _, c := range []struct {
		name string
		seal func(b hosted.AreaBase) string
	}{
		{"no seal", func(hosted.AreaBase) string { return "" }},
		{"a seal over other content", func(b hosted.AreaBase) string {
			b.Files = map[string]string{markerPath: SHA([]byte("hosted\n"))}
			return sealBase("skills", b)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, local := movedMarker(t, c.seal)
			st := statusOf(t, d, local)
			if st.State != StateDiverged || !reflect.DeepEqual(st.Pending, []string{markerPath}) {
				t.Fatalf("status = %q pending %v, want %q naming %s", st.State, st.Pending, StateDiverged, markerPath)
			}
			_, err := d.PreparePush(context.Background(), "skills", []PushFile{{Path: markerPath, Content: []byte("local\n")}})
			var behind *ErrBehind
			if !errors.As(err, &behind) || !behind.Diverged || behind.PullCmd != d.DivergedCmd || !strings.Contains(err.Error(), markerPath) {
				t.Errorf("push = %v, want the diverged refusal naming %s", err, markerPath)
			}
		})
	}
}

func TestStatusSealedMarkerTakesTheUsualPath(t *testing.T) {
	d, local := movedMarker(t, func(b hosted.AreaBase) string { return sealBase("skills", b) })
	if st := statusOf(t, d, local); st.State != StateBehind {
		t.Fatalf("a base the driver recorded = %q, want %q", st.State, StateBehind)
	}
}

// A base the driver records at the snapshot's version without one of its files —
// a first claim carrying a hosted file forward — is behind, not diverged.
func TestStatusDriverBaseShortOfTheSnapshotIsBehind(t *testing.T) {
	h := newMemHosted()
	h.snapshot(map[string]string{"skills/a.md": "a\n", "skills/b.md": "b\n"})
	d := testDriver(t, h, nil)
	local := map[string]string{"skills/a.md": SHA([]byte("a\n"))}
	if err := d.saveBase("skills", hosted.AreaBase{Version: 1, Files: local}); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(t, d, local); st.State != StateBehind {
		t.Fatalf("state = %q, want %q", st.State, StateBehind)
	}
}

// pullAndCommit plans, stages and commits a pull of skills over local, failing
// the given writes.
func pullAndCommit(t *testing.T, d *Driver, local map[string]string, failed map[string]bool) {
	t.Helper()
	job, err := d.PlanPull(context.Background(), "skills", local)
	if err != nil || job == nil {
		t.Fatalf("PlanPull: %v %v", job, err)
	}
	if _, err := d.Stage(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := d.Commit(job, failed); err != nil {
		t.Fatal(err)
	}
}

func TestStatusAfterAnIncompletePullIsBehind(t *testing.T) {
	h := newMemHosted()
	h.snapshot(map[string]string{"skills/a.md": "a\n"})
	d := testDriver(t, h, nil)
	local := map[string]string{"skills/a.md": SHA([]byte("a\n"))}
	if err := d.saveBase("skills", hosted.AreaBase{Version: 1, Files: local}); err != nil {
		t.Fatal(err)
	}
	h.snapshot(map[string]string{"skills/a.md": "a2\n"})
	delete(h.blobs, "skills/a.md") // the upload is still in flight
	pullAndCommit(t, d, local, nil)
	if b, _, _ := hosted.LoadAreaBase(d.Server, d.Project, d.RepoRoot, "skills"); b.Version != 2 || b.Files["skills/a.md"] != local["skills/a.md"] {
		t.Fatalf("test premise: the incomplete path keeps its old sha at version 2: %+v", b)
	}
	h.blobs["skills/a.md"] = []byte("a2\n") // the upload lands
	if st := statusOf(t, d, local); st.State != StateBehind {
		t.Fatalf("state = %q, want %q", st.State, StateBehind)
	}
}

func TestStatusAfterAFailedWriteIsBehind(t *testing.T) {
	h := newMemHosted()
	h.snapshot(map[string]string{"skills/a.md": "a\n"})
	d := testDriver(t, h, nil)
	local := map[string]string{"skills/a.md": SHA([]byte("a\n"))}
	if err := d.saveBase("skills", hosted.AreaBase{Version: 1, Files: local}); err != nil {
		t.Fatal(err)
	}
	h.snapshot(map[string]string{"skills/a.md": "a2\n"})
	pullAndCommit(t, d, local, map[string]bool{"skills/a.md": true})
	if st := statusOf(t, d, local); st.State != StateBehind {
		t.Fatalf("state = %q, want %q", st.State, StateBehind)
	}
}

// Clearing a merged conflict reseals a sealed base and never seals an unsealed one.
func TestLoadBaseResolveKeepsTheSealVerdict(t *testing.T) {
	for _, wasSealed := range []bool{true, false} {
		h := newMemHosted()
		d := testDriver(t, h, nil)
		b := hosted.AreaBase{Version: 1, Files: map[string]string{}, Unmerged: map[string]string{"skills/a.md": "r"}}
		if wasSealed {
			b.Seal = sealBase("skills", b)
		}
		if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, "skills", b); err != nil {
			t.Fatal(err)
		}
		got, isSealed, err := d.loadBase("skills")
		if err != nil || got.Unmerged != nil {
			t.Fatalf("loadBase = %+v, %v; want the merged conflict cleared", got, err)
		}
		saved, _, _ := hosted.LoadAreaBase(d.Server, d.Project, d.RepoRoot, "skills")
		if isSealed != wasSealed || sealed("skills", saved) != wasSealed {
			t.Errorf("sealed before %v: loadBase says %v, saved base sealed %v", wasSealed, isSealed, sealed("skills", saved))
		}
	}
}

// A stamp-only default whose bytes differ from the base is local; one equal to
// it, or one no recorded side names, is not.
func TestWithStampOnly(t *testing.T) {
	local := map[string]string{"k": "1"}
	stampOnly := map[string]string{"edited": "D", "same": "S", "unnamed": "U", "k": "other"}
	base := map[string]string{"edited": "H", "same": "S"}
	got, added := WithStampOnly(local, stampOnly, base)
	if !reflect.DeepEqual(added, []string{"edited"}) || got["edited"] != "D" || got["k"] != "1" || len(got) != 2 {
		t.Errorf("WithStampOnly = %v added %v", got, added)
	}
	if len(local) != 1 {
		t.Errorf("local was modified: %v", local)
	}
}
