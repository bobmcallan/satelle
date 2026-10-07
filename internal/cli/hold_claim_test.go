package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/verb"
)

// A story in flight on one machine is a hosted hold other machines can see
// (sty_52eb8c2f). These tests fake only the hosted server: a hold surface with
// an atomic checkout, on the same server as the work-state ingest fake.

const (
	otherLoc   = "loc_other_aaaaaaaa"
	otherSeen  = "2026-09-01T00:00:00Z"
	holdNoGit  = "[sync]\nstories = \"personal\"\nledger = \"personal\"\nhold_unpushed = false\n\n[hosted]\nproject = \"probe\"\n"
	pullSyncTm = "[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n"
)

// fakeHoldServer is the hosted hold surface: one holder per story, an atomic
// checkout, and a log it returns verbatim. A story exists once the work-state
// fake has ingested it or the test says the server knows it.
type fakeHoldServer struct {
	mu        sync.Mutex
	f         *fakeWorkstateServer
	known     map[string]bool
	holders   map[string]hosted.HoldState
	log       map[string][]map[string]any
	down      bool
	checkouts []string // "<id>@<location>"
	releases  []string
}

func newFakeHoldServer() *fakeHoldServer {
	return &fakeHoldServer{
		known:   map[string]bool{},
		holders: map[string]hosted.HoldState{},
		log:     map[string][]map[string]any{},
	}
}

func (h *fakeHoldServer) exists(id string) bool {
	return h.known[id] || h.f.hasItem("probe", id) || h.holders[id].LocationID != ""
}

func (h *fakeHoldServer) setHolder(id, loc, label string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.known[id] = true
	h.holders[id] = hosted.HoldState{LocationID: loc, Label: label, LastSeenAt: otherSeen}
}

func (h *fakeHoldServer) setDown(down bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.down = down
}

func (h *fakeHoldServer) holder(id string) hosted.HoldState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.holders[id]
}

func (h *fakeHoldServer) checkoutIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.checkouts)
}

func (h *fakeHoldServer) releasedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.releases)
}

