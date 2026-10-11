package cli

// Story-scoped work-state sync (sty_991b6900): push, pull and snapshot take story
// ids or an epic container id and move only those stories and their ledger rows.

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const scopeTheme = "epic:scope-test"

// scopeToml opts executions in too, so a push that leaked one would show.
const scopeToml = "[sync]\nstories = \"personal\"\nledger = \"personal\"\nexecutions = \"personal\"\n\n[hosted]\nproject = \"probe\"\n"

// scopeFixture is a local store holding an epic of two members, a story outside
// it, and an execution and a task under the first member. Every story has two
// ledger rows.
type scopeFixture struct {
	epic, m1, m2, other, exec, task string
}

func (s scopeFixture) members() []string { return []string{s.m1, s.m2} }

func openScopeDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func seedScopeStore(t *testing.T) scopeFixture {
	t.Helper()
	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	mk := func(in workitem.CreateInput) string {
		it, err := db.Stories.Create(ctx, in, time.Now())
		if err != nil {
			t.Fatalf("create %q: %v", in.Title, err)
		}
		return it.ID
	}
	var f scopeFixture
	f.epic = mk(workitem.CreateInput{Kind: workitem.KindStory, Title: "Scope epic", Category: "epic-parent", Tags: []string{scopeTheme}})
	f.m1 = mk(workitem.CreateInput{Kind: workitem.KindStory, Title: "Scope member one", Category: "feature", Tags: []string{scopeTheme}})
	f.m2 = mk(workitem.CreateInput{Kind: workitem.KindStory, Title: "Scope member two", Category: "feature", Tags: []string{scopeTheme}})
	f.other = mk(workitem.CreateInput{Kind: workitem.KindStory, Title: "Scope unrelated", Category: "feature"})
	f.exec = mk(workitem.CreateInput{Kind: workitem.KindExecution, Title: "Execution under m1", ParentID: f.m1})
	f.task = mk(workitem.CreateInput{Kind: workitem.KindTask, Title: "Task under m1", ParentID: f.m1})
	for _, id := range []string{f.epic, f.m1, f.m2, f.other} {
		for _, kind := range []string{"note", "plan"} {
			if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: id, Kind: kind, Body: kind + " for " + id}, time.Now()); err != nil {
				t.Fatalf("ledger %s: %v", id, err)
			}
		}
	}
	return f
}

