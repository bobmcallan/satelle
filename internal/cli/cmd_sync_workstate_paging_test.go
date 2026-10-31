package cli

// Paged work-state Snapshot (sty_2cb4e1f6): every verb that pulls work-state
// pages through the project, sends the last pass's token as since_token, resumes
// an interrupted pass from its checkpoint, and falls back to a full pass when
// the server refuses a token.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
	"github.com/bobmcallan/satelle/internal/store"
)

// pagingEnv is a bound repo against the fake server with a page size small
// enough that a handful of rows spans several pages. Flag-free commands resolve
// the server from the global config, as the serve reconcile loop's child does.
func pagingEnv(t *testing.T, pageSize int32) (url string, f *fakeWorkstateServer, repo string) {
	t.Helper()
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	repo = workstateRepo(t, mergeToml)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	prev := hosted.DefaultSnapshotPageSize
	hosted.DefaultSnapshotPageSize = pageSize
	t.Cleanup(func() { hosted.DefaultSnapshotPageSize = prev })
	return ts.URL, f, repo
}

// seedChanged puts a story and a ledger row on the fake as changed now, so a
// since pass that follows an earlier token returns them.
func seedChanged(f *fakeWorkstateServer, suffix, title string) {
	at := time.Now().UTC()
	id, led := "sty_pg"+suffix, "led_pg"+suffix
	seedHostedStory(f, id, title, "backlog", at)
	seedHostedLedger(f, led, id, "note", map[string]any{"s": suffix}, at)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.itemVer = f.stamp(f.itemVer, "probe", id)
	f.ledgerVer = f.stamp(f.ledgerVer, "probe", led)
}

func seedChangedN(f *fakeWorkstateServer, n int) {
	for i := 1; i <= n; i++ {
		seedChanged(f, fmt.Sprintf("%02d", i), "Paged story")
	}
}

func localLedgerHas(t *testing.T, id string) bool {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, ok, err := db.Ledger.GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// wantLocalRows asserts each story is listed exactly once and each ledger row
// is in the store.
func wantLocalRows(t *testing.T, n int) {
	t.Helper()
	list, err := runRoot(t, "story", "list", "--limit", "50")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, list)
	}
	for i := 1; i <= n; i++ {
		suffix := fmt.Sprintf("%02d", i)
		if c := strings.Count(list, "sty_pg"+suffix); c != 1 {
			t.Errorf("story sty_pg%s listed %d time(s), want 1:\n%s", suffix, c, list)
		}
		if !localLedgerHas(t, "led_pg"+suffix) {
			t.Errorf("ledger row led_pg%s missing from the local store", suffix)
		}
	}
}

func syncStateFile(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(hosted.DocumentSyncStatePathOverride)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func pageTokensOf(reqs []*syncpb.SnapshotRequest) []string {
	var out []string
	for _, r := range reqs {
		out = append(out, r.GetPageToken())
	}
	return out
}

// AC2/AC6: pull pages through the project, every request asks for a page, and
// the summary names the pass kind and page count.
func TestWorkstatePullPagesEveryRow(t *testing.T) {
	url, f, _ := pagingEnv(t, 2)
	seedChangedN(f, 5)

	out, err := runRoot(t, "sync", "workstate", "pull", "--server", url)
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(full pass, 3 page(s))") || !strings.Contains(out, "5 item(s), 5 ledger") {
		t.Fatalf("pull summary = %q", out)
	}
	wantLocalRows(t, 5)
	reqs := f.snapshotRequests()
	if got := strings.Join(pageTokensOf(reqs), ","); got != ",o2.0,o4.0" {
		t.Fatalf("page tokens = %q, want one request per page following next_page_token", got)
	}
	for i, r := range reqs {
		if r.GetPageSize() != 2 {
			t.Errorf("request %d page_size = %d, want 2", i, r.GetPageSize())
		}
	}
}