func (h *fakeHoldServer) routes(mux *http.ServeMux, f *fakeWorkstateServer) {
	h.f = f
	const item = "/api/v1/projects/{project}/workstate/items/{id}"
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	})
	// gate answers 503 while the hold surface is down and 404 for a story the
	// server has never seen; it reports whether the handler may go on.
	gate := func(w http.ResponseWriter, r *http.Request) bool {
		if h.down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return false
		}
		if !h.exists(r.PathValue("id")) {
			http.NotFound(w, r)
			return false
		}
		return true
	}
	mux.HandleFunc("GET "+item, func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if !gate(w, r) {
			return
		}
		id := r.PathValue("id")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "hold": h.holders[id]})
	})
	mux.HandleFunc("POST "+item+"/checkout", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if !gate(w, r) {
			return
		}
		id := r.PathValue("id")
		var in struct {
			LocationID string `json:"location_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		h.checkouts = append(h.checkouts, id+"@"+in.LocationID)
		if cur := h.holders[id]; cur.LocationID != "" && cur.LocationID != in.LocationID {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "held", "hold": cur})
			return
		}
		now := hosted.HoldState{LocationID: in.LocationID, Label: "box", LastSeenAt: time.Now().UTC().Format(time.RFC3339)}
		h.holders[id] = now
		_ = json.NewEncoder(w).Encode(now)
	})
	mux.HandleFunc("POST "+item+"/release", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if !gate(w, r) {
			return
		}
		id := r.PathValue("id")
		h.releases = append(h.releases, id)
		delete(h.holders, id)
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("GET "+item+"/checkout-log", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		entries := h.log[r.PathValue("id")]
		if entries == nil {
			entries = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(entries)
	})
}

// holdFixture is a bound repo whose route lets `story set --status in_progress`
// engage with no reviewer, against the fake hosted server.
type holdFixture struct {
	ts   *httptest.Server
	f    *fakeWorkstateServer
	hs   *fakeHoldServer
	repo string
	loc  string
}

func newHoldFixture(t *testing.T, syncToml string) *holdFixture {
	t.Helper()
	hs := newFakeHoldServer()
	ts, f := newFakeWorkstateServerWith(t, hs.routes)
	seedCred(t, ts.URL)
	repo := workstateRepo(t, syncToml)
	t.Chdir(repo)
	writeRoute(t, filepath.Join(repo, ".satelle", "workflows"),
		`["*"]
obligations = ["raised", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
	// The server this machine dials is a machine setting, not a repo one.
	global := "[hosted]\nserver = \"" + ts.URL + "\"\n"
	if err := os.WriteFile(config.GlobalConfigPath(), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	loc, err := hosted.LocationID(repo)
	if err != nil {
		t.Fatal(err)
	}
	return &holdFixture{ts: ts, f: f, hs: hs, repo: repo, loc: loc}
}

func engageStory(t *testing.T, id string) (string, error) {
	t.Helper()
	return runRoot(t, "story", "set", id, "--status", "in_progress")
}

func (fx *holdFixture) status(t *testing.T, id string) string {
	t.Helper()
	out, err := runRoot(t, "story", "get", id)
	if err != nil {
		t.Fatalf("get %s: %v\n%s", id, err, out)
	}
	return jsonField(t, out, "status")
}

func (fx *holdFixture) push(t *testing.T) string {
	t.Helper()
	out, err := runRoot(t, "sync", "workstate", "push", "--server", fx.ts.URL)
	if err != nil {
		t.Fatalf("push must exit zero: %v\n%s", err, out)
	}
	return out
}

func (fx *holdFixture) pending(t *testing.T) map[string]string {
	t.Helper()
	p, err := hosted.PendingClaims(fx.ts.URL, "probe", fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (fx *holdFixture) heldHere(t *testing.T, id string) bool {
	t.Helper()
	reg, err := hosted.LoadHolds(fx.ts.URL, "probe", fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	return reg[id] == fx.loc
}

// AC1: an unheld story is checked out before the engage takes its lease; one
// already held here is an idempotent checkout.
func TestEngageClaimsTheHostedHold(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	unheld := createStoryID(t, "unheld on the server")
	fx.hs.mu.Lock()
	fx.hs.known[unheld] = true
	fx.hs.mu.Unlock()

	if out, err := engageStory(t, unheld); err != nil {
		t.Fatalf("engage unheld: %v\n%s", err, out)
	}
	if got := fx.hs.checkoutIDs(); !slices.Equal(got, []string{unheld + "@" + fx.loc}) {
		t.Fatalf("checkouts = %v, want one for %s", got, unheld)
	}
	if fx.hs.holder(unheld).LocationID != fx.loc || !fx.heldHere(t, unheld) {
		t.Fatalf("hold not placed or not recorded: server=%+v", fx.hs.holder(unheld))
	}
	if len(fx.pending(t)) != 0 {
		t.Fatalf("a placed hold left a pending claim: %v", fx.pending(t))
	}
}

func TestEngageOfAStoryHeldHereIsIdempotent(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "already mine")
	fx.hs.setHolder(id, fx.loc, "box")

	if out, err := engageStory(t, id); err != nil {
		t.Fatalf("engage held here: %v\n%s", err, out)
	}
	if got := fx.hs.holder(id).LocationID; got != fx.loc {
		t.Fatalf("holder = %q, want this location", got)
	}
	if got := fx.status(t, id); got != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got)
	}
}

// AC1: held elsewhere → refused, naming holder, label and last-seen; no lease is
// taken, so the next story engages on the same tree.
func TestEngageRefusedWhenHeldElsewhere(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	taken := createStoryID(t, "taken elsewhere")
	free := createStoryID(t, "free")
	fx.hs.setHolder(taken, otherLoc, "desk")
	fx.hs.mu.Lock()
	fx.hs.known[free] = true
	fx.hs.mu.Unlock()

	out, err := engageStory(t, taken)
	if err == nil {
		t.Fatalf("engage must be refused:\n%s", out)
	}
	for _, want := range []string{otherLoc, "desk", "last seen " + otherSeen, "hold takeover " + taken} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
	if got := fx.status(t, taken); got != "backlog" {
		t.Fatalf("refused story moved to %q", got)
	}
	if fx.hs.holder(taken).LocationID != otherLoc {
		t.Fatalf("refusal disturbed the other holder: %+v", fx.hs.holder(taken))
	}
	if out, err := engageStory(t, free); err != nil {
		t.Fatalf("a refused engage left a lease behind: %v\n%s", err, out)
	}
}

// AC2: two machines engaging the same unheld story at the same moment: the
// server's checkout lets exactly one through, and the other is told who won.
func TestConcurrentEngageExactlyOneWins(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	fx.hs.mu.Lock()
	fx.hs.known["sty_race"] = true
	fx.hs.mu.Unlock()

	cfg := config.Config{Hosted: config.HostedConfig{Project: "probe"}}
	roots := []string{t.TempDir(), t.TempDir()}
	infos := make([]verb.HoldInfo, len(roots))
	errs := make([]error, len(roots))
	var start, done sync.WaitGroup
	start.Add(1)
	for i, root := range roots {
		claim := hostedHoldClaimer(&app.App{Config: cfg, RepoRoot: root})
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			infos[i], errs[i] = claim(t.Context(), "sty_race")
		}()
	}
	start.Done()
	done.Wait()

	var winners, losers []int
	for i := range roots {
		if errs[i] != nil {
			t.Fatalf("machine %d: %v", i, errs[i])
		}
		if infos[i].HeldElsewhere {
			losers = append(losers, i)
		} else {
			winners = append(winners, i)
		}
	}
	if len(winners) != 1 || len(losers) != 1 {
		t.Fatalf("winners=%v losers=%v, want exactly one of each", winners, losers)
	}
	winnerLoc, err := hosted.LocationID(roots[winners[0]])
	if err != nil {
		t.Fatal(err)
	}
	if winnerLoc == "" || infos[losers[0]].Holder != winnerLoc {
		t.Fatalf("loser was told holder %q, want the winner %q", infos[losers[0]].Holder, winnerLoc)
	}
	if fx.hs.holder("sty_race").LocationID != winnerLoc {
		t.Fatalf("server holder = %+v, want the winner", fx.hs.holder("sty_race"))
	}
}

// AC3: the server cannot be reached → the engage goes ahead with a warning and
// the claim is remembered for the next push.
func TestEngageWhenServerDownRecordsPendingClaim(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "engaged offline")
	fx.ts.Close()

	var out string
	var err error
	stderr := captureStderr(t, func() { out, err = engageStory(t, id) })
	if err != nil {
		t.Fatalf("an unreachable server must not stop the engage: %v\n%s", err, out)
	}
	if !strings.Contains(stderr, "hosted hold for "+id+" not placed") {
		t.Errorf("no warning for the unplaced hold on stderr:\n%s", stderr)
	}
	if _, ok := fx.pending(t)[id]; !ok {
		t.Fatalf("no pending claim for %s: %v", id, fx.pending(t))
	}
	if got := fx.status(t, id); got != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got)
	}
}

