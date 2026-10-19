package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const stubRemoteID = "rem_1"

// seenRequest is one request the stub server received.
type seenRequest struct {
	Method, Path, Query, Body string
	Header                    http.Header
}

// stubServer implements the satelle-server wire shape for a remote's declared
// triggers (sty_e8ca6ba4): PUT/GET declared-triggers, POST fire, POST
// declared-triggers/{name}/pause|resume and GET triggers (the fire records). A
// fire of a declared, unpaused trigger pushes a WakeRequest to wakeURL with the
// wake credential. It fails the test on any request that carries a project.
type stubServer struct {
	t       *testing.T
	ts      *httptest.Server
	wakeURL string

	// wakeFail makes a fire of a declared trigger record outcome wake_failed and
	// answer 502 "wake failed", as satelle-server does when it cannot deliver the
	// wake; noRecord leaves that fire unrecorded and badGateway answers a plain
	// 502 instead, so the tests can show what the client must not accept.
	wakeFail, noRecord, badGateway bool

	// rejectNoCron makes a registration answer 400 for a schedule trigger without
	// params.cron, naming the trigger unless nameless; source and event are what a
	// fire's record and its pushed WakeRequest carry (no event when nil).
	rejectNoCron, nameless bool
	source                 string
	event                  any

	mu       sync.Mutex
	seen     []seenRequest
	declared []hosted.DeclaredTrigger
	fires    []hosted.FireRecord
}

func newStubServer(t *testing.T, wakeURL string) *stubServer {
	t.Helper()
	s := &stubServer{t: t, wakeURL: wakeURL}
	s.ts = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.ts.Close)
	return s
}

func (s *stubServer) requests() []seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]seenRequest(nil), s.seen...)
}

func (s *stubServer) reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *stubServer) find(name string) *hosted.DeclaredTrigger {
	for i := range s.declared {
		if s.declared[i].Name == name {
			return &s.declared[i]
		}
	}
	return nil
}