// localCounts is the number of stories and ledger rows in the local store.
func localCounts(t *testing.T) (stories, rows int) {
	t.Helper()
	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	items, err := db.Stories.List(context.Background(), workitem.ListFilter{Kind: workitem.KindStory, Limit: 2000})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := db.Ledger.ListInsertedAfter(context.Background(), 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	return len(items), len(entries)
}

// postedRows is what Apply received for the probe project: item ids and the
// story_id of every ledger row.
func postedRows(f *fakeWorkstateServer) (items, ledgerStories []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, batch := range f.posts["probe"] {
		for _, it := range batch["items"].([]any) {
			items = append(items, it.(map[string]any)["id"].(string))
		}
		for _, e := range batch["ledger"].([]any) {
			sid, _ := e.(map[string]any)["story_id"].(string)
			ledgerStories = append(ledgerStories, sid)
		}
	}
	slices.Sort(items)
	slices.Sort(ledgerStories)
	return items, ledgerStories
}

func sortedIDs(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// seedHostedScopeStory puts a story on the fake as changed now, with one ledger row.
func seedHostedScopeStory(f *fakeWorkstateServer, id string, tags []string, category string) {
	at := time.Now().Add(time.Hour).UTC()
	seedHostedStory(f, id, "Hosted "+id, "backlog", at)
	f.mu.Lock()
	rec := f.itemsByID["probe"][id].(map[string]any)
	if category != "" {
		rec["category"] = category
	}
	if tags != nil {
		rec["tags"] = tags
	}
	f.mu.Unlock()
	seedHostedLedger(f, "led_"+id, id, "note", map[string]any{"id": id}, at)
}

// AC1: the three verbs take ids as positional arguments; with none they behave as
// before (the rest of the work-state suite, unchanged).
func TestScopedWorkstateVerbsAcceptIDs(t *testing.T) {
	ts, _ := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, scopeToml)
	fx := seedScopeStore(t)

	out, err := runRoot(t, "sync", "workstate", "push", "--dry-run", "--server", ts.URL, fx.m1, fx.m2)
	if err != nil {
		t.Fatalf("push --dry-run ids: %v\n%s", err, out)
	}
	if !strings.Contains(out, "scoped to 2 id(s): "+strings.Join(sortedIDs(fx.m1, fx.m2), ", ")) {
		t.Fatalf("push dry-run does not name the scope:\n%s", out)
	}
	out, err = runRoot(t, "sync", "workstate", "pull", "--dry-run", "--server", ts.URL, fx.m1, "sty_elsewhere")
	if err != nil {
		t.Fatalf("pull --dry-run ids: %v\n%s", err, out)
	}
	if !strings.Contains(out, "scoped to 2 id(s): ") || !strings.Contains(out, "sty_elsewhere") {
		t.Fatalf("pull dry-run does not name the scope:\n%s", out)
	}
	out, err = runRoot(t, "sync", "workstate", "push", "--dry-run", "--server", ts.URL)
	if err != nil || strings.Contains(out, "scoped to") {
		t.Fatalf("unscoped push --dry-run changed: %v\n%s", err, out)
	}
}

// AC2: a push naming one story sends its item and its ledger rows only — no
// execution, no task, no other story — and leaves the stored cursor where it was.
func TestScopedPushSendsOnlyTheNamedStory(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	repo := workstateRepo(t, scopeToml)
	fx := seedScopeStore(t)

	// A project push first, so a cursor is stored for the scoped push to leave alone.
	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("project push: %v\n%s", err, out)
	}
	cursorBefore, err := hosted.LoadWorkstateCursor(ts.URL, "probe", repo)
	if err != nil || cursorBefore.ItemsUpdatedAt.IsZero() {
		t.Fatalf("project push stored no cursor: %+v %v", cursorBefore, err)
	}
	f.mu.Lock()
	f.posts = map[string][]map[string]any{}
	f.mu.Unlock()

	out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL, fx.m1)
	if err != nil {
		t.Fatalf("scoped push: %v\n%s", err, out)
	}
	items, rows := postedRows(f)
	if !slices.Equal(items, []string{fx.m1}) {
		t.Errorf("posted items = %v, want only %s (no execution %s, task %s)", items, fx.m1, fx.exec, fx.task)
	}
	if !slices.Equal(rows, []string{fx.m1, fx.m1}) {
		t.Errorf("posted ledger rows belong to %v, want exactly %s's two", rows, fx.m1)
	}
	if !strings.Contains(out, "scoped push") || !strings.Contains(out, "1 item(s), 2 ledger") {
		t.Errorf("scoped push summary = %q", out)
	}
	cursorAfter, err := hosted.LoadWorkstateCursor(ts.URL, "probe", repo)
	if err != nil {
		t.Fatal(err)
	}
	if cursorAfter != cursorBefore {
		t.Errorf("scoped push moved the cursor: %+v -> %+v", cursorBefore, cursorAfter)
	}
}