// AC3: the server has never seen the story → same outcome.
func TestEngageOfAStoryNeverPushedRecordsPendingClaim(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "never pushed")

	var out string
	var err error
	stderr := captureStderr(t, func() { out, err = engageStory(t, id) })
	if err != nil {
		t.Fatalf("a story the server has not seen must still engage: %v\n%s", err, out)
	}
	if !strings.Contains(stderr, "hosted hold for "+id+" not placed") {
		t.Errorf("no warning for the unplaced hold on stderr:\n%s", stderr)
	}
	if _, ok := fx.pending(t)[id]; !ok {
		t.Fatalf("no pending claim for %s: %v", id, fx.pending(t))
	}
	if got := fx.hs.holder(id); got.LocationID != "" {
		t.Errorf("a hold was placed for a story the server has not seen: %+v", got)
	}
}

// AC4 (i): created and engaged before its first push → the push ingests the
// item, then places the hold.
func TestPushPlacesTheHoldOfAStoryEngagedBeforeItsFirstPush(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "engaged then pushed")
	captureStderr(t, func() {
		if out, err := engageStory(t, id); err != nil {
			t.Fatalf("engage: %v\n%s", err, out)
		}
	})
	if fx.f.hasItem("probe", id) {
		t.Fatal("precondition: the server must not have the story yet")
	}

	fx.push(t)

	if !fx.f.hasItem("probe", id) {
		t.Fatal("the push did not ingest the story")
	}
	if got := fx.hs.holder(id).LocationID; got != fx.loc {
		t.Fatalf("server holder = %q, want this location %q", got, fx.loc)
	}
	if !fx.heldHere(t, id) || len(fx.pending(t)) != 0 {
		t.Fatalf("hold not recorded or still pending: held=%v pending=%v", fx.heldHere(t, id), fx.pending(t))
	}
}

