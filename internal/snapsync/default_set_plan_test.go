package snapsync

import (
	"errors"
	"reflect"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// Plans over a DefaultSet (sty_04e8c778): a hosted file that is an unedited
// embedded default is supplied by the binary, so a tree that lacks it is in step.

const defPath = "tasks/tsk_default.md"

func defaultsOf(sha string) DefaultSet {
	return NewDefaultSet(map[string]map[string]bool{defPath: {sha: true}}, nil)
}

// AC7: only hosted bytes the set holds are the binary's; everything else is
// written, with or without a base and after an earlier pull classed the path.
func TestPlanPullClassesHostedBytesByContent(t *testing.T) {
	const d, e, o = "default-sha", "edited-sha", "older-stamp-sha"
	set := defaultsOf(d)
	for _, c := range []struct {
		name   string
		base   *hosted.AreaBase
		remote string
		want   Action
		set    DefaultSet
	}{
		{"base holds the default, hosted edited it", &hosted.AreaBase{Version: 1, Files: map[string]string{defPath: d}}, e, Write, set},
		{"no base, hosted bytes are not the default", nil, e, Write, set},
		{"no base, hosted bytes are the default", nil, d, BinaryDefault, set},
		{"base holds the default, hosted unchanged", &hosted.AreaBase{Version: 1, Files: map[string]string{defPath: d}}, d, BinaryDefault, set},
		{"base lacks it, hosted bytes are the default", &hosted.AreaBase{Version: 1, Files: map[string]string{}}, d, BinaryDefault, set},
		{"an older stamp the base proved", &hosted.AreaBase{Version: 1, Files: map[string]string{defPath: o}, Defaults: map[string]string{defPath: o}}, o,
			BinaryDefault, NewDefaultSet(nil, &hosted.AreaBase{Defaults: map[string]string{defPath: o}})},
		{"an older stamp nothing proved", &hosted.AreaBase{Version: 1, Files: map[string]string{defPath: o}}, o, LocalDeleted, set},
		{"the zero set holds nothing", nil, d, Write, DefaultSet{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			plan := PlanPull(c.base, rec(0, map[string]string{defPath: c.remote}), 2, map[string]string{}, map[string]string{defPath: c.remote}, c.set)
			if got := actions(plan)[defPath]; got != c.want {
				t.Fatalf("action = %v, want %v", got, c.want)
			}
			if c.want == BinaryDefault {
				if plan.Base.Files[defPath] != c.remote {
					t.Errorf("planned base = %v, want the snapshot's sha kept", plan.Base.Files)
				}
				if len(plan.Differences()) != 0 {
					t.Errorf("a binary default is a difference: %+v", plan.Differences())
				}
			}
		})
	}
}

// A base-proved default stays proved across a pull that plans it again.
func TestPlanPullCarriesProvedDefaultsForward(t *testing.T) {
	const o = "older-stamp-sha"
	base := &hosted.AreaBase{Version: 1, Files: map[string]string{defPath: o}, Defaults: map[string]string{defPath: o}}
	plan := PlanPull(base, rec(0, map[string]string{defPath: o}), 2, map[string]string{}, map[string]string{defPath: o}, NewDefaultSet(nil, base))
	if !reflect.DeepEqual(plan.Base.Defaults, map[string]string{defPath: o}) {
		t.Errorf("planned Defaults = %v, want the proof kept", plan.Base.Defaults)
	}
	gone := PlanPull(base, rec(0, map[string]string{}), 2, map[string]string{}, nil, NewDefaultSet(nil, base))
	if len(gone.Base.Defaults) != 0 {
		t.Errorf("a default the snapshot dropped leaves Defaults: %v", gone.Base.Defaults)
	}
}

// AC6: a push is claimed for a real change and never for a default alone.
func TestPlanPushClaimsOnlyForARealChange(t *testing.T) {
	const d, mine, edited = "default-sha", "mine-sha", "edited-sha"
	none := func(string) bool { return false }
	set := defaultsOf(d)
	const authored = "tasks/tsk_mine.md"
	base := &hosted.AreaBase{Version: 2, Files: map[string]string{defPath: d, authored: mine}}
	eff := rec(1, map[string]string{defPath: d, authored: mine})
	heads := map[string]string{defPath: d, authored: mine}

	plan, err := PlanPush("tasks", "pull", "reconcile", "loc", false, false, base, 2, eff, map[string]string{authored: mine}, heads, set, none)
	if err != nil || plan.Claim {
		t.Fatalf("local holding only the authored file over a hosted default: claim=%v err=%v, want no claim", plan.Claim, err)
	}
	plan, err = PlanPush("tasks", "pull", "reconcile", "loc", false, false, base, 2, eff, map[string]string{authored: edited}, heads, set, none)
	if err != nil || !plan.Claim {
		t.Fatalf("an edited authored file: claim=%v err=%v, want a claim", plan.Claim, err)
	}
	if _, kept := plan.Record.Files[authored]; !kept || plan.Record.Files[authored] != edited {
		t.Errorf("claimed record = %v, want the edited task", plan.Record.Files)
	}
	if _, has := plan.Record.Files[defPath]; has {
		t.Errorf("the claimed record carries the default: %v", plan.Record.Files)
	}
	plan, _ = PlanPush("tasks", "pull", "reconcile", "loc", false, false, base, 2, eff, map[string]string{authored: mine}, heads, DefaultSet{}, none)
	if !plan.Claim {
		t.Error("without the set, a default the tree lacks is a deletion to publish (strict comparison)")
	}
}

// AC10: a base that holds a hosted default, a tree that lacks it and a newer
// snapshot is behind, never diverged — this machine changed nothing.
func TestPlanPushBehindOverAHeldDefaultIsNotDiverged(t *testing.T) {
	const d, a = "default-sha", "authored-sha"
	const authored = "tasks/tsk_mine.md"
	none := func(string) bool { return false }
	set := defaultsOf(d)
	base := &hosted.AreaBase{Version: 1, Files: map[string]string{defPath: d, authored: a}}
	eff := rec(1, map[string]string{authored: a}) // another machine's push dropped the default
	heads := map[string]string{authored: a}

	defaultOnly := &hosted.AreaBase{Version: 1, Files: map[string]string{defPath: d}}
	for _, c := range []struct {
		name     string
		base     *hosted.AreaBase
		local    map[string]string
		diverged bool
		set      DefaultSet
	}{
		{"empty tree over a base of only the default", defaultOnly, map[string]string{}, false, set},
		{"only the unchanged authored file", base, map[string]string{authored: a}, false, set},
		{"the authored file edited", base, map[string]string{authored: "edited"}, true, set},
		{"the unchanged authored file, without the set", base, map[string]string{authored: a}, true, DefaultSet{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := PlanPush("tasks", "satelle sync pull", "satelle sync reconcile", "loc", false, false, c.base, 2, eff, c.local, heads, c.set, none)
			var behind *ErrBehind
			if !errors.As(err, &behind) {
				t.Fatalf("err = %v, want ErrBehind", err)
			}
			if behind.Diverged != c.diverged {
				t.Errorf("Diverged = %v, want %v", behind.Diverged, c.diverged)
			}
			wantCmd := "satelle sync pull"
			if c.diverged {
				wantCmd = "satelle sync reconcile"
			}
			if behind.PullCmd != wantCmd {
				t.Errorf("PullCmd = %q, want %q", behind.PullCmd, wantCmd)
			}
		})
	}
}

// AC10, Status: the same shape reads behind, not diverged, through the driver.
func TestStatusBehindOverAHeldDefaultIsNotDiverged(t *testing.T) {
	const authored = "tasks/tsk_mine.md"
	const defBody = "---\n---\nDEFAULT task\n"
	h := newMemHosted()
	h.snapshot(map[string]string{authored: "mine\n"})
	h.snapshot(map[string]string{authored: "mine v2\n"}) // another machine's push: the default is gone
	d := testDriver(t, h, nil)
	d.Defaults = func(string) (map[string]map[string]bool, error) {
		return map[string]map[string]bool{defPath: {SHA([]byte(defBody)): true}}, nil
	}
	b := hosted.AreaBase{Version: 1, Files: map[string]string{defPath: SHA([]byte(defBody)), authored: SHA([]byte("mine\n"))}}
	if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, "tasks", b); err != nil {
		t.Fatal(err)
	}
	st, err := d.Status(t.Context(), "tasks", map[string]string{authored: SHA([]byte("mine\n"))})
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateBehind {
		t.Errorf("state = %q, want %q (the tree changed nothing)", st.State, StateBehind)
	}
	st, err = d.Status(t.Context(), "tasks", map[string]string{authored: SHA([]byte("my edit\n"))})
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateDiverged {
		t.Errorf("state = %q, want %q (the tree edited the authored file)", st.State, StateDiverged)
	}
}