// AC3: an epic container id stands for the container and its members, and a pull
// asks the server for exactly that set.
func TestScopedEpicResolvesToContainerAndMembers(t *testing.T) {
	t.Run("push", func(t *testing.T) {
		ts, f := newFakeWorkstateServer(t)
		seedCred(t, ts.URL)
		workstateRepo(t, scopeToml)
		fx := seedScopeStore(t)

		out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL, fx.epic)
		if err != nil {
			t.Fatalf("push epic: %v\n%s", err, out)
		}
		items, rows := postedRows(f)
		if want := sortedIDs(fx.epic, fx.m1, fx.m2); !slices.Equal(items, want) {
			t.Errorf("posted items = %v, want %v (unrelated %s and execution %s absent)", items, want, fx.other, fx.exec)
		}
		if want := sortedIDs(fx.epic, fx.epic, fx.m1, fx.m1, fx.m2, fx.m2); !slices.Equal(rows, want) {
			t.Errorf("posted ledger rows belong to %v, want %v", rows, want)
		}
		if strings.Contains(out, "container only") {
			t.Errorf("an epic with members printed the empty-epic notice:\n%s", out)
		}
	})
	t.Run("pull", func(t *testing.T) {
		_, f, _ := pagingEnv(t, 50)
		fx := seedScopeStore(t)
		for _, id := range append(fx.members(), fx.epic) {
			seedHostedScopeStory(f, id, []string{scopeTheme}, "")
		}
		seedHostedScopeStory(f, fx.other, nil, "")

		out, err := runRoot(t, "sync", "workstate", "pull", fx.epic)
		if err != nil {
			t.Fatalf("pull epic: %v\n%s", err, out)
		}
		reqs := f.snapshotRequests()
		if len(reqs) != 1 {
			t.Fatalf("requests = %d, want 1", len(reqs))
		}
		if got, want := reqs[0].GetStoryIds(), sortedIDs(fx.epic, fx.m1, fx.m2); !slices.Equal(got, want) {
			t.Errorf("story_ids = %v, want the container and its two members %v", got, want)
		}
		if localLedgerHas(t, "led_"+fx.other) {
			t.Errorf("the unrelated story's ledger row landed")
		}
		for _, id := range []string{fx.epic, fx.m1, fx.m2} {
			if !localLedgerHas(t, "led_"+id) {
				t.Errorf("ledger row of %s missing", id)
			}
		}
	})
}

// AC4: a scoped pull or snapshot sends story_ids on every page, applies the rows
// through the same path as the full pass, and leaves the project's since-token
// and checkpoint alone across a multi-page pass.
func TestScopedPassKeepsTokenAndSendsScopeOnEveryPage(t *testing.T) {
	for _, verb := range []string{"pull", "snapshot"} {
		t.Run(verb, func(t *testing.T) {
			url, f, repo := pagingEnv(t, 2)
			seedChangedN(f, 4) // sty_pg01..04, each with a ledger row
			// A token a full pass would discard (other store, other areas) or
			// replace; the scoped pass must read, write and clear nothing.
			seeded := hosted.SnapshotToken{Token: "v1.0", Areas: "other", StoreID: "other-store", SavedAt: time.Unix(1700000000, 0).UTC()}
			if err := hosted.SaveSnapshotToken(url, "probe", repo, seeded); err != nil {
				t.Fatal(err)
			}
			before := syncStateFile(t)

			out, err := runRoot(t, "sync", "workstate", verb, "sty_pg01", "sty_pg02", "sty_pg03")
			if err != nil {
				t.Fatalf("scoped %s: %v\n%s", verb, err, out)
			}
			if !strings.Contains(out, "scoped pass, 2 page(s)") || !strings.Contains(out, "3 item(s), 3 ledger") {
				t.Errorf("summary = %q", out)
			}
			reqs := f.snapshotRequests()
			if len(reqs) != 2 {
				t.Fatalf("requests = %d, want 2 pages", len(reqs))
			}
			want := []string{"sty_pg01", "sty_pg02", "sty_pg03"}
			for i, r := range reqs {
				if !slices.Equal(r.GetStoryIds(), want) {
					t.Errorf("request %d story_ids = %v, want %v", i, r.GetStoryIds(), want)
				}
				if r.GetSinceToken() != "" {
					t.Errorf("request %d carried since_token %q", i, r.GetSinceToken())
				}
			}
			if got := strings.Join(pageTokensOf(reqs), ","); got != ",o2.0" {
				t.Errorf("page tokens = %q, want a second page following next_page_token", got)
			}
			list, lerr := runRoot(t, "story", "list", "--limit", "50")
			if lerr != nil {
				t.Fatalf("list: %v\n%s", lerr, list)
			}
			for _, id := range want {
				if c := strings.Count(list, id); c != 1 || !localLedgerHas(t, "led_pg"+id[len("sty_pg"):]) {
					t.Errorf("%s: listed %d time(s) or its ledger row is missing", id, c)
				}
			}
			if strings.Contains(list, "sty_pg04") {
				t.Errorf("sty_pg04 is outside the scope but landed:\n%s", list)
			}
			if after := syncStateFile(t); after != before {
				t.Errorf("scoped %s changed the sync state file:\n--- before\n%s\n--- after\n%s", verb, before, after)
			}
		})
	}
}