// AC4 (ii): another location got there first → a collision line, none of the
// story's rows sent, the rest of the push proceeds.
func TestPushReportsACollisionForAStoryHeldByAnotherLocation(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "engaged before first push, taken meanwhile")
	captureStderr(t, func() {
		if _, err := engageStory(t, id); err != nil {
			t.Fatal(err)
		}
	})
	fx.hs.setHolder(id, otherLoc, "desk")
	other := createStoryID(t, "unrelated and unheld")

	out := fx.push(t)

	for _, want := range []string{"collision: " + id, otherLoc, "desk", "satelle story hold takeover " + id} {
		if !strings.Contains(out, want) {
			t.Errorf("collision output missing %q:\n%s", want, out)
		}
	}
	if fx.f.hasItem("probe", id) || fx.f.ledgerCount("probe", id) != 0 {
		t.Error("rows of the story another location holds were sent")
	}
	if !fx.f.hasItem("probe", other) {
		t.Error("the rest of the push did not proceed")
	}
	if fx.hs.holder(id).LocationID != otherLoc {
		t.Errorf("collision disturbed the other holder: %+v", fx.hs.holder(id))
	}
	if len(fx.pending(t)) != 0 {
		t.Errorf("a settled collision stays pending: %v", fx.pending(t))
	}
}

// AC4 (iii): a story the server knew, engaged while it could not be reached —
// unreachable at the first push it stays pending, then it is granted.
func TestPushGrantsAnOfflineEngageOfAKnownStory(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "known, engaged offline")
	fx.push(t)
	if !fx.f.hasItem("probe", id) {
		t.Fatal("precondition: story pushed")
	}
	fx.hs.setDown(true)
	captureStderr(t, func() {
		if _, err := engageStory(t, id); err != nil {
			t.Fatal(err)
		}
	})
	if _, ok := fx.pending(t)[id]; !ok {
		t.Fatalf("offline engage left no pending claim: %v", fx.pending(t))
	}

	// Still unreachable: the push goes through and the claim waits.
	out := fx.push(t)
	if _, ok := fx.pending(t)[id]; !ok {
		t.Fatalf("claim must stay pending while the server is unreachable: %v", fx.pending(t))
	}
	if !strings.Contains(out, "stays pending") {
		t.Errorf("no note that the claim stays pending:\n%s", out)
	}

	fx.hs.setDown(false)
	fx.push(t)
	if got := fx.hs.holder(id).LocationID; got != fx.loc {
		t.Fatalf("server holder = %q, want this location", got)
	}
	if len(fx.pending(t)) != 0 || !fx.heldHere(t, id) {
		t.Fatalf("granted claim not settled: pending=%v held=%v", fx.pending(t), fx.heldHere(t, id))
	}
}

// AC4 (iii): ... or collided, when another location took it meanwhile.
func TestPushCollidesAnOfflineEngageOfAKnownStory(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "known, engaged offline, taken meanwhile")
	fx.push(t)
	fx.hs.setDown(true)
	captureStderr(t, func() {
		if _, err := engageStory(t, id); err != nil {
			t.Fatal(err)
		}
	})
	fx.hs.setDown(false)
	fx.hs.setHolder(id, otherLoc, "desk")

	out := fx.push(t)

	for _, want := range []string{"collision: " + id, otherLoc, "desk", "satelle story hold takeover " + id} {
		if !strings.Contains(out, want) {
			t.Errorf("collision output missing %q:\n%s", want, out)
		}
	}
	if len(fx.pending(t)) != 0 {
		t.Errorf("a settled collision stays pending: %v", fx.pending(t))
	}
	if fx.hs.holder(id).LocationID != otherLoc {
		t.Errorf("collision disturbed the other holder: %+v", fx.hs.holder(id))
	}
}