// AC2/AC6: the flag-free snapshot is the command the serve reconcile loop runs.
func TestWorkstateFlagFreeSnapshotPagesEveryRow(t *testing.T) {
	_, f, _ := pagingEnv(t, 2)
	seedChangedN(f, 5)

	out, err := runRoot(t, "sync", "workstate", "snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	want := `Snapshot work-state from project "probe" (full pass, 3 page(s)): 5 item(s), 5 ledger.`
	if !strings.Contains(out, want) {
		t.Fatalf("snapshot summary = %q, want %q", out, want)
	}
	wantLocalRows(t, 5)
	if n := len(f.snapshotRequests()); n != 3 {
		t.Fatalf("requests = %d, want 3", n)
	}
}

// AC3/AC6: the second flag-free snapshot sends the first pass's token and
// receives and applies only what changed; a failed pass keeps the token.
func TestWorkstateSnapshotSendsSinceTokenAndAppliesOnlyChanges(t *testing.T) {
	url, f, _ := pagingEnv(t, 2)
	seedChangedN(f, 5)
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("first snapshot: %v\n%s", err, out)
	}
	firstToken := fmt.Sprintf("v%d.0", f.seq)
	if !strings.Contains(syncStateFile(t), `"token": "`+firstToken+`"`) {
		t.Fatalf("first pass did not save its sync_token %s:\n%s", firstToken, syncStateFile(t))
	}

	seedChanged(f, "06", "Changed after the first pass")
	out, err := runRoot(t, "sync", "workstate", "snapshot")
	if err != nil {
		t.Fatalf("second snapshot: %v\n%s", err, out)
	}
	want := `Snapshot work-state from project "probe" (since pass, 1 page(s)): 1 item(s), 1 ledger.`
	if !strings.Contains(out, want) {
		t.Fatalf("second summary = %q, want %q", out, want)
	}
	reqs := f.snapshotRequests()
	if got := reqs[len(reqs)-1].GetSinceToken(); got != firstToken {
		t.Fatalf("second pass since_token = %q, want the first pass's %q", got, firstToken)
	}
	wantLocalRows(t, 6)
	secondToken := fmt.Sprintf("v%d.0", f.seq)
	if !strings.Contains(syncStateFile(t), `"token": "`+secondToken+`"`) {
		t.Fatalf("second pass did not replace the token with %s:\n%s", secondToken, syncStateFile(t))
	}

	// A pass that fails leaves the token where it was.
	seedChanged(f, "07", "Changed again")
	f.mu.Lock()
	f.snapFailCall = f.gets + 1
	f.mu.Unlock()
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err == nil {
		t.Fatalf("snapshot against a failing server succeeded:\n%s", out)
	}
	if !strings.Contains(syncStateFile(t), `"token": "`+secondToken+`"`) {
		t.Fatalf("a failed pass replaced the token:\n%s", syncStateFile(t))
	}

	// pull shares the token: nothing has changed since the next pass.
	out, err = runRoot(t, "sync", "workstate", "pull", "--server", url)
	if err != nil {
		t.Fatalf("pull after: %v\n%s", err, out)
	}
	if !strings.Contains(out, "since pass") || !strings.Contains(out, "page(s)") {
		t.Fatalf("pull summary = %q", out)
	}
	wantLocalRows(t, 7)
	reqs = f.snapshotRequests()
	if got := reqs[len(reqs)-1].GetSinceToken(); got != secondToken {
		t.Fatalf("pull since_token = %q, want %q", got, secondToken)
	}
}

