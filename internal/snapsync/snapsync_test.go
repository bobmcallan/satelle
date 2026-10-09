package snapsync

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// reader serves records by version; a missing key is "no valid record there".
func reader(recs map[int]Record) ReadFunc {
	return func(v int) (Record, bool, error) {
		r, ok := recs[v]
		return r, ok, nil
	}
}

func rec(parent int, files map[string]string) Record {
	return Record{Parent: parent, Nonce: "n", Files: files}
}

func TestRecordPathRoundTrip(t *testing.T) {
	p := RecordPath("skills")
	if p != "backups/sync/skills.snapshot.json" {
		t.Fatalf("RecordPath = %q", p)
	}
	if area, ok := AreaOfRecordPath(p); !ok || area != "skills" {
		t.Fatalf("AreaOfRecordPath(%q) = %q, %v", p, area, ok)
	}
	for _, bad := range []string{"skills/x.md", "backups/sync/.snapshot.json", "backups/sync/a/b.snapshot.json", "backups/sync/skills.json"} {
		if _, ok := AreaOfRecordPath(bad); ok {
			t.Errorf("AreaOfRecordPath(%q) accepted a non-record path", bad)
		}
	}
}

func TestDecodeRejectsNonRecords(t *testing.T) {
	if _, err := Decode([]byte("not json")); err == nil {
		t.Error("garbage decoded as a record")
	}
	if _, err := Decode([]byte(`{"parent":1}`)); err == nil {
		t.Error("a record with no files map decoded")
	}
	r, err := NewRecord(3, "loc", map[string]string{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(r.Encode())
	if err != nil || got.Parent != 3 || got.Files["a"] != "b" || got.Nonce == "" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}

func TestEffectiveChain(t *testing.T) {
	recs := map[int]Record{
		1: rec(0, map[string]string{"a": "1"}),
		2: rec(1, map[string]string{"a": "2"}),
	}
	v, r, err := Effective(0, 2, reader(recs))
	if err != nil || v != 2 || r.Files["a"] != "2" {
		t.Fatalf("Effective = %d %+v %v, want 2", v, r, err)
	}
}

func TestEffectiveDuplicateParentLowestWins(t *testing.T) {
	// 2 and 3 both claim parent 1: 2 won, 3 is a dead head. 4 builds on 2.
	recs := map[int]Record{
		1: rec(0, map[string]string{"a": "1"}),
		2: rec(1, map[string]string{"a": "winner"}),
		3: rec(1, map[string]string{}), // the loser deleted everything
		4: rec(2, map[string]string{"a": "next"}),
	}
	v, r, err := Effective(0, 4, reader(recs))
	if err != nil || v != 4 || r.Files["a"] != "next" {
		t.Fatalf("Effective = %d %+v %v, want 4 built on the winner", v, r, err)
	}
	// The loser as the RAW HEAD: nothing built on top yet.
	v, r, err = Effective(1, 3, reader(map[int]Record{1: recs[1], 2: recs[2], 3: recs[3]}))
	if err != nil || v != 2 || r.Files["a"] != "winner" {
		t.Fatalf("with the loser as raw head Effective = %d %+v %v, want the winner (2)", v, r, err)
	}
}

func TestEffectiveIgnoresOrphanAndSkipsGaps(t *testing.T) {
	recs := map[int]Record{
		1: rec(0, map[string]string{"a": "1"}),
		// 2 is a gap (404 or undecodable)
		3: rec(7, map[string]string{"a": "orphan"}), // parent never effective
		4: rec(1, map[string]string{"a": "ok"}),
	}
	v, r, err := Effective(0, 4, reader(recs))
	if err != nil || v != 4 || r.Files["a"] != "ok" {
		t.Fatalf("Effective = %d %+v %v, want 4", v, r, err)
	}
}

func TestEffectiveFromBaseAndNone(t *testing.T) {
	recs := map[int]Record{1: rec(0, map[string]string{"a": "1"})}
	// Nothing hosted.
	if v, _, err := Effective(0, 0, reader(nil)); err != nil || v != 0 {
		t.Fatalf("empty store: %d %v", v, err)
	}
	// Base is already the head: re-read it.
	v, r, err := Effective(1, 1, reader(recs))
	if err != nil || v != 1 || r.Files["a"] != "1" {
		t.Fatalf("at head: %d %+v %v", v, r, err)
	}
	// A base ahead of the hosted head belongs to a replaced store: start over.
	v, _, err = Effective(5, 1, reader(recs))
	if err != nil || v != 1 {
		t.Fatalf("stale base: %d %v", v, err)
	}
	// A base whose record cannot be read is an error, not a silent snapshot 0.
	if _, _, err = Effective(2, 2, reader(recs)); err == nil {
		t.Fatal("unreadable base did not error")
	}
	boom := errors.New("net")
	if _, _, err = Effective(0, 1, func(int) (Record, bool, error) { return Record{}, false, boom }); !errors.Is(err, boom) {
		t.Fatalf("transport error lost: %v", err)
	}
}

func actions(p PullPlan) map[string]Action {
	out := map[string]Action{}
	for _, e := range p.Entries {
		out[e.Path] = e.Action
	}
	return out
}

func TestPlanPullThreeWay(t *testing.T) {
	base := &hosted.AreaBase{Version: 1, Files: map[string]string{
		"remote-changed": "b1", "local-changed": "b2", "both-changed": "b3", "remote-deleted": "b4",
		"same": "b5", "local-deleted": "b6", "remote-deleted-local-edited": "b7", "gone-both": "b8",
		"remote-changed-local-deleted": "b9",
	}}
	remote := rec(1, map[string]string{
		"remote-changed": "r1", "local-changed": "b2", "both-changed": "r3",
		"same": "b5", "local-deleted": "b6", "added-remote": "r10", "remote-changed-local-deleted": "r9",
	})
	local := map[string]string{
		"remote-changed": "b1", "local-changed": "l2", "both-changed": "l3", "remote-deleted": "b4",
		"same": "b5", "remote-deleted-local-edited": "l7", "added-local": "l11",
	}
	heads := map[string]string{"remote-changed": "r1", "both-changed": "r3", "added-remote": "r10", "remote-changed-local-deleted": "r9"}
	plan := PlanPull(base, remote, 2, local, heads)
	got := actions(plan)
	want := map[string]Action{
		"remote-changed":               Write,
		"local-changed":                KeepLocal,
		"both-changed":                 Conflict,
		"remote-deleted":               Delete,
		"same":                         InSync,
		"local-deleted":                LocalDeleted,
		"remote-deleted-local-edited":  DeletedRemotelyKept,
		"added-remote":                 Write,
		"added-local":                  KeepLocal,
		"remote-changed-local-deleted": Write,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actions =\n%v\nwant\n%v", got, want)
	}
	nb := plan.Base
	if nb.Version != 2 {
		t.Errorf("base version = %d, want 2 (a pull advances it even with conflicts)", nb.Version)
	}
	if nb.Files["both-changed"] != "b3" || nb.Unmerged["both-changed"] != "r3" {
		t.Errorf("a conflicted path keeps its old base sha and is marked unmerged: files=%v unmerged=%v", nb.Files, nb.Unmerged)
	}
	if _, ok := nb.Files["remote-deleted"]; ok {
		t.Error("a path deleted remotely must leave the base")
	}
	if nb.Files["local-changed"] != "b2" {
		t.Error("a locally changed path keeps the OLD base sha so the push publishes it")
	}
}

func TestPlanPullIncompleteLeavesPathAlone(t *testing.T) {
	base := &hosted.AreaBase{Version: 1, Files: map[string]string{"f": "old"}}
	// The snapshot names new bytes but the hosted head is still the old ones.
	plan := PlanPull(base, rec(1, map[string]string{"f": "new"}), 2, map[string]string{"f": "old"}, map[string]string{"f": "old"})
	if got := actions(plan)["f"]; got != Incomplete {
		t.Fatalf("action = %v, want Incomplete", got)
	}
	if plan.Base.Files["f"] != "old" {
		t.Errorf("an incomplete path keeps its old base sha, got %q", plan.Base.Files["f"])
	}
	// Local already equal to the snapshot is in sync whatever the head holds.
	plan = PlanPull(base, rec(1, map[string]string{"f": "new"}), 2, map[string]string{"f": "new"}, map[string]string{"f": "old"})
	if got := actions(plan)["f"]; got != InSync {
		t.Fatalf("local == snapshot: action = %v, want InSync", got)
	}
}

func TestPlanPullNoBase(t *testing.T) {
	remote := rec(0, map[string]string{"equal": "e", "differs": "r", "missing": "m"})
	local := map[string]string{
		"equal": "e", "differs": "l",
		"stale-done.md": "s", "notes.md": "n",
	}
	// stale-done.md equals an abandoned head; notes.md was never published.
	heads := map[string]string{"equal": "e", "differs": "r", "missing": "m", "stale-done.md": "s"}
	plan := PlanPull(nil, remote, 1, local, heads)
	got := actions(plan)
	want := map[string]Action{
		"equal": InSync, "differs": Conflict, "missing": Write,
		"stale-done.md": RemovedStale, "notes.md": LocalOnly,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	if plan.Base.Files["equal"] != "e" || plan.Base.Version != 1 {
		t.Errorf("an equal path is adopted into the base: %+v", plan.Base)
	}
	if _, ok := plan.Base.Files["notes.md"]; ok {
		t.Error("a local-only file must not enter the base")
	}
}

func TestPlanPullStillUnmerged(t *testing.T) {
	base := &hosted.AreaBase{Version: 2, Files: map[string]string{"x": "old"}, Unmerged: map[string]string{"x": "r"}}
	plan := PlanPull(base, rec(0, map[string]string{"x": "r"}), 2, map[string]string{"x": "mine"}, map[string]string{"x": "r"})
	if got := actions(plan)["x"]; got != StillUnmerged {
		t.Fatalf("action = %v, want StillUnmerged", got)
	}
	if plan.Base.Unmerged["x"] != "r" || plan.Base.Files["x"] != "old" {
		t.Errorf("an unresolved conflict must survive a second pull untouched: %+v", plan.Base)
	}
}

func TestResolveClearsMarkersWhoseCopyIsGone(t *testing.T) {
	b := hosted.AreaBase{Version: 3, Files: map[string]string{"a": "old"}, Unmerged: map[string]string{"a": "ra", "b": "rb"}}
	got, changed := Resolve(b, func(p string) bool { return p == "b" })
	if !changed {
		t.Fatal("Resolve reported no change")
	}
	if got.Files["a"] != "ra" {
		t.Errorf("a resolved path takes the remote sha as its base, got %q", got.Files["a"])
	}
	if _, ok := got.Unmerged["a"]; ok || got.Unmerged["b"] != "rb" {
		t.Errorf("only the resolved marker clears: %v", got.Unmerged)
	}
	got, _ = Resolve(got, func(string) bool { return false })
	if got.Unmerged != nil {
		t.Errorf("no markers left should be nil, got %v", got.Unmerged)
	}
}

func TestPlanPushRefusals(t *testing.T) {
	none := func(string) bool { return false }
	eff := rec(0, map[string]string{"a": "1"})

	// Behind: base 1, hosted effective 2.
	_, err := PlanPush("skills", "satelle sync rehydrate", "loc", false, false, &hosted.AreaBase{Version: 1, Files: map[string]string{"a": "1"}}, 2, eff,
		map[string]string{"a": "1"}, map[string]string{"a": "1"}, none)
	var behind *ErrBehind
	if !errors.As(err, &behind) || behind.Base != 1 || behind.Effective != 2 {
		t.Fatalf("err = %v, want ErrBehind{1,2}", err)
	}
	for _, want := range []string{"snapshot 2", "snapshot 1", "satelle sync rehydrate"} {
		if !contains(err.Error(), want) {
			t.Errorf("message %q lacks %q", err.Error(), want)
		}
	}

	// A machine that has never synced, against a hosted snapshot.
	_, err = PlanPush("skills", "pull", "loc", false, false, nil, 2, eff, map[string]string{"a": "1"}, nil, none)
	if !errors.As(err, &behind) || behind.Base != 0 {
		t.Fatalf("no base vs snapshot 2: %v", err)
	}

	// Unmerged.
	_, err = PlanPush("skills", "pull", "loc", false, false,
		&hosted.AreaBase{Version: 2, Files: map[string]string{"a": "1"}, Unmerged: map[string]string{"a": "r"}}, 2, eff,
		map[string]string{"a": "1"}, map[string]string{"a": "1"}, none)
	var um *ErrUnmerged
	if !errors.As(err, &um) || um.Paths[0] != "a" || !contains(err.Error(), "backups/sync-conflicts/skills/a") {
		t.Fatalf("err = %v, want ErrUnmerged naming the copy", err)
	}

	// Versions agree but a hosted change is not applied here yet.
	_, err = PlanPush("skills", "pull", "loc", false, false, &hosted.AreaBase{Version: 2, Files: map[string]string{"a": "0"}}, 2,
		rec(0, map[string]string{"a": "1"}), map[string]string{"a": "0"}, map[string]string{"a": "1"}, none)
	if !errors.As(err, &behind) || len(behind.Pending) != 1 {
		t.Fatalf("err = %v, want ErrBehind with pending paths", err)
	}
}

func TestPlanPushPublishesWholeStateAndOnlyChangedBlobs(t *testing.T) {
	none := func(string) bool { return false }
	base := &hosted.AreaBase{Version: 4, Files: map[string]string{"keep": "k", "edit": "e0", "drop": "d"}}
	eff := rec(3, map[string]string{"keep": "k", "edit": "e0", "drop": "d"})
	local := map[string]string{"keep": "k", "edit": "e1", "new": "n"}
	heads := map[string]string{"keep": "k", "edit": "e0", "drop": "d"}
	plan, err := PlanPush("skills", "pull", "loc", false, false, base, 4, eff, local, heads, none)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Claim || plan.Record.Parent != 4 {
		t.Fatalf("plan = %+v, want a claim on parent 4", plan)
	}
	if _, ok := plan.Record.Files["drop"]; ok {
		t.Error("a file deleted locally must be absent from the published snapshot")
	}
	if !reflect.DeepEqual(plan.Uploads, []string{"edit", "new"}) {
		t.Errorf("uploads = %v, want only the changed and new blobs", plan.Uploads)
	}

	// Nothing changed: no claim.
	plan, err = PlanPush("skills", "pull", "loc", false, false, base, 4, eff, map[string]string{"keep": "k", "edit": "e0", "drop": "d"}, heads, none)
	if err != nil || plan.Claim || len(plan.Uploads) != 0 {
		t.Fatalf("unchanged tree: %+v %v, want no claim and no uploads", plan, err)
	}
}

func TestPlanPushForceOverBehind(t *testing.T) {
	none := func(string) bool { return false }
	eff := rec(2, map[string]string{"a": "a1", "b": "b1"})
	heads := map[string]string{"a": "a1", "b": "b1"}
	local := map[string]string{"a": "a2"}

	// Behind: this machine last synced snapshot 1, the hosted copy is at 3. A plain
	// push is refused; the forced one publishes this tree on parent 3.
	base := &hosted.AreaBase{Version: 1, Files: map[string]string{"a": "a0"}}
	if _, err := PlanPush("skills", "pull", "loc", false, false, base, 3, eff, local, heads, none); err == nil {
		t.Fatal("a behind push without force must be refused")
	}
	plan, err := PlanPush("skills", "pull", "loc", false, true, base, 3, eff, local, heads, none)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Claim || !plan.Forced || plan.BaseVersion != 1 || plan.Record.Parent != 3 {
		t.Fatalf("plan = %+v, want a forced claim on parent 3 from base 1", plan)
	}
	if !reflect.DeepEqual(plan.Record.Files, local) {
		t.Errorf("record files = %v, want exactly the local tree (b, only hosted, is absent)", plan.Record.Files)
	}
	if !reflect.DeepEqual(plan.Uploads, []string{"a"}) {
		t.Errorf("uploads = %v, want only the changed blob", plan.Uploads)
	}

	// Versions agree but a hosted change is not applied here: also overridden.
	base = &hosted.AreaBase{Version: 3, Files: map[string]string{"a": "a0", "b": "b1"}}
	if _, err := PlanPush("skills", "pull", "loc", false, false, base, 3, eff, local, heads, none); err == nil {
		t.Fatal("a push over unapplied hosted changes without force must be refused")
	}
	plan, err = PlanPush("skills", "pull", "loc", false, true, base, 3, eff, local, heads, none)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Claim || !plan.Forced || plan.Record.Parent != 3 || !reflect.DeepEqual(plan.Record.Files, local) {
		t.Fatalf("plan = %+v, want a forced claim of exactly the local tree on parent 3", plan)
	}

	// Nothing to override: force does not mark an ordinary push as forced.
	base = &hosted.AreaBase{Version: 3, Files: eff.Files}
	plan, err = PlanPush("skills", "pull", "loc", false, true, base, 3, eff, eff.Files, heads, none)
	if err != nil || plan.Forced || plan.Claim {
		t.Fatalf("in-sync tree: %+v %v, want no claim and not forced", plan, err)
	}
}

func TestPlanPushForceStillRefusesUnmerged(t *testing.T) {
	none := func(string) bool { return false }
	eff := rec(2, map[string]string{"a": "1"})
	for _, baseVersion := range []int{3, 1} { // versions agree, then behind
		base := &hosted.AreaBase{Version: baseVersion, Files: map[string]string{"a": "1"}, Unmerged: map[string]string{"a": "r"}}
		plan, err := PlanPush("skills", "pull", "loc", false, true, base, 3, eff,
			map[string]string{"a": "1"}, map[string]string{"a": "1"}, none)
		var um *ErrUnmerged
		if !errors.As(err, &um) || um.Paths[0] != "a" {
			t.Fatalf("base %d: err = %v, want ErrUnmerged", baseVersion, err)
		}
		if plan.Claim || len(plan.Uploads) != 0 {
			t.Errorf("base %d: plan = %+v, want an empty plan", baseVersion, plan)
		}
	}
}

func TestPlanPushFirstClaimCarriesForwardUnlessPruned(t *testing.T) {
	none := func(string) bool { return false }
	local := map[string]string{"mine": "m"}
	heads := map[string]string{"theirs": "t", "mine": "old"}
	plan, err := PlanPush("documents", "pull", "loc", false, false, nil, 0, Record{}, local, heads, none)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Record.Files["theirs"] != "t" || plan.Record.Files["mine"] != "m" {
		t.Errorf("first claim must carry hosted files forward, local winning: %v", plan.Record.Files)
	}
	if !reflect.DeepEqual(plan.Uploads, []string{"mine"}) {
		t.Errorf("uploads = %v", plan.Uploads)
	}
	plan, err = PlanPush("documents", "pull", "loc", true, false, nil, 0, Record{}, local, heads, none)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plan.Record.Files["theirs"]; ok {
		t.Errorf("--prune must drop hosted files the tree lacks: %v", plan.Record.Files)
	}
	// Prune of an empty tree still publishes.
	plan, _ = PlanPush("documents", "pull", "loc", true, false, nil, 0, Record{}, map[string]string{}, heads, none)
	if !plan.Claim {
		t.Error("pruning every stale head from an empty tree must still claim")
	}
	// Nothing anywhere: nothing to publish.
	plan, _ = PlanPush("documents", "pull", "loc", false, false, nil, 0, Record{}, map[string]string{}, map[string]string{}, none)
	if plan.Claim {
		t.Error("an empty tree against an empty store must not claim")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
