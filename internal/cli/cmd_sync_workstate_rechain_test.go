package cli

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// setLocalChain replaces a story's local events with the given ones, each kept with
// the base it is given: the shapes an earlier pull left.
func setLocalChain(t *testing.T, id string, events []ledger.Entry) {
	t.Helper()
	withRepoStore(t, func(db *store.DB) {
		ctx := context.Background()
		cur, err := db.Ledger.StoryChain(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var gone []string
		for _, e := range cur {
			gone = append(gone, e.ID)
		}
		if len(gone) > 0 {
			if err := db.Ledger.RemoveEvents(ctx, id, gone, func(ledger.Entry) bool { return true }); err != nil {
				t.Fatal(err)
			}
		}
		for _, e := range events {
			if _, err := db.Ledger.Upsert(ctx, e, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// relinked is the events in the order given, each built on the one before and the
// first on none: a chain linked in the order its events arrived.
func relinked(events ...ledger.Entry) []ledger.Entry {
	out := make([]ledger.Entry, len(events))
	base := ""
	for i, e := range events {
		e.BaseID = base
		out[i] = e
		base = e.ID
	}
	return out
}

func spliced(parts ...[]ledger.Entry) []ledger.Entry {
	var out []ledger.Entry
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// chainShape is a chain's events and bases, in chain order.
func chainShape(chain []ledger.Entry) []string {
	var out []string
	for _, e := range chain {
		out = append(out, e.ID+"<-"+e.BaseID)
	}
	return out
}

func localWork(id string) ledger.Entry {
	return ledger.Entry{ID: "evt_localwork", StoryID: id, Kind: ledger.KindComment, Actor: "bob", Body: "local work"}
}

// forkAll has a pull that cannot read the hosted chains seed every story it pulls
// with a genesis of its own, as the pull that forked them did.
func forkAll(t *testing.T, ts *httptest.Server, f *fakeWorkstateServer) {
	t.Helper()
	f.chain.unreadable = true
	if out, err := pullRaw(t, ts.URL); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	f.chain.unreadable = false
}

var rechainLine = regexp.MustCompile(`(?m)^(rechained|would rechain|refused|would refuse|nothing to rechain) (\S+): (.*)$`)

// rechainOutcomes reads the one line each story gets, the dry run's wording and the
// real run's made the same.
func rechainOutcomes(t *testing.T, out string) map[string]string {
	t.Helper()
	norm := strings.NewReplacer("rechained", "rechain", "would rechain", "rechain", "would refuse", "refuse", "refused", "refuse", "nothing to rechain", "nothing")
	got := map[string]string{}
	for _, m := range rechainLine.FindAllStringSubmatch(out, -1) {
		if _, dup := got[m[2]]; dup {
			t.Fatalf("%s is reported twice:\n%s", m[2], out)
		}
		got[m[2]] = norm.Replace(m[1]) + ": " + m[3]
	}
	return got
}

func noAppendsAfterPush(t *testing.T, ts *httptest.Server, f *fakeWorkstateServer, id string) {
	t.Helper()
	before := len(f.chain.appendsFor(id))
	if out, err := pushRaw(t, ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if n := len(f.chain.appendsFor(id)); n != before {
		t.Errorf("the push sent %d Append(s) for %s after rechain, want none", n-before, id)
	}
}

// sty_8fc70019 AC1: the seeded pair sits after the first hosted events and later
// hosted events are linked onto it; rechain repairs it all the same, the local chain
// becoming the hosted chain event for event.
func TestRechainRepairsMidChainSeed(t *testing.T) {
	ts, f, id, want, repo := forkedByOldPull(t)
	seeds := localChain(t, id)
	shape := spliced(want[:2], seeds, want[2:])
	setLocalChain(t, id, relinked(shape...))
	got := localChain(t, id)
	if !slices.Equal(idsOf(got), idsOf(shape)) || got[2+len(seeds)].BaseID != seeds[len(seeds)-1].ID || want[2].BaseID == seeds[len(seeds)-1].ID {
		t.Fatalf("fixture chain = %v, want later hosted events linked onto the seeded pair", chainShape(got))
	}

	out, err := runRoot(t, "sync", "workstate", "rechain", "--dry-run", "--server", ts.URL)
	if err != nil || !strings.Contains(out, "would rechain "+id) {
		t.Fatalf("rechain --dry-run = %v\n%s", err, out)
	}
	if !slices.Equal(chainShape(localChain(t, id)), chainShape(got)) {
		t.Fatal("--dry-run changed the chain")
	}
	out, err = runRoot(t, "sync", "workstate", "rechain", "--server", ts.URL)
	if err != nil || !strings.Contains(out, "rechained "+id) {
		t.Fatalf("rechain = %v\n%s", err, out)
	}
	sameChain(t, localChain(t, id), want)
	if g, w := chainShape(localChain(t, id)), chainShape(want); !slices.Equal(g, w) {
		t.Errorf("bases after rechain = %v, want %v", g, w)
	}
	if c := storyConflicts(t, repo); len(c) != 0 {
		t.Errorf("conflicts after rechain = %v, want none", c)
	}
	if head := storyHeads(t, ts.URL, "probe", repo)[id]; head != want[len(want)-1].ID {
		t.Errorf("head after rechain = %q, want the hosted latest %q", head, want[len(want)-1].ID)
	}
	noAppendsAfterPush(t, ts, f, id)
}

// sty_8fc70019 AC2: local work anywhere in the chain, not only at its end, refuses
// the story by name and leaves its chain as it was, whether run or dry-run.
func TestRechainRefusesMidChainLocalWork(t *testing.T) {
	ts, _, id, want, _ := forkedByOldPull(t)
	seeds := localChain(t, id)
	setLocalChain(t, id, relinked(spliced(want[:2], seeds, []ledger.Entry{localWork(id)}, want[2:])...))
	before := chainShape(localChain(t, id))

	for _, args := range [][]string{{"--dry-run"}, {}} {
		out, err := runRoot(t, append([]string{"sync", "workstate", "rechain", id, "--server", ts.URL}, args...)...)
		if err == nil || !strings.Contains(out, id+": local events the hosted chain lacks are not only a seeded genesis") || !strings.Contains(out, "evt_localwork (comment)") {
			t.Fatalf("rechain %v = %v\n%s, want the story refused by name with the event", args, err, out)
		}
		if got := chainShape(localChain(t, id)); !slices.Equal(got, before) {
			t.Errorf("rechain %v changed the chain: %v, want %v", args, got, before)
		}
	}
}

// sty_8fc70019 AC3, AC4: over one set of stories in every shape, the dry run reports
// each story as the real run then does, each exactly once, and exits non-zero for
// the refusals in both.
func TestRechainDryRunMatchesRun(t *testing.T) {
	ts, f, ids, wants, _ := publishedStories(t, "head seed", "mid seed", "reordered", "local work", "unlinkable", "in sync")
	forkAll(t, ts, f)
	headSeed, midSeed, reordered, work, unlinkable, inSync := ids[0], ids[1], ids[2], ids[3], ids[4], ids[5]

	w := wants[midSeed]
	setLocalChain(t, midSeed, relinked(spliced(w[:2], localChain(t, midSeed), w[2:])...))
	w = wants[reordered]
	setLocalChain(t, reordered, relinked(spliced([]ledger.Entry{w[1], w[0]}, w[2:])...))
	w = wants[work]
	setLocalChain(t, work, relinked(spliced(w[:2], localChain(t, work), []ledger.Entry{localWork(work)}, w[2:])...))
	setLocalChain(t, inSync, wants[inSync])
	f.chain.mu.Lock()
	f.chain.chains[chainKey("probe", unlinkable)][2].BaseId = "evt_nowhere"
	f.chain.mu.Unlock()

	shapes := map[string][]string{}
	for _, id := range ids {
		shapes[id] = chainShape(localChain(t, id))
	}

	dryOut, dryErr := runRoot(t, "sync", "workstate", "rechain", "--dry-run", "--server", ts.URL)
	if dryErr == nil {
		t.Errorf("--dry-run exited 0 with refusals:\n%s", dryOut)
	}
	for _, id := range ids {
		if got := chainShape(localChain(t, id)); !slices.Equal(got, shapes[id]) {
			t.Errorf("--dry-run changed %s: %v, want %v", id, got, shapes[id])
		}
	}
	runOut, runErr := runRoot(t, "sync", "workstate", "rechain", "--server", ts.URL)
	if runErr == nil {
		t.Errorf("rechain exited 0 with refusals:\n%s", runOut)
	}

	dry, run := rechainOutcomes(t, dryOut), rechainOutcomes(t, runOut)
	if len(dry) != len(run) {
		t.Fatalf("dry run reported %v, run reported %v", dry, run)
	}
	for id, d := range dry {
		if run[id] != d {
			t.Errorf("%s: dry run says %q, run says %q", id, d, run[id])
		}
	}
	for _, id := range []string{headSeed, midSeed, reordered} {
		if !strings.HasPrefix(run[id], "rechain: ") {
			t.Errorf("%s = %q, want it rechained", id, run[id])
		}
		sameChain(t, localChain(t, id), wants[id])
	}
	if !strings.HasPrefix(run[work], "refuse: ") || !strings.Contains(run[work], "evt_localwork (comment)") {
		t.Errorf("local work = %q, want it refused by name", run[work])
	}
	if !strings.HasPrefix(run[unlinkable], "refuse: ") || !strings.Contains(run[unlinkable], "does not link at "+wants[unlinkable][2].ID) {
		t.Errorf("unlinkable = %q, want it refused with the reason", run[unlinkable])
	}
	for _, id := range []string{work, unlinkable} {
		if got := chainShape(localChain(t, id)); !slices.Equal(got, shapes[id]) {
			t.Errorf("refused %s changed: %v, want %v", id, got, shapes[id])
		}
	}
	if _, ok := run[inSync]; ok || len(run) != 5 {
		t.Errorf("run reported %v, want the five stories that differ and not the one in sync", run)
	}

	out, err := runRoot(t, "sync", "workstate", "rechain", inSync, "--dry-run", "--server", ts.URL)
	if err != nil || rechainOutcomes(t, out)[inSync] != "nothing: the local chain is the hosted chain" {
		t.Errorf("rechain of a story in sync = %v\n%s, want nothing to rechain", err, out)
	}
}

// sty_8fc70019 AC4: discovery lists no story that is only ahead of its hosted chain,
// none the hosted project does not hold and none already on it, and a run with only
// repairable stories exits 0.
func TestRechainDiscoverySkipsAheadAndUnpublished(t *testing.T) {
	ts, f, ids, wants, _ := publishedStories(t, "seeded", "ahead", "synced")
	forkAll(t, ts, f)
	seeded, ahead, synced := ids[0], ids[1], ids[2]
	setLocalChain(t, synced, wants[synced])
	setLocalChain(t, ahead, wants[ahead])
	withRepoStore(t, func(db *store.DB) {
		if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{StoryID: ahead, Kind: ledger.KindComment, Actor: "bob", Body: "not pushed yet"}, time.Now()); err != nil {
			t.Fatal(err)
		}
	})
	unpublished := createStoryID(t, "unpublished")
	skipped := []string{ahead, synced, unpublished}
	shapes := map[string][]string{}
	for _, id := range skipped {
		shapes[id] = chainShape(localChain(t, id))
	}

	for _, args := range [][]string{{"--dry-run"}, {}} {
		out, err := runRoot(t, append([]string{"sync", "workstate", "rechain", "--server", ts.URL}, args...)...)
		if err != nil {
			t.Fatalf("rechain %v = %v\n%s, want exit 0", args, err, out)
		}
		got := rechainOutcomes(t, out)
		if len(got) != 1 || !strings.HasPrefix(got[seeded], "rechain: ") {
			t.Errorf("rechain %v reported %v, want only %s", args, got, seeded)
		}
		for _, id := range skipped {
			if strings.Contains(out, id) {
				t.Errorf("rechain %v named %s, want it skipped:\n%s", args, id, out)
			}
			if g := chainShape(localChain(t, id)); !slices.Equal(g, shapes[id]) {
				t.Errorf("rechain %v changed %s: %v, want %v", args, id, g, shapes[id])
			}
		}
	}
}

// sty_8fc70019 AC6: a story whose local events are all the hosted ones, linked in
// another order and with its head in the middle, is found by comparison with the
// hosted chain though no conflict is recorded, and becomes the hosted chain.
func TestRechainRepairsReorderedChain(t *testing.T) {
	ts, f, id, want, repo := publishedElsewhere(t)
	if out, err := pullRaw(t, ts.URL); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	sameChain(t, localChain(t, id), want)
	setLocalChain(t, id, relinked(spliced([]ledger.Entry{want[1], want[0]}, want[2:])...))
	if err := hosted.SaveStoryHeads(ts.URL, "probe", repo, map[string]string{id: want[0].ID}); err != nil {
		t.Fatal(err)
	}
	if c := storyConflicts(t, repo); len(c) != 0 {
		t.Fatalf("conflicts = %v, want none recorded", c)
	}

	out, err := runRoot(t, "sync", "workstate", "rechain", "--server", ts.URL)
	if err != nil || !strings.HasPrefix(rechainOutcomes(t, out)[id], "rechain: ") {
		t.Fatalf("rechain = %v\n%s, want the reordered story rechained", err, out)
	}
	sameChain(t, localChain(t, id), want)
	if g, w := chainShape(localChain(t, id)), chainShape(want); !slices.Equal(g, w) {
		t.Errorf("bases after rechain = %v, want %v", g, w)
	}
	if head := storyHeads(t, ts.URL, "probe", repo)[id]; head != want[len(want)-1].ID {
		t.Errorf("head after rechain = %q, want the hosted latest %q", head, want[len(want)-1].ID)
	}
	noAppendsAfterPush(t, ts, f, id)
}

func ent(id, base, kind string) ledger.Entry {
	return ledger.Entry{ID: id, BaseID: base, StoryID: "sty_x", Kind: kind, Actor: "bob"}
}

func seedEvents(version, snapshot, base string) []ledger.Entry {
	v := ent(version, base, ledger.KindWorkflowVersion)
	v.Refs = json.RawMessage(`{"workflow_version":"h"}`)
	s := ent(snapshot, version, ledger.KindDefinitionSnapshot)
	s.Actor = "satelle"
	s.Refs = json.RawMessage(`{"definition_snapshot":"sty_x"}`)
	return []ledger.Entry{v, s}
}

func hev(id, base string) hosted.ChainEvent { return hosted.ChainEvent{ID: id, BaseID: base} }

func hostedOf(ids ...string) []hosted.ChainEvent {
	var out []hosted.ChainEvent
	base := ""
	for _, id := range ids {
		out = append(out, hev(id, base))
		base = id
	}
	return out
}

func hostedIDs(events []hosted.ChainEvent) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.ID)
	}
	return out
}

func TestPlanRechain(t *testing.T) {
	a, b := ent("a", "", ledger.KindStoryCreated), ent("b", "a", ledger.KindComment)
	seeds := seedEvents("v", "s", "b")
	tests := []struct {
		name         string
		local        []ledger.Entry
		hosted       []hosted.ChainEvent
		outcome      string
		remove, take []string
		seeded       int
		reason       string
	}{
		{
			// The seeded pair sits after the first hosted events and a hosted event is
			// linked onto it, with a child whose base is the hosted one.
			name:    "mid-chain seed with a wrong-base hosted event and its child",
			local:   relinked(spliced([]ledger.Entry{a, b}, seeds, []ledger.Entry{ent("c", "", ledger.KindComment), ent("d", "", ledger.KindComment)})...),
			hosted:  hostedOf("a", "b", "c", "d"),
			outcome: rechainDo, remove: []string{"v", "s", "c", "d"}, take: []string{"c", "d"}, seeded: 2,
		},
		{
			name:    "head seed",
			local:   relinked(seedEvents("v", "s", "")...),
			hosted:  hostedOf("a", "b"),
			outcome: rechainDo, remove: []string{"v", "s"}, take: []string{"a", "b"}, seeded: 2,
		},
		{
			name:    "reordered, nothing local-only",
			local:   relinked(b, a, ent("c", "", ledger.KindComment)),
			hosted:  hostedOf("a", "b", "c"),
			outcome: rechainDo, remove: []string{"b", "a", "c"}, take: []string{"a", "b", "c"},
		},
		{
			name:    "a start of the hosted chain takes the rest",
			local:   []ledger.Entry{a, b},
			hosted:  hostedOf("a", "b", "c"),
			outcome: rechainDo, take: []string{"c"},
		},
		{
			name:    "already the hosted chain",
			local:   []ledger.Entry{a, b},
			hosted:  hostedOf("a", "b"),
			outcome: rechainNothing,
		},
		{
			name:    "local work is refused by name",
			local:   relinked(a, ent("w", "", ledger.KindComment), b),
			hosted:  hostedOf("a", "b"),
			outcome: rechainRefused, reason: "not only a seeded genesis: w (comment)",
		},
		{
			name:    "a workflow_version alone is no seed",
			local:   relinked(seedEvents("v", "s", "")[:1]...),
			hosted:  hostedOf("a"),
			outcome: rechainRefused, reason: "not only a seeded genesis: v (workflow_version)",
		},
		{
			name:    "a hosted chain that does not link",
			local:   []ledger.Entry{a},
			hosted:  []hosted.ChainEvent{hev("a", ""), hev("c", "nowhere")},
			outcome: rechainRefused, reason: "does not link at c",
		},
		{
			name:    "a local chain that forks",
			local:   []ledger.Entry{a, b, ent("x", "a", ledger.KindComment)},
			hosted:  hostedOf("a", "b"),
			outcome: rechainRefused, reason: "forks at a",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := planRechain(tc.local, tc.hosted)
			if got.outcome != tc.outcome || !strings.Contains(got.reason, tc.reason) {
				t.Fatalf("plan = %q %q, want %q containing %q", got.outcome, got.reason, tc.outcome, tc.reason)
			}
			if got.outcome == rechainRefused {
				return
			}
			if !slices.Equal(got.remove, tc.remove) || !slices.Equal(hostedIDs(got.take), tc.take) || got.seeded != tc.seeded {
				t.Errorf("remove %v take %v seeded %d, want %v %v %d", got.remove, hostedIDs(got.take), got.seeded, tc.remove, tc.take, tc.seeded)
			}
			// Nothing that stays builds on an event that goes, so the ledger cannot
			// refuse the removal.
			gone := map[string]bool{}
			for _, id := range got.remove {
				gone[id] = true
			}
			for _, e := range tc.local {
				if !gone[e.ID] && gone[e.BaseID] {
					t.Errorf("%s stays but builds on a removed event %s", e.ID, e.BaseID)
				}
			}
		})
	}
}

func TestDivergent(t *testing.T) {
	a, b := ent("a", "", ledger.KindStoryCreated), ent("b", "a", ledger.KindComment)
	tests := []struct {
		name   string
		local  []ledger.Entry
		hosted []hosted.ChainEvent
		want   bool
	}{
		{"in sync", []ledger.Entry{a, b}, hostedOf("a", "b"), false},
		{"ahead of hosted", []ledger.Entry{a, b, ent("x", "b", ledger.KindComment)}, hostedOf("a", "b"), false},
		{"behind hosted", []ledger.Entry{a}, hostedOf("a", "b"), false},
		{"unpublished", []ledger.Entry{a, b}, nil, false},
		{"reordered", relinked(b, a), hostedOf("a", "b"), true},
		{"wrong base", []ledger.Entry{a, ent("b", "", ledger.KindComment)}, hostedOf("a", "b"), true},
		{"head seed", relinked(seedEvents("v", "s", "")...), hostedOf("a", "b"), true},
		{"mid-chain seed", relinked(spliced([]ledger.Entry{a}, seedEvents("v", "s", "a"), []ledger.Entry{b})...), hostedOf("a", "b"), true},
	}
	for _, tc := range tests {
		if got := divergent(tc.local, tc.hosted); got != tc.want {
			t.Errorf("%s: divergent = %v, want %v", tc.name, got, tc.want)
		}
	}
}