func (s *stubServer) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(body), r.Header.Clone()})

	base := "/api/v1/remotes/" + stubRemoteID
	if !strings.HasPrefix(r.URL.Path, base+"/") {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer stub-token" {
		s.reply(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, base)
	switch {
	case rest == "/declared-triggers" && r.Method == http.MethodPut:
		var in struct {
			Triggers []hosted.DeclaredTrigger `json:"triggers"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			s.reply(w, http.StatusBadRequest, map[string]string{"error": "bad_body"})
			return
		}
		next := make([]hosted.DeclaredTrigger, 0, len(in.Triggers))
		for _, t := range in.Triggers {
			if s.rejectNoCron && t.Kind == "schedule" && t.Params["cron"] == "" {
				e := map[string]string{"error": "invalid_params", "message": "schedule trigger " + t.Name + " requires params.cron", "trigger": t.Name}
				if s.nameless {
					delete(e, "trigger")
				}
				s.reply(w, http.StatusBadRequest, e)
				return
			}
			if old := s.find(t.Name); old != nil {
				t.Paused = old.Paused
			}
			if t.Params["cron"] != "" {
				t.NextFireAt = "2026-10-20T03:00:00Z"
			}
			t.UpdatedAt = "2026-10-19T00:00:00Z"
			next = append(next, t)
		}
		s.declared = next
		s.reply(w, http.StatusOK, s.declared)
	case rest == "/declared-triggers" && r.Method == http.MethodGet:
		s.reply(w, http.StatusOK, s.declared)
	case strings.HasPrefix(rest, "/declared-triggers/") && r.Method == http.MethodPost:
		parts := strings.Split(strings.TrimPrefix(rest, "/declared-triggers/"), "/")
		t := s.find(parts[0])
		if len(parts) != 2 || t == nil || (parts[1] != "pause" && parts[1] != "resume") {
			s.reply(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		t.Paused = parts[1] == "pause"
		s.reply(w, http.StatusOK, t)
	case rest == "/fire" && r.Method == http.MethodPost:
		var in struct {
			StoryID string `json:"story_id"`
			Trigger string `json:"trigger"`
		}
		_ = json.Unmarshal(body, &in)
		id := fmt.Sprintf("fire_%d", len(s.fires)+1)
		if !s.noRecord {
			s.fires = append(s.fires, hosted.FireRecord{ID: id, Trigger: in.Trigger, Source: s.source})
		}
		if in.Trigger != "" {
			t := s.find(in.Trigger)
			if t == nil || t.Paused {
				reason := "trigger is not declared"
				if t != nil {
					reason = "trigger is paused"
				}
				if n := len(s.fires); n > 0 && s.fires[n-1].ID == id {
					s.fires[n-1].Outcome = "refused"
				}
				s.reply(w, http.StatusConflict, map[string]string{"error": "trigger_refused", "message": reason, "trigger_id": id})
				return
			}
		}
		if s.badGateway {
			s.reply(w, http.StatusBadGateway, map[string]string{"error": "bad_gateway"})
			return
		}
		if s.wakeFail {
			if n := len(s.fires); n > 0 && s.fires[n-1].ID == id {
				s.fires[n-1].Outcome = "wake_failed"
			}
			s.reply(w, http.StatusBadGateway, map[string]string{"error": "wake_failed", "message": "wake failed", "trigger_id": id})
			return
		}
		if s.wakeURL != "" {
			payload := map[string]any{
				"trigger_id": id, "remote_id": stubRemoteID, "remote": "edge", "story_id": in.StoryID, "trigger": in.Trigger,
			}
			if s.source != "" {
				payload["source"] = s.source
			}
			if s.event != nil {
				payload["event"] = s.event
			}
			wake, _ := json.Marshal(payload)
			req, _ := http.NewRequest(http.MethodPost, s.wakeURL, bytes.NewReader(wake))
			req.Header.Set("Authorization", "Bearer wake-credential")
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}
		s.reply(w, http.StatusOK, map[string]string{"trigger_id": id})
	case rest == "/triggers" && r.Method == http.MethodGet:
		s.reply(w, http.StatusOK, s.newestFirst())
	default:
		http.NotFound(w, r)
	}
}

// newestFirst is the fire records in the order satelle-server lists them: newest
// first. The caller holds the lock.
func (s *stubServer) newestFirst() []hosted.FireRecord {
	out := make([]hosted.FireRecord, len(s.fires))
	for i, r := range s.fires {
		out[len(s.fires)-1-i] = r
	}
	return out
}

// nightlyCron is the params table of the fixture's "nightly" trigger, the one
// trigger of the five that carries params.
const nightlyCron = "[trigger.params]\ncron = \"0 3 * * *\"\n"

// editEdgeDef rewrites remote "edge"'s remote.toml in repo, replacing from by to.
func editEdgeDef(t *testing.T, repo, from, to string) {
	t.Helper()
	path := filepath.Join(repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", "remote.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), from) {
		t.Fatalf("remote.toml lacks %q:\n%s", from, b)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), from, to, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// remoteWithTriggers creates remote "edge" and gives it the five trigger kinds.
func remoteWithTriggers(t *testing.T) string {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	if out, err := runRoot(t, "remote", "create", "edge"); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	path := filepath.Join(repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", "remote.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := "[[trigger]]\nname = \"manual\"\nkind = \"manual\"\n"
	if !strings.Contains(string(b), old) {
		t.Fatalf("scaffold lacks the manual trigger:\n%s", b)
	}
	five := "[[trigger]]\nname = \"nightly\"\nkind = \"schedule\"\n" + nightlyCron + "\n" +
		"[[trigger]]\nname = \"new-story\"\nkind = \"story-event\"\n\n" +
		"[[trigger]]\nname = \"epic-drive\"\nkind = \"handoff\"\n\n" +
		"[[trigger]]\nname = \"on-push\"\nkind = \"inbound-event\"\n\n" +
		"[[trigger]]\nname = \"from-session\"\nkind = \"mcp\"\n"
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, five, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hosted.SessionTokenEnv, "stub-token")
	return repo
}

func TestRemoteTriggerCLI(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	run := func(args ...string) (string, error) {
		return runRoot(t, append([]string{"remote", "trigger"}, append(args, "--server", srv.ts.URL)...)...)
	}
	last := func() seenRequest {
		rs := srv.requests()
		return rs[len(rs)-1]
	}
	const base = "/api/v1/remotes/" + stubRemoteID

	if out, err := run("register", stubRemoteID, "--def", "edge"); err != nil || !strings.Contains(out, "registered 5 trigger(s)") {
		t.Fatalf("register: %v\n%s", err, out)
	}
	if r := last(); r.Method != "PUT" || r.Path != base+"/declared-triggers" {
		t.Errorf("register sent %s %s", r.Method, r.Path)
	} else {
		// params go exactly as written; the triggers without any carry no params key.
		want := `{"triggers":[{"name":"nightly","kind":"schedule","params":{"cron":"0 3 * * *"}},{"name":"new-story","kind":"story-event"},{"name":"epic-drive","kind":"handoff"},{"name":"on-push","kind":"inbound-event"},{"name":"from-session","kind":"mcp"}]}`
		if r.Body != want {
			t.Errorf("register body = %s\nwant %s", r.Body, want)
		}
	}

	out, err := run("list", stubRemoteID)
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if r := last(); r.Method != "GET" || r.Path != base+"/declared-triggers" || r.Body != "" {
		t.Errorf("list sent %s %s %q", r.Method, r.Path, r.Body)
	}
	for _, want := range []string{"NAME", "PAUSED", "PARAMS", "NEXT FIRE", "nightly", "schedule", "cron=0 3 * * *", "2026-10-20T03:00:00Z", "from-session", "mcp"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output lacks %q:\n%s", want, out)
		}
	}

	if out, err := run("fire", stubRemoteID, "nightly", "--story", "sty_x"); err != nil || !strings.Contains(out, "fire_1") {
		t.Fatalf("fire: %v\n%s", err, out)
	}
	if r := last(); r.Method != "POST" || r.Path != base+"/fire" || r.Body != `{"story_id":"sty_x","trigger":"nightly"}` {
		t.Errorf("fire sent %s %s %s", r.Method, r.Path, r.Body)
	}

	if _, err := run("pause", stubRemoteID, "nightly"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if r := last(); r.Method != "POST" || r.Path != base+"/declared-triggers/nightly/pause" || r.Body != "{}" {
		t.Errorf("pause sent %s %s %s", r.Method, r.Path, r.Body)
	}
	if out, _ := run("list", stubRemoteID); !strings.Contains(out, "nightly       schedule       true") {
		t.Errorf("the paused trigger must list as paused:\n%s", out)
	}

	out, err = run("fire", stubRemoteID, "nightly")
	if err == nil {
		t.Fatalf("a fire of a paused trigger must fail:\n%s", out)
	}
	for _, want := range []string{"trigger nightly refused", "trigger is paused", "fire_2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("409 refusal lacks %q: %v", want, err)
		}
	}
	if r := last(); r.Body != `{"trigger":"nightly"}` {
		t.Errorf("an unstoried fire must send only the trigger, sent %s", r.Body)
	}

	if _, err := run("resume", stubRemoteID, "nightly"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if r := last(); r.Path != base+"/declared-triggers/nightly/resume" {
		t.Errorf("resume sent %s", r.Path)
	}

	// --def holds a name to the local definition before anything is sent.
	before := len(srv.requests())
	if _, err := run("fire", stubRemoteID, "ghost", "--def", "edge"); err == nil || !strings.Contains(err.Error(), `declares no trigger "ghost"`) {
		t.Errorf("an undeclared name must be refused locally, got %v", err)
	}
	if len(srv.requests()) != before {
		t.Error("a locally refused name must send nothing")
	}

	// No project anywhere on the wire: path, query, headers or body.
	for _, r := range srv.requests() {
		hdrs, _ := json.Marshal(r.Header)
		for _, part := range []string{r.Path, r.Query, r.Body, string(hdrs)} {
			if strings.Contains(strings.ToLower(part), "project") {
				t.Errorf("%s %s carries a project: %q", r.Method, r.Path, part)
			}
		}
		if !strings.HasPrefix(r.Path, base+"/") {
			t.Errorf("request outside the remote's routes: %s", r.Path)
		}
	}
}

// receiver is a test wake endpoint: it captures the WakeRequest the server's
// fire pushes to it.
type receiver struct {
	mu   sync.Mutex
	body string
	auth string
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	b, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.body, r.auth = string(b), req.Header.Get("Authorization")
	r.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (r *receiver) take() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, a := r.body, r.auth
	r.body, r.auth = "", ""
	return b, a
}

func newStory(t *testing.T, status string) string {
	t.Helper()
	// Seeded through the store: creating a story at in_progress through the verb
	// would engage it and hold the seat.
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	it, err := a.Store.Stories.Create(context.Background(), workitem.CreateInput{
		Kind: workitem.KindStory, Title: "wake " + status, Status: status,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return it.ID
}

func handoffRows(t *testing.T, story string) []ledger.Entry {
	t.Helper()
	var out []ledger.Entry
	for _, e := range ledgerRows(t, ledger.KindRemoteHandoff) {
		if e.StoryID == story {
			out = append(out, e)
		}
	}
	return out
}

func TestWakeHandoff(t *testing.T) {
	remoteWithTriggers(t)
	inA, inB := newStory(t, "in_progress"), newStory(t, "in_progress")
	backlog := newStory(t, "backlog")

	rcv := &receiver{}
	rcvSrv := httptest.NewServer(rcv)
	t.Cleanup(rcvSrv.Close)
	srv := newStubServer(t, rcvSrv.URL)
	if out, err := runRoot(t, "remote", "trigger", "register", stubRemoteID, "--def", "edge", "--server", srv.ts.URL); err != nil {
		t.Fatalf("register: %v\n%s", err, out)
	}

	// fire makes the stub server push a WakeRequest to the receiver, then hands
	// what the receiver captured to the remote's wake handler.
	fire := func(args ...string) (wake string, out string, err error) {
		t.Helper()
		if o, ferr := runRoot(t, append([]string{"remote", "trigger", "fire", stubRemoteID, "--server", srv.ts.URL}, args...)...); ferr != nil {
			t.Fatalf("fire %v: %v\n%s", args, ferr, o)
		}
		body, auth := rcv.take()
		if body == "" || auth != "Bearer wake-credential" {
			t.Fatalf("the receiver got body %q auth %q", body, auth)
		}
		out, err = runRootIn(t, body, "remote", "wake", "edge", "--request", "-")
		return body, out, err
	}

	// A declared trigger naming a story at an entry status: one hand-off.
	wake, out, err := fire("nightly", "--story", inA)
	if err != nil {
		t.Fatalf("wake: %v\n%s", err, out)
	}
	for _, want := range []string{`"trigger":"nightly"`, `"trigger_id":"fire_1"`, `"story_id":"` + inA + `"`} {
		if !strings.Contains(wake, want) {
			t.Errorf("the pushed WakeRequest lacks %s: %s", want, wake)
		}
	}
	rows := handoffRows(t, inA)
	if len(rows) != 1 {
		t.Fatalf("want one hand-off row for %s, got %d", inA, len(rows))
	}
	var p map[string]string
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p["remote"] != "edge" || p["trigger"] != "nightly" || p["kind"] != "schedule" || p["fire_id"] != "fire_1" {
		t.Errorf("hand-off payload = %v", p)
	}

	// A redelivered WakeRequest records nothing new.
	if out, err := runRootIn(t, wake, "remote", "wake", "edge"); err != nil || !strings.Contains(out, "no new hand-off") {
		t.Errorf("redelivery: %v\n%s", err, out)
	}
	if n := len(handoffRows(t, inA)); n != 1 {
		t.Errorf("a redelivered wake must add nothing, have %d rows", n)
	}

	// A story at no entry status is refused with its status and the entries.
	_, out, err = fire("nightly", "--story", backlog)
	if err == nil || !strings.Contains(err.Error(), "story "+backlog+" is at backlog; remote edge accepts in_progress") {
		t.Errorf("want a refusal naming the status and the entries, got %v\n%s", err, out)
	}
	if n := len(handoffRows(t, backlog)); n != 0 {
		t.Errorf("a refused story must record nothing, have %d rows", n)
	}

	// An undeclared trigger name is refused.
	undeclared := `{"trigger_id":"fire_9","remote_id":"rem_1","remote":"edge","story_id":"` + inB + `","trigger":"ghost"}`
	if out, err := runRootIn(t, undeclared, "remote", "wake", "edge"); err == nil || !strings.Contains(err.Error(), "unknown trigger ghost") {
		t.Errorf("want an unknown-trigger refusal, got %v\n%s", err, out)
	}
	if n := len(handoffRows(t, inB)); n != 0 {
		t.Errorf("a refused wake must record nothing, have %d rows", n)
	}

	// No story: a sweep hands off every story at an entry status and no other.
	if _, out, err = fire("nightly"); err != nil {
		t.Fatalf("sweep: %v\n%s", err, out)
	}
	for _, id := range []string{inA, inB} {
		if n := len(handoffRows(t, id)); n != map[string]int{inA: 2, inB: 1}[id] {
			t.Errorf("after the sweep %s has %d rows", id, n)
		}
	}
	if n := len(handoffRows(t, backlog)); n != 0 {
		t.Errorf("the sweep must not take a story at no entry status, have %d rows", n)
	}

	// An unnamed fire is recorded with the trigger none.
	if _, out, err = fire("--story", inB); err != nil {
		t.Fatalf("unnamed: %v\n%s", err, out)
	}
	rows = handoffRows(t, inB)
	var last map[string]string
	_ = json.Unmarshal(rows[len(rows)-1].Payload, &last)
	if last["trigger"] != "" || last["fire_id"] != "fire_4" || !strings.Contains(out, "trigger none") {
		t.Errorf("an unnamed fire must record trigger none: payload %v\n%s", last, out)
	}
	if err := (func() error { _, e := runRootIn(t, `{"remote":"edge"}`, "remote", "wake", "edge"); return e })(); err == nil {
		t.Error("a request with no fire-record id must be refused")
	}
}

// setStub changes the stub's behaviour between requests, under its lock.
func (s *stubServer) setStub(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

// fireIDs is the set of fire-record ids in the listing. The server lists newest
// first, so nothing here depends on the order.
func fireIDs(recs []hosted.FireRecord) map[string]bool {
	seen := map[string]bool{}
	for _, r := range recs {
		seen[r.FireID()] = true
	}
	return seen
}

// newFire returns the record in recs that is not in seen and carries trigger.
func newFire(recs []hosted.FireRecord, seen map[string]bool, trigger string) (hosted.FireRecord, bool) {
	for _, r := range recs {
		if r.FireID() != "" && !seen[r.FireID()] && r.Trigger == trigger {
			return r, true
		}
	}
	return hosted.FireRecord{}, false
}

// checkNamedFire fires trigger on the remote and returns the output. A fire the
// server accepted must be on record under the trigger's name. A fire it
// answered with 502 "wake failed" also passes, when the listing shows a new
// record carrying that name with outcome wake_failed. Any other error is
// returned.
func checkNamedFire(run func(...string) (string, error), list func() []hosted.FireRecord, remoteID, trigger string) (string, error) {
	seen := fireIDs(list())
	out, err := run("fire", remoteID, trigger)
	var failed *hosted.WakeFailedError
	wakeFailed := errors.As(err, &failed)
	if err != nil && !wakeFailed {
		return out, fmt.Errorf("fire %s: %w", trigger, err)
	}
	r, ok := newFire(list(), seen, trigger)
	if !ok {
		return out, fmt.Errorf("fire %s left no new record carrying the trigger name (error %v)", trigger, err)
	}
	if wakeFailed && r.Outcome != "wake_failed" {
		return out, fmt.Errorf("a wake failure must record outcome wake_failed, got %+v", r)
	}
	return out, nil
}

// checkRefusedFire fires a trigger the server refuses (a paused one) and returns
// the output. The refusal must carry the trigger's 409, and the listing must
// show a new record for the trigger with outcome refused.
func checkRefusedFire(run func(...string) (string, error), list func() []hosted.FireRecord, remoteID, trigger string) (string, error) {
	seen := fireIDs(list())
	out, err := run("fire", remoteID, trigger)
	if err == nil || !strings.Contains(err.Error(), "trigger "+trigger+" refused") {
		return out, fmt.Errorf("a paused trigger must be refused with its 409, got %v", err)
	}
	r, ok := newFire(list(), seen, trigger)
	if !ok {
		return out, fmt.Errorf("the refused fire must be recorded: no new record for %s", trigger)
	}
	if r.Outcome != "refused" {
		return out, fmt.Errorf("a refused fire must record outcome refused, got %+v", r)
	}
	return out, nil
}

// TestRemoteTriggerFireWakeFailed proves, without the network, that a named fire
// answered 502 "wake failed" and recorded is accepted and reported as a wake
// failure with the fire recorded, and that nothing else is.
func TestRemoteTriggerFireWakeFailed(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	run := func(args ...string) (string, error) {
		return runRoot(t, append([]string{"remote", "trigger"}, append(args, "--server", srv.ts.URL)...)...)
	}
	list := func() []hosted.FireRecord {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return srv.newestFirst()
	}
	if out, err := run("register", stubRemoteID, "--def", "edge"); err != nil {
		t.Fatalf("register: %v\n%s", err, out)
	}

	srv.setStub(func() { srv.wakeFail = true })
	if out, err := checkNamedFire(run, list, stubRemoteID, "nightly"); err != nil {
		t.Fatalf("a recorded wake failure must pass: %v\n%s", err, out)
	}
	// What the command reports names the outcome and the record, not the status.
	_, err := run("fire", stubRemoteID, "nightly")
	if err == nil {
		t.Fatal("a failed wake must still exit non-zero")
	}
	for _, want := range []string{"wake failed", "fire recorded as fire_2", "nightly"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("fire output lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("fire output must name the outcome, not only the status: %v", err)
	}

	// A wake failure the server did not record is not accepted.
	srv.setStub(func() { srv.noRecord = true })
	if out, err := checkNamedFire(run, list, stubRemoteID, "nightly"); err == nil {
		t.Errorf("an unrecorded wake failure must fail the check:\n%s", out)
	}
	srv.setStub(func() { srv.noRecord = false })

	// A plain 502 is not a wake failure, whatever is on record.
	srv.setStub(func() { srv.wakeFail, srv.badGateway = false, true })
	if out, err := checkNamedFire(run, list, stubRemoteID, "nightly"); err == nil {
		t.Errorf("a plain 502 must fail the check:\n%s", out)
	}
	if _, err := run("fire", stubRemoteID, "nightly"); err == nil || strings.Contains(err.Error(), "wake failed") {
		t.Errorf("a plain 502 must stay a generic error, got %v", err)
	}

	// The listing is newest first and holds older "nightly" records by now; a
	// refused fire is still found as the new one, with outcome refused.
	srv.setStub(func() { srv.badGateway = false })
	if out, err := run("pause", stubRemoteID, "nightly"); err != nil {
		t.Fatalf("pause: %v\n%s", err, out)
	}
	if out, err := checkRefusedFire(run, list, stubRemoteID, "nightly"); err != nil {
		t.Errorf("a recorded refusal must pass: %v\n%s", err, out)
	}
	srv.setStub(func() { srv.noRecord = true })
	if out, err := checkRefusedFire(run, list, stubRemoteID, "nightly"); err == nil {
		t.Errorf("an unrecorded refusal must fail the check:\n%s", out)
	}
}

// TestRemoteTriggerRegisterRejected proves a server 400 on register is reported
// with the server's reason and the trigger it names, and that the CLI itself
// judges no params: the same cron-less definition registers when the server
// accepts it.
func TestRemoteTriggerRegisterRejected(t *testing.T) {
	repo := remoteWithTriggers(t)
	editEdgeDef(t, repo, nightlyCron, "")
	srv := newStubServer(t, "")
	register := func() (string, error) {
		return runRoot(t, "remote", "trigger", "register", stubRemoteID, "--def", "edge", "--server", srv.ts.URL)
	}

	if out, err := register(); err != nil {
		t.Fatalf("the CLI must not validate params; the server accepted them: %v\n%s", err, out)
	}
	if got := srv.find("nightly"); got == nil || got.Params != nil {
		t.Errorf("a trigger without params must be stored without any: %+v", got)
	}

	srv.setStub(func() { srv.rejectNoCron = true })
	_, err := register()
	if err == nil {
		t.Fatal("a 400 must fail the register")
	}
	for _, want := range []string{"trigger nightly rejected", "schedule trigger nightly requires params.cron"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("400 report lacks %q: %v", want, err)
		}
	}

	srv.setStub(func() { srv.nameless = true })
	_, err = register()
	if err == nil || !strings.Contains(err.Error(), "trigger registration rejected: schedule trigger nightly requires params.cron") {
		t.Errorf("a 400 naming no trigger must report the reason alone, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "trigger nightly rejected") {
		t.Errorf("the CLI must not infer the trigger name: %v", err)
	}
}

// TestWakeCarriesSourceAndEvent proves the fire's source and event ride on the
// hand-off as the server sent them: a wake with no event records today's keys
// plus source, and a push-shaped wake adds the event's fields without choosing
// the story.
func TestWakeCarriesSourceAndEvent(t *testing.T) {
	remoteWithTriggers(t)
	story := newStory(t, "in_progress")
	rcv := &receiver{}
	rcvSrv := httptest.NewServer(rcv)
	t.Cleanup(rcvSrv.Close)
	srv := newStubServer(t, rcvSrv.URL)
	if out, err := runRoot(t, "remote", "trigger", "register", stubRemoteID, "--def", "edge", "--server", srv.ts.URL); err != nil {
		t.Fatalf("register: %v\n%s", err, out)
	}

	// fire pushes the stub's WakeRequest to the receiver, hands it to the remote's
	// wake handler and returns the new hand-off's payload and the command output.
	fire := func(args ...string) (map[string]string, string) {
		t.Helper()
		before := len(handoffRows(t, story))
		if o, err := runRoot(t, append([]string{"remote", "trigger", "fire", stubRemoteID, "--server", srv.ts.URL}, args...)...); err != nil {
			t.Fatalf("fire %v: %v\n%s", args, err, o)
		}
		body, _ := rcv.take()
		out, err := runRootIn(t, body, "remote", "wake", "edge")
		if err != nil {
			t.Fatalf("wake %s: %v\n%s", body, err, out)
		}
		rows := handoffRows(t, story)
		if len(rows) != before+1 {
			t.Fatalf("want one new hand-off for %s, have %d after %d\n%s", body, len(rows), before, out)
		}
		var p map[string]string
		if err := json.Unmarshal(rows[len(rows)-1].Payload, &p); err != nil {
			t.Fatal(err)
		}
		return p, out
	}
	keys := func(p map[string]string) string {
		var ks []string
		for k := range p {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}

	// No event: today's payload keys plus source, with the value as sent.
	for _, source := range []string{"api", "mcp", "schedule"} {
		srv.setStub(func() { srv.source, srv.event = source, nil })
		p, _ := fire("nightly", "--story", story)
		if got, want := keys(p), "fire_id,kind,remote,source,trigger"; got != want {
			t.Errorf("source %s: payload keys %s, want %s (%v)", source, got, want, p)
		}
		if p["source"] != source || p["remote"] != "edge" || p["trigger"] != "nightly" || p["kind"] != "schedule" {
			t.Errorf("source %s: payload = %v", source, p)
		}
	}

	// A push-shaped wake: no story_id, so it sweeps; branch and sha are passed
	// on unchanged and the event picks nothing.
	srv.setStub(func() {
		srv.source = "push"
		srv.event = map[string]string{"kind": "push", "repo": "acme/edge", "branch": "feature/x", "sha": "abc123"}
	})
	p, out := fire()
	if p["source"] != "push" || p["event_kind"] != "push" || p["repo"] != "acme/edge" || p["branch"] != "feature/x" || p["sha"] != "abc123" {
		t.Errorf("the push event must ride on the hand-off verbatim: %v", p)
	}
	if !strings.Contains(out, "at feature/x@abc123") {
		t.Errorf("the wake line must show the event's branch and sha:\n%s", out)
	}
}

// TestRemoteTriggerFires proves `fires` lists the server's fire records with
// their source exactly as sent, a record that carries none shown as -.
func TestRemoteTriggerFires(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	srv.setStub(func() {
		srv.fires = []hosted.FireRecord{
			{ID: "fire_1", Trigger: "nightly", Source: "api"},
			{ID: "fire_2", Trigger: "from-session", Source: "mcp"},
			{ID: "fire_3", Trigger: "nightly", Source: "schedule", Outcome: "wake_failed"},
			{ID: "fire_4", Source: "push"},
			{ID: "fire_5", Trigger: "nightly"},
		}
	})
	out, err := runRoot(t, "remote", "trigger", "fires", stubRemoteID, "--server", srv.ts.URL)
	if err != nil {
		t.Fatalf("fires: %v\n%s", err, out)
	}
	if r := srv.requests(); len(r) != 1 || r[0].Method != "GET" || r[0].Path != "/api/v1/remotes/"+stubRemoteID+"/triggers" {
		t.Errorf("fires must read the fire records once: %+v", r)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1+5 || !strings.HasPrefix(lines[0], "FIRE") || !strings.Contains(lines[0], "SOURCE") {
		t.Fatalf("want a header and five records:\n%s", out)
	}
	// newest first, as the stub lists them
	for i, want := range [][]string{
		{"fire_5", "nightly", "-", "-"},
		{"fire_4", "none", "push", "-"},
		{"fire_3", "nightly", "schedule", "wake_failed"},
		{"fire_2", "from-session", "mcp", "-"},
		{"fire_1", "nightly", "api", "-"},
	} {
		if got := strings.Fields(lines[i+1]); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("record %d = %v, want %v", i, got, want)
		}
	}
}

// TestRemoteTriggerLive runs the trigger commands against a real satelle-server.
// It is skipped unless SATELLE_LIVE_SERVER (the base URL), SATELLE_LIVE_REMOTE
// (the remote's server id) and SATELLE_TOKEN (a session token for a member who
// may declare triggers) are set. It asserts nothing about a wake arriving: that
// delivery is proven against the stub server above.
func TestRemoteTriggerLive(t *testing.T) {
	server, remoteID, token := os.Getenv("SATELLE_LIVE_SERVER"), os.Getenv("SATELLE_LIVE_REMOTE"), os.Getenv(hosted.SessionTokenEnv)
	if server == "" || remoteID == "" || token == "" {
		t.Skip("set SATELLE_LIVE_SERVER, SATELLE_LIVE_REMOTE and SATELLE_TOKEN to run against a real satelle-server")
	}
	remoteWithTriggers(t)
	t.Setenv(hosted.SessionTokenEnv, token) // remoteWithTriggers installs the stub's
	// The fixture's schedule trigger keeps its params.cron: satelle-server refuses
	// a schedule trigger without one, so every register here sends it.
	run := func(args ...string) (string, error) {
		return runRoot(t, append([]string{"remote", "trigger"}, append(args, "--server", server)...)...)
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := run(args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	client := hosted.NewClient(server, hosted.DefaultStore(), nil)
	ctx := context.Background()

	must("register", remoteID, "--def", "edge")
	if out := must("list", remoteID); !strings.Contains(out, "nightly") || !strings.Contains(out, "from-session") {
		t.Fatalf("list lacks the registered names:\n%s", out)
	}
	// satelle-server answers 502 "wake failed" when it records the fire but
	// cannot deliver the wake; that is its contract, so it passes when the fire
	// is on record under the trigger's name.
	records := func() []hosted.FireRecord {
		recs, err := client.ListFireRecords(ctx, remoteID)
		if err != nil {
			t.Fatal(err)
		}
		return recs
	}
	if out, err := checkNamedFire(run, records, remoteID, "nightly"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	must("pause", remoteID, "nightly")
	if out := must("list", remoteID); !strings.Contains(out, "true") {
		t.Errorf("list must show the paused trigger:\n%s", out)
	}
	if out, err := checkRefusedFire(run, records, remoteID, "nightly"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	must("resume", remoteID, "nightly")

	// Last, because a register replaces the declared set: a schedule trigger with
	// params.cron must come back from the server as sent, with a next fire time.
	t.Run("schedule", func(t *testing.T) {
		if os.Getenv("SATELLE_LIVE_SCHEDULE") != "1" {
			t.Skip("SATELLE_LIVE_SCHEDULE not set: satelle-server sty_3732a17d not yet reported live")
		}
		must("register", remoteID, "--def", "edge")
		triggers, err := client.ListDeclaredTriggers(ctx, remoteID)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range triggers {
			if d.Name != "nightly" {
				continue
			}
			if want := (map[string]string{"cron": "0 3 * * *"}); !reflect.DeepEqual(d.Params, want) {
				t.Fatalf("the server echoed params %v, want %v", d.Params, want)
			}
			if _, err := time.Parse(time.RFC3339, d.NextFireAt); err != nil {
				t.Fatalf("next_fire_at %q is not an RFC3339 time: %v", d.NextFireAt, err)
			}
			return
		}
		t.Fatalf("the server lists no trigger nightly: %+v", triggers)
	})
}