// AC5: every refusal exits non-zero, names its case, applies nothing and never
// falls back to an unscoped pass.
func TestScopedRefusalsApplyNothing(t *testing.T) {
	t.Run("push of an id the store lacks", func(t *testing.T) {
		ts, f := newFakeWorkstateServer(t)
		seedCred(t, ts.URL)
		workstateRepo(t, scopeToml)
		fx := seedScopeStore(t)

		out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL, fx.m1, "sty_nope")
		if err == nil || !strings.Contains(err.Error(), `unknown story or epic id "sty_nope"`) {
			t.Fatalf("err = %v, out = %q; want the unknown-id refusal", err, out)
		}
		if n := f.postCount("probe"); n != 0 {
			t.Errorf("a refused push posted %d batch(es)", n)
		}
	})
	t.Run("pull from a server that ignores the scope", func(t *testing.T) {
		_, f, _ := pagingEnv(t, 50)
		f.ignoreScope = true
		seedChangedN(f, 3)
		sBefore, lBefore := localCounts(t)

		out, err := runRoot(t, "sync", "workstate", "pull", "sty_pg01")
		if err == nil || !strings.Contains(err.Error(), "does not support story-scoped Snapshot") || !strings.Contains(err.Error(), "nothing applied") {
			t.Fatalf("err = %v, out = %q; want the unsupported-scope refusal", err, out)
		}
		if s, l := localCounts(t); s != sBefore || l != lBefore {
			t.Errorf("store went from %d/%d to %d/%d stories/ledger rows", sBefore, lBefore, s, l)
		}
	})
	t.Run("pull of an id the server lacks", func(t *testing.T) {
		_, f, _ := pagingEnv(t, 50)
		seedChangedN(f, 2)
		sBefore, lBefore := localCounts(t)

		out, err := runRoot(t, "sync", "workstate", "pull", "sty_pg01", "sty_missing")
		if err == nil || !strings.Contains(err.Error(), "sty_missing not found on") || !strings.Contains(err.Error(), "nothing applied") {
			t.Fatalf("err = %v, out = %q; want the not-found refusal", err, out)
		}
		if s, l := localCounts(t); s != sBefore || l != lBefore {
			t.Errorf("store went from %d/%d to %d/%d stories/ledger rows", sBefore, lBefore, s, l)
		}
	})
	t.Run("pull whose page token the server rejects", func(t *testing.T) {
		_, f, _ := pagingEnv(t, 2)
		seedChangedN(f, 4)
		f.mu.Lock()
		f.snapRejectCall = 2 // the second page's token is refused
		f.mu.Unlock()
		sBefore, lBefore := localCounts(t)

		out, err := runRoot(t, "sync", "workstate", "pull", "sty_pg01", "sty_pg02", "sty_pg03")
		if err == nil || !strings.Contains(err.Error(), "page token rejected") || !strings.Contains(err.Error(), "nothing applied") {
			t.Fatalf("err = %v, out = %q; want the rejected-token refusal", err, out)
		}
		reqs := f.snapshotRequests()
		if len(reqs) != 2 {
			t.Errorf("requests = %d, want the two scoped pages and no fallback pass", len(reqs))
		}
		for i, r := range reqs {
			if len(r.GetStoryIds()) == 0 {
				t.Errorf("request %d was unscoped", i)
			}
		}
		if s, l := localCounts(t); s != sBefore || l != lBefore {
			t.Errorf("the first page was applied: store went from %d/%d to %d/%d", sBefore, lBefore, s, l)
		}
	})
}