// AC4: a row delivered more than once — on two pages of one pass, or in both a
// full pass and a later since pass — lands once, with no error.
func TestWorkstateRepeatedRowsApplyOnce(t *testing.T) {
	url, f, _ := pagingEnv(t, 2)
	f.repeatBoundary = true
	seedChangedN(f, 5)
	// Seeded straight into the fake with no version: the fake repeats it in every
	// pass, since pass included.
	seedHostedStory(f, "sty_pg06", "Always repeated", "backlog", time.Now().UTC())
	seedHostedLedger(f, "led_pg06", "sty_pg06", "note", map[string]any{"s": "06"}, time.Now().UTC())

	for pass, args := range [][]string{
		{"sync", "workstate", "pull", "--server", url},
		{"sync", "workstate", "snapshot"},
		{"sync", "workstate", "pull", "--server", url},
	} {
		out, err := runRoot(t, args...)
		if err != nil {
			t.Fatalf("pass %d: %v\n%s", pass+1, err, out)
		}
		if strings.Contains(out, "conflict") {
			t.Fatalf("pass %d refused a repeated row:\n%s", pass+1, out)
		}
	}
	wantLocalRows(t, 6)
}

// AC5: a server that refuses the since_token gets a full pass instead, with one
// line saying why, and the fresh token is saved.
func TestWorkstateRejectedSinceTokenFallsBackToFullPass(t *testing.T) {
	_, f, _ := pagingEnv(t, 2)
	seedChangedN(f, 5)
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("first snapshot: %v\n%s", err, out)
	}
	f.expireTokens()
	seedChanged(f, "06", "After the token expired")
	before := len(f.snapshotRequests())

	out, err := runRoot(t, "sync", "workstate", "snapshot")
	if err != nil {
		t.Fatalf("snapshot after expiry: %v\n%s", err, out)
	}
	if !strings.Contains(out, "since-token discarded: rejected by server (token expired); ran a full pass") {
		t.Fatalf("no discard notice in %q", out)
	}
	if !strings.Contains(out, "(full pass, 3 page(s)): 6 item(s), 6 ledger.") {
		t.Fatalf("summary = %q, want the full pass of all six rows over 3 pages", out)
	}
	reqs := f.snapshotRequests()[before:]
	if reqs[0].GetSinceToken() == "" || reqs[1].GetSinceToken() != "" || reqs[1].GetPageToken() != "" {
		t.Fatalf("want the rejected since request, then a request with no tokens; got %q then %q/%q",
			reqs[0].GetSinceToken(), reqs[1].GetSinceToken(), reqs[1].GetPageToken())
	}
	wantLocalRows(t, 6)
	fresh := fmt.Sprintf(`"token": "v%d.1"`, f.seq)
	if !strings.Contains(syncStateFile(t), fresh) {
		t.Fatalf("fresh token %s not saved:\n%s", fresh, syncStateFile(t))
	}
}

// AC5: --force runs a full pass and ignores a stored token.
func TestWorkstateForceRunsFullPass(t *testing.T) {
	_, f, _ := pagingEnv(t, 2)
	seedChangedN(f, 3)
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("first snapshot: %v\n%s", err, out)
	}
	out, err := runRoot(t, "sync", "workstate", "snapshot", "--force")
	if err != nil {
		t.Fatalf("forced snapshot: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(full pass, 2 page(s)): 3 item(s), 3 ledger.") {
		t.Fatalf("forced summary = %q", out)
	}
	reqs := f.snapshotRequests()
	if got := reqs[len(reqs)-1].GetSinceToken(); got != "" {
		t.Fatalf("forced pass sent since_token %q", got)
	}
}

// AC5: a token saved for other opted-in areas would skip rows the new set needs.
func TestWorkstateAreaChangeDiscardsToken(t *testing.T) {
	_, f, repo := pagingEnv(t, 2)
	seedChangedN(f, 3)
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("first snapshot: %v\n%s", err, out)
	}
	cfg := "[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "sync", "workstate", "snapshot")
	if err != nil {
		t.Fatalf("snapshot after area change: %v\n%s", err, out)
	}
	if !strings.Contains(out, "since-token discarded: opted-in work-state areas changed") || !strings.Contains(out, "full pass") {
		t.Fatalf("output = %q", out)
	}
	reqs := f.snapshotRequests()
	if got := reqs[len(reqs)-1].GetSinceToken(); got != "" {
		t.Fatalf("pass after an area change sent since_token %q", got)
	}
}

