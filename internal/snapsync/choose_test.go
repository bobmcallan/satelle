package snapsync

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// chooseFixture plans a pull that produces every action but the two no-base ones
// (see chooseNoBase): fresh each call, because Choose rewrites the plan.
func chooseFixture() (PullPlan, Record, map[string]string) {
	base := &hosted.AreaBase{Version: 1, Files: map[string]string{
		"remote-changed": "b1", "local-changed": "b2", "both-changed": "b3", "remote-deleted": "b4",
		"same": "b5", "local-deleted": "b6", "remote-deleted-local-edited": "b7", "gone-both": "b8",
		"remote-changed-local-deleted": "b9",
	}, Unmerged: map[string]string{"unmerged": "ru"}}
	remote := rec(1, map[string]string{
		"remote-changed": "r1", "local-changed": "b2", "both-changed": "r3",
		"same": "b5", "local-deleted": "b6", "added-remote": "r10", "remote-changed-local-deleted": "r9",
		"incomplete": "ri", "unmerged": "ru2",
	})
	local := map[string]string{
		"remote-changed": "b1", "local-changed": "l2", "both-changed": "l3", "remote-deleted": "b4",
		"same": "b5", "remote-deleted-local-edited": "l7", "added-local": "l11", "unmerged": "lu",
		"incomplete": "li",
	}
	heads := map[string]string{
		"remote-changed": "r1", "both-changed": "r3", "added-remote": "r10", "remote-changed-local-deleted": "r9",
		"local-changed": "b2", "local-deleted": "b6", "incomplete": "stale", "unmerged": "ru2",
	}
	return PlanPull(base, remote, 2, local, heads, DefaultSet{}), remote, heads
}

func chooseNoBase() (PullPlan, Record, map[string]string) {
	remote := rec(0, map[string]string{"missing": "m"})
	local := map[string]string{"stale-done.md": "s", "notes.md": "n"}
	heads := map[string]string{"missing": "m", "stale-done.md": "s"}
	return PlanPull(nil, remote, 1, local, heads, DefaultSet{}), remote, heads
}

func entryOf(t *testing.T, p PullPlan, path string) Entry {
	t.Helper()
	for _, e := range p.Entries {
		if e.Path == path {
			return e
		}
	}
	t.Fatalf("no entry for %s", path)
	return Entry{}
}

// Every pull action against both choices: the resulting action, whose bytes the
// choice replaces, and the base this machine will hold.
func TestChooseEveryAction(t *testing.T) {
	type want struct {
		action Action
		saved  Origin
	}
	cases := []struct {
		path   string
		before Action
		noBase bool
		local  want
		hosted want
	}{
		{"remote-changed", Write, false, want{KeepLocal, OriginHosted}, want{Write, OriginLocal}},
		{"added-remote", Write, false, want{LocalDeleted, OriginHosted}, want{Write, OriginNone}},
		{"remote-deleted", Delete, false, want{KeepLocal, OriginNone}, want{Delete, OriginLocal}},
		{"local-changed", KeepLocal, false, want{KeepLocal, OriginNone}, want{Write, OriginLocal}},
		{"added-local", KeepLocal, false, want{KeepLocal, OriginNone}, want{Delete, OriginLocal}},
		{"local-deleted", LocalDeleted, false, want{LocalDeleted, OriginNone}, want{Write, OriginNone}},
		{"both-changed", Conflict, false, want{KeepLocal, OriginHosted}, want{Write, OriginLocal}},
		{"remote-deleted-local-edited", DeletedRemotelyKept, false, want{DeletedRemotelyKept, OriginNone}, want{Delete, OriginLocal}},
		{"unmerged", StillUnmerged, false, want{KeepLocal, OriginHosted}, want{Write, OriginLocal}},
		{"stale-done.md", RemovedStale, true, want{LocalOnly, OriginNone}, want{RemovedStale, OriginLocal}},
		{"notes.md", LocalOnly, true, want{LocalOnly, OriginNone}, want{Delete, OriginLocal}},
	}
	for _, c := range cases {
		for _, side := range []Side{TakeLocal, TakeHosted} {
			plan, remote, heads := chooseFixture()
			if c.noBase {
				plan, remote, heads = chooseNoBase()
			}
			if got := entryOf(t, plan, c.path).Action; got != c.before {
				t.Fatalf("%s: planned %v, want %v", c.path, got, c.before)
			}
			w := c.local
			if side == TakeHosted {
				w = c.hosted
			}
			saved, err := plan.Choose(remote, heads, c.path, side)
			if err != nil {
				t.Errorf("%s side %d: %v", c.path, side, err)
				continue
			}
			e := entryOf(t, plan, c.path)
			if e.Action != w.action || saved != w.saved || e.Overridden != w.saved || e.Chosen != side {
				t.Errorf("%s side %d: action=%v saved=%v chosen=%v, want %v %v", c.path, side, e.Action, saved, e.Chosen, w.action, w.saved)
			}
			if _, still := plan.Base.Unmerged[c.path]; still {
				t.Errorf("%s side %d: still marked unmerged after a choice", c.path, side)
			}
		}
	}
}