// AC6: an epic container synced without members says so on stderr and still exits
// 0 — whether it was fresh to the store or already there.
func TestScopedEpicWithoutMembersSaysSo(t *testing.T) {
	notice := func(id string) string { return "epic " + id + ": container only" }

	t.Run("pull", func(t *testing.T) {
		_, f, _ := pagingEnv(t, 50)
		const epic, m1, m2 = "sty_ep000001", "sty_ep000002", "sty_ep000003"
		seedHostedScopeStory(f, epic, []string{scopeTheme}, "epic-parent")
		seedHostedScopeStory(f, m1, []string{scopeTheme}, "feature")
		seedHostedScopeStory(f, m2, []string{scopeTheme}, "feature")

		// (1) a fresh store: the container arrives alone.
		out, err := runRoot(t, "sync", "workstate", "pull", epic)
		if err != nil {
			t.Fatalf("pull container: %v\n%s", err, out)
		}
		if c := strings.Count(out, notice(epic)); c != 1 {
			t.Errorf("notice printed %d time(s), want 1:\n%s", c, out)
		}
		for _, want := range []string{"none are in the local store", "name the member story ids"} {
			if !strings.Contains(out, want) {
				t.Errorf("notice lacks %q:\n%s", want, out)
			}
		}
		reqs := f.snapshotRequests()
		if len(reqs) != 1 || !slices.Equal(reqs[0].GetStoryIds(), []string{epic}) {
			t.Fatalf("requests = %v, want one naming only %s", reqs, epic)
		}
		if s, _ := localCounts(t); s != 1 {
			t.Errorf("stories after the first pull = %d, want only the container", s)
		}

		// (2) the container is local now and still has no members: said again,
		// and the request still names only the container.
		out, err = runRoot(t, "sync", "workstate", "pull", epic)
		if err != nil {
			t.Fatalf("second pull: %v\n%s", err, out)
		}
		if c := strings.Count(out, notice(epic)); c != 1 {
			t.Errorf("second pull printed the notice %d time(s), want 1:\n%s", c, out)
		}
		reqs = f.snapshotRequests()
		if len(reqs) != 2 || !slices.Equal(reqs[1].GetStoryIds(), []string{epic}) {
			t.Fatalf("second request story_ids = %v, want only %s", reqs[len(reqs)-1].GetStoryIds(), epic)
		}

		// (3) naming the members syncs them, and there is nothing to say.
		out, err = runRoot(t, "sync", "workstate", "pull", m1, m2)
		if err != nil {
			t.Fatalf("pull members: %v\n%s", err, out)
		}
		if strings.Contains(out, "container only") {
			t.Errorf("naming the members printed the notice:\n%s", out)
		}
		if s, _ := localCounts(t); s != 3 {
			t.Errorf("stories after naming the members = %d, want 3", s)
		}
	})
	t.Run("push", func(t *testing.T) {
		ts, f := newFakeWorkstateServer(t)
		seedCred(t, ts.URL)
		workstateRepo(t, scopeToml)
		db := openScopeDB(t)
		epic, err := db.Stories.Create(context.Background(), workitem.CreateInput{
			Kind: workitem.KindStory, Title: "Lonely epic", Category: "epic-parent", Tags: []string{"epic:lonely"},
		}, time.Now())
		_ = db.Close()
		if err != nil {
			t.Fatal(err)
		}

		out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL, epic.ID)
		if err != nil {
			t.Fatalf("push container: %v\n%s", err, out)
		}
		if items, _ := postedRows(f); !slices.Equal(items, []string{epic.ID}) {
			t.Errorf("posted items = %v, want only the container", items)
		}
		if c := strings.Count(out, notice(epic.ID)); c != 1 {
			t.Errorf("notice printed %d time(s), want 1:\n%s", c, out)
		}
	})
}