// AC5: rows held back for unpushed code stay local, but the hold is re-checked
// out so its last-seen keeps moving.
func TestPushRefreshesTheHoldOfAHeldBackStory(t *testing.T) {
	hs := newFakeHoldServer()
	w := newHoldWorldWith(t, holdSyncToml, hs.routes)
	loc, err := hosted.LocationID(w.repo)
	if err != nil {
		t.Fatal(err)
	}
	hs.mu.Lock()
	hs.known[w.dirty] = true
	hs.holders[w.dirty] = hosted.HoldState{LocationID: loc, Label: "box", LastSeenAt: otherSeen}
	hs.mu.Unlock()
	if err := hosted.RecordHold(w.url, "probe", w.repo, w.dirty, loc); err != nil {
		t.Fatal(err)
	}

	w.push(t)

	w.assertPublished(t, w.dirty, false)
	if got := hs.checkoutIDs(); !slices.Equal(got, []string{w.dirty + "@" + loc}) {
		t.Fatalf("checkouts = %v, want exactly the held-back story held here", got)
	}
	if seen := hs.holder(w.dirty).LastSeenAt; seen == otherSeen || seen == "" {
		t.Fatalf("last seen did not move: %q", seen)
	}
}

// AC6: publishing a story at rest releases the hold held here; a story whose
// rows are still held back keeps it.
func TestPushReleasesTheHoldOfAPublishedStoryAtRest(t *testing.T) {
	hs := newFakeHoldServer()
	w := newHoldWorldWith(t, holdSyncToml, hs.routes)
	loc, err := hosted.LocationID(w.repo)
	if err != nil {
		t.Fatal(err)
	}
	finished := createStoryID(t, "finished")
	parked := createStoryID(t, "parked")
	putInState(t, finished, w.term, "", "", false)
	putInState(t, parked, "blocked", "", "", false)
	for _, id := range []string{finished, parked, w.dirty} {
		hs.setHolder(id, loc, "box")
		if err := hosted.RecordHold(w.url, "probe", w.repo, id, loc); err != nil {
			t.Fatal(err)
		}
	}

	w.push(t)

	w.assertPublished(t, w.dirty, false)
	got := hs.releasedIDs()
	slices.Sort(got)
	want := []string{finished, parked}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("released = %v, want exactly %v (the story held back keeps its hold)", got, want)
	}
	reg, _ := hosted.LoadHolds(w.url, "probe", w.repo)
	if _, ok := reg[finished]; ok {
		t.Error("a released story is still in the local registry")
	}
	if reg[w.dirty] != loc {
		t.Error("the held-back story's registry entry was dropped")
	}
	if hs.holder(w.dirty).LocationID != loc {
		t.Error("the held-back story's hosted hold was released")
	}
}

func seedHostedHoldStory(f *fakeWorkstateServer, id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.itemsByID["probe"] == nil {
		f.itemsByID["probe"] = map[string]any{}
	}
	f.itemsByID["probe"][id] = map[string]any{
		"id": id, "kind": "story", "status": status, "title": "story " + id,
		"created_at": "2026-09-01T00:00:00Z", "updated_at": "2026-09-01T00:00:00Z",
	}
}

func pullIn(t *testing.T, fx *holdFixture) string {
	t.Helper()
	out, err := runRoot(t, "sync", "workstate", "pull", "--server", fx.ts.URL)
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	return out
}

// AC7: a story another location holds is listed after a pull — though its
// hosted status is still backlog — and a hold with no log says last seen, never
// since.
func TestPullListsAStoryInFlightElsewhereWithLastSeen(t *testing.T) {
	fx := newHoldFixture(t, pullSyncTm)
	seedHostedHoldStory(fx.f, "sty_flight", "backlog")
	seedHostedHoldStory(fx.f, "sty_mine", "backlog")
	seedHostedHoldStory(fx.f, "sty_unheld", "backlog")
	seedHostedHoldStory(fx.f, "sty_finished", "done")
	fx.hs.setHolder("sty_flight", otherLoc, "desk")
	fx.hs.setHolder("sty_mine", fx.loc, "box")
	fx.hs.setHolder("sty_finished", otherLoc, "desk")

	out := pullIn(t, fx)

	line := lineWith(out, "sty_flight")
	if line == "" {
		t.Fatalf("a story held elsewhere is not listed as in flight:\n%s", out)
	}
	for _, want := range []string{otherLoc, "desk", "last seen " + otherSeen} {
		if !strings.Contains(line, want) {
			t.Errorf("in-flight line missing %q: %s", want, line)
		}
	}
	if strings.Contains(line, "since") {
		t.Errorf("last-seen must not be presented as since: %s", line)
	}
	for _, id := range []string{"sty_mine", "sty_unheld", "sty_finished"} {
		if lineWith(out, id) != "" {
			t.Errorf("%s must not be listed as in flight elsewhere:\n%s", id, out)
		}
	}
}