// The base after a choice is what makes the next push publish the local file or
// drop it, and the next pull a no-op.
func TestChooseBase(t *testing.T) {
	plan, remote, heads := chooseFixture()
	for _, p := range []string{"both-changed", "remote-changed", "remote-deleted"} {
		if _, err := plan.Choose(remote, heads, p, TakeLocal); err != nil {
			t.Fatal(err)
		}
	}
	if plan.Base.Files["both-changed"] != "r3" {
		t.Errorf("a conflict taken local takes the hosted sha as its base, got %q", plan.Base.Files["both-changed"])
	}
	if plan.Base.Files["remote-changed"] != "r1" {
		t.Errorf("a hosted change kept local is based on the hosted sha, got %q", plan.Base.Files["remote-changed"])
	}
	if _, ok := plan.Base.Files["remote-deleted"]; ok {
		t.Error("a hosted deletion kept local leaves the base, so the push adds the file back")
	}
	if _, ok := plan.Base.Unmerged["both-changed"]; ok {
		t.Error("a chosen conflict is no longer unmerged")
	}
	if got := plan.Base.Unmerged["unmerged"]; got != "" {
		// An earlier unmerged path stays so until it is chosen itself.
		t.Logf("unrelated unmerged marker kept: %q", got)
	}
}

func TestChooseStillUnmergedClearsTheCopy(t *testing.T) {
	plan, remote, heads := chooseFixture()
	if _, err := plan.Choose(remote, heads, "unmerged", TakeHosted); err != nil {
		t.Fatal(err)
	}
	e := entryOf(t, plan, "unmerged")
	if !e.ClearCopy || e.Remote != "ru2" || plan.Base.Files["unmerged"] != "ru2" || plan.Base.Unmerged["unmerged"] != "" {
		t.Errorf("taking hosted for an old conflict: %+v base=%+v", e, plan.Base)
	}
}

func TestChooseRefusals(t *testing.T) {
	plan, remote, heads := chooseFixture()
	for _, c := range []struct {
		path, want string
	}{
		{"same", "does not differ"},
		{"gone-both", "not in this area"},
		{"nowhere", "not in this area"},
		{"incomplete", "hosted bytes incomplete"},
	} {
		for _, side := range []Side{TakeLocal, TakeHosted} {
			_, err := plan.Choose(remote, heads, c.path, side)
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), c.path) {
				t.Errorf("%s side %d: err=%v, want one naming the path and %q", c.path, side, err, c.want)
			}
		}
	}
	// A hosted change whose bytes are not uploaded cannot be taken, but local-only
	// choices that need no hosted bytes still can.
	plan, remote, _ = chooseFixture()
	if _, err := plan.Choose(remote, map[string]string{}, "local-changed", TakeHosted); err == nil {
		t.Error("taking hosted bytes the hosted copy lacks was accepted")
	}
	if _, err := plan.Choose(remote, map[string]string{}, "local-changed", Side(9)); err == nil {
		t.Error("an unknown side was accepted")
	}
}

func TestChooseAllSkipsInSyncAndIncomplete(t *testing.T) {
	plan, _, _ := chooseFixture()
	got := ChooseAll(plan, map[string]Side{"both-changed": TakeHosted}, TakeLocal)
	if got["both-changed"] != TakeHosted {
		t.Error("a per-path choice must beat the area-wide one")
	}
	if _, ok := got["same"]; ok {
		t.Error("an in-step path was chosen")
	}
	if _, ok := got["incomplete"]; ok {
		t.Error("an incomplete path was chosen by -all")
	}
	if got["remote-changed"] != TakeLocal || got["added-local"] != TakeLocal {
		t.Errorf("differing paths not all chosen: %v", got)
	}
}

func TestDescribeNamesEveryClass(t *testing.T) {
	plan, _, _ := chooseFixture()
	seen := map[string]bool{}
	for _, e := range plan.Entries {
		class, def := Describe(e)
		if class == "" || def == "" {
			t.Errorf("%s: empty description", e.Path)
		}
		seen[class] = true
	}
	for _, want := range []string{"changed here only", "changed on hosted only", "changed on both", "added on hosted only", "added here only", "deleted here only", "deleted on hosted only", "still unmerged"} {
		if !seen[want] {
			t.Errorf("class %q never described", want)
		}
	}
}