// A tree beside held defaults reads in step, not unpushed, and the area with a
// defaults-only snapshot reads in step with no local file at all.
func TestStatusInStepBesideHeldDefaults(t *testing.T) {
	const authored = "tasks/tsk_mine.md"
	const defBody = "---\n---\nDEFAULT task\n"
	h := newMemHosted()
	h.snapshot(map[string]string{authored: "mine\n", defPath: defBody})
	d := testDriver(t, h, nil)
	d.Defaults = func(string) (map[string]map[string]bool, error) {
		return map[string]map[string]bool{defPath: {SHA([]byte(defBody)): true}}, nil
	}
	b := hosted.AreaBase{Version: 1, Files: map[string]string{defPath: SHA([]byte(defBody)), authored: SHA([]byte("mine\n"))}}
	if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, "tasks", b); err != nil {
		t.Fatal(err)
	}
	st, err := d.Status(t.Context(), "tasks", map[string]string{authored: SHA([]byte("mine\n"))})
	if err != nil || st.State != StateInStep {
		t.Errorf("status = %+v (%v), want in step beside a held default", st, err)
	}
	st, err = d.Status(t.Context(), "tasks", map[string]string{})
	if err != nil || st.State != StateBehind {
		t.Errorf("empty tree = %+v (%v): the authored file is hosted, so the area is behind", st, err)
	}
}