// A replaced local database holds none of the rows the token says were synced.
func TestWorkstateReplacedStoreDiscardsToken(t *testing.T) {
	_, f, _ := pagingEnv(t, 2)
	seedChangedN(f, 3)
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("first snapshot: %v\n%s", err, out)
	}
	dbPath := runtimeDBPath(t)
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	out, err := runRoot(t, "sync", "workstate", "snapshot")
	if err != nil {
		t.Fatalf("snapshot after store replaced: %v\n%s", err, out)
	}
	if !strings.Contains(out, "since-token discarded: local store changed") || !strings.Contains(out, "full pass") {
		t.Fatalf("output = %q", out)
	}
	wantLocalRows(t, 3)
}

// AC9: a pass cut short keeps the pages it applied and a checkpoint, saves no
// since-token, and the next pass continues from the checkpoint.
func TestWorkstateInterruptedPassResumesFromCheckpoint(t *testing.T) {
	_, f, _ := pagingEnv(t, 1)
	seedChangedN(f, 4)
	f.mu.Lock()
	f.snapFailCall = 3 // pages 1 and 2 are served, the third request fails
	f.mu.Unlock()

	out, err := runRoot(t, "sync", "workstate", "snapshot")
	if err == nil {
		t.Fatalf("interrupted pass succeeded:\n%s", out)
	}
	list, lerr := runRoot(t, "story", "list", "--limit", "50")
	if lerr != nil {
		t.Fatalf("list: %v\n%s", lerr, list)
	}
	for _, id := range []string{"sty_pg01", "sty_pg02"} {
		if !strings.Contains(list, id) {
			t.Errorf("page 1-2 story %s was not kept:\n%s", id, list)
		}
	}
	for _, id := range []string{"sty_pg03", "sty_pg04"} {
		if strings.Contains(list, id) {
			t.Errorf("story %s from an unserved page is in the store", id)
		}
	}
	state := syncStateFile(t)
	if strings.Contains(state, `"token":`) || !strings.Contains(state, `"resume_page_token": "o2.0"`) {
		t.Fatalf("want a checkpoint at page 3 and no since-token, got:\n%s", state)
	}

	before := len(f.snapshotRequests())
	out, err = runRoot(t, "sync", "workstate", "snapshot")
	if err != nil {
		t.Fatalf("resumed pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(resumed pass, 2 page(s)): 2 item(s), 2 ledger.") {
		t.Fatalf("resumed summary = %q", out)
	}
	reqs := f.snapshotRequests()[before:]
	if reqs[0].GetPageToken() != "o2.0" {
		t.Fatalf("first request of the resumed pass carried page_token %q, want the checkpoint o2.0", reqs[0].GetPageToken())
	}
	wantLocalRows(t, 4)
	state = syncStateFile(t)
	if !strings.Contains(state, fmt.Sprintf(`"token": "v%d.0"`, f.seq)) || strings.Contains(state, "resume_page_token") {
		t.Fatalf("want the since-token saved and the checkpoint cleared, got:\n%s", state)
	}
}

// AC5: a resume checkpoint the server no longer honours falls back to a full pass.
func TestWorkstateRejectedResumeTokenFallsBackToFullPass(t *testing.T) {
	_, f, _ := pagingEnv(t, 1)
	seedChangedN(f, 3)
	f.mu.Lock()
	f.snapFailCall = 2
	f.mu.Unlock()
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err == nil {
		t.Fatalf("interrupted pass succeeded:\n%s", out)
	}
	f.expireTokens()

	out, err := runRoot(t, "sync", "workstate", "snapshot")
	if err != nil {
		t.Fatalf("pass after expiry: %v\n%s", err, out)
	}
	if !strings.Contains(out, "resume page token discarded: rejected by server (token expired); ran a full pass") ||
		!strings.Contains(out, "(full pass, 3 page(s))") {
		t.Fatalf("output = %q", out)
	}
	wantLocalRows(t, 3)
}