func TestPullShowsSinceWhenTheCheckoutLogHasTheHolder(t *testing.T) {
	fx := newHoldFixture(t, pullSyncTm)
	seedHostedHoldStory(fx.f, "sty_flight", "backlog")
	fx.hs.setHolder("sty_flight", otherLoc, "desk")
	fx.hs.mu.Lock()
	fx.hs.log["sty_flight"] = []map[string]any{
		{"action": "checkout", "location_id": otherLoc, "at": "2026-08-30T10:00:00Z"},
	}
	fx.hs.mu.Unlock()

	line := lineWith(pullIn(t, fx), "sty_flight")
	if !strings.Contains(line, "since 2026-08-30T10:00:00Z") {
		t.Fatalf("in-flight line = %q, want since the log entry's time", line)
	}
	if strings.Contains(line, "last seen") {
		t.Errorf("since and last seen are not both shown: %s", line)
	}
}

// AC7: listed even when the pull materialised no rows.
func TestPullListsInFlightStoriesWhenNothingWasPulled(t *testing.T) {
	fx := newHoldFixture(t, "[sync]\nexecutions = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")
	seedHostedHoldStory(fx.f, "sty_flight", "backlog")
	fx.hs.setHolder("sty_flight", otherLoc, "desk")

	out := pullIn(t, fx)

	if !strings.Contains(out, "No work-state rows to pull") {
		t.Fatalf("precondition: the pull materialised nothing:\n%s", out)
	}
	if lineWith(out, "sty_flight") == "" {
		t.Fatalf("an empty pull hid the story in flight:\n%s", out)
	}
}

// AC7: `story hold show` prints the same for one story, or unheld.
func TestStoryHoldShow(t *testing.T) {
	fx := newHoldFixture(t, pullSyncTm)
	fx.hs.setHolder("sty_flight", otherLoc, "desk")
	fx.hs.mu.Lock()
	fx.hs.known["sty_free"] = true
	fx.hs.log["sty_flight"] = []map[string]any{
		{"action": "checkout", "location_id": otherLoc, "at": "2026-08-30T10:00:00Z"},
	}
	fx.hs.mu.Unlock()

	out, err := runRoot(t, "story", "hold", "show", "sty_flight", "--server", fx.ts.URL)
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	for _, want := range []string{"sty_flight", otherLoc, "desk", "since 2026-08-30T10:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q: %s", want, out)
		}
	}
	out, err = runRoot(t, "story", "hold", "show", "sty_free", "--server", fx.ts.URL)
	if err != nil || !strings.Contains(out, "unheld") {
		t.Fatalf("show unheld = %q, %v", out, err)
	}
}

// AC8: with no hosted project bound, engaging dials nothing and records nothing.
func TestEngageUnboundMakesNoHoldCalls(t *testing.T) {
	fx := newHoldFixture(t, "[sync]\nstories = \"personal\"\n")
	id := createStoryID(t, "unbound")
	if out, err := engageStory(t, id); err != nil {
		t.Fatalf("engage: %v\n%s", err, out)
	}
	if got := fx.hs.checkoutIDs(); len(got) != 0 {
		t.Fatalf("unbound engage placed a hold: %v", got)
	}
	if len(fx.pending(t)) != 0 {
		t.Fatalf("unbound engage recorded a pending claim: %v", fx.pending(t))
	}
}

// lineWith returns the first output line naming id.
func lineWith(out, id string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, id) {
			return l
		}
	}
	return ""
}
