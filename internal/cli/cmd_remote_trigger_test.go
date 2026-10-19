package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			if old := s.find(t.Name); old != nil {
				t.Paused = old.Paused
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
		s.fires = append(s.fires, hosted.FireRecord{ID: id, Trigger: in.Trigger})
		if in.Trigger != "" {
			t := s.find(in.Trigger)
			if t == nil || t.Paused {
				reason := "trigger is not declared"
				if t != nil {
					reason = "trigger is paused"
				}
				s.reply(w, http.StatusConflict, map[string]string{"error": "trigger_refused", "message": reason, "trigger_id": id})
				return
			}
		}
		if s.wakeURL != "" {
			wake, _ := json.Marshal(map[string]string{
				"trigger_id": id, "remote_id": stubRemoteID, "remote": "edge", "story_id": in.StoryID, "trigger": in.Trigger,
			})
			req, _ := http.NewRequest(http.MethodPost, s.wakeURL, bytes.NewReader(wake))
			req.Header.Set("Authorization", "Bearer wake-credential")
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}
		s.reply(w, http.StatusOK, map[string]string{"trigger_id": id})
	case rest == "/triggers" && r.Method == http.MethodGet:
		s.reply(w, http.StatusOK, s.fires)
	default:
		http.NotFound(w, r)
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
	five := "[[trigger]]\nname = \"nightly\"\nkind = \"schedule\"\n\n" +
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
		want := `{"triggers":[{"name":"nightly","kind":"schedule"},{"name":"new-story","kind":"story-event"},{"name":"epic-drive","kind":"handoff"},{"name":"on-push","kind":"inbound-event"},{"name":"from-session","kind":"mcp"}]}`
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
	for _, want := range []string{"NAME", "PAUSED", "nightly", "schedule", "from-session", "mcp"} {
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
	fireFor := func(trigger string) (hosted.FireRecord, bool) {
		recs, err := client.ListFireRecords(ctx, remoteID)
		if err != nil {
			t.Fatal(err)
		}
		for i := len(recs) - 1; i >= 0; i-- {
			if recs[i].Trigger == trigger {
				return recs[i], true
			}
		}
		return hosted.FireRecord{}, false
	}

	must("register", remoteID, "--def", "edge")
	if out := must("list", remoteID); !strings.Contains(out, "nightly") || !strings.Contains(out, "from-session") {
		t.Fatalf("list lacks the registered names:\n%s", out)
	}
	must("fire", remoteID, "nightly")
	if rec, ok := fireFor("nightly"); !ok || rec.FireID() == "" {
		t.Fatalf("the fire record must carry the trigger name nightly, got %+v (found %v)", rec, ok)
	}
	must("pause", remoteID, "nightly")
	if out := must("list", remoteID); !strings.Contains(out, "true") {
		t.Errorf("list must show the paused trigger:\n%s", out)
	}
	before, _ := fireFor("nightly")
	out, err := run("fire", remoteID, "nightly")
	if err == nil || !strings.Contains(err.Error(), "trigger nightly refused") {
		t.Fatalf("a paused trigger must be refused with its 409, got %v\n%s", err, out)
	}
	if after, ok := fireFor("nightly"); !ok || after.FireID() == before.FireID() {
		t.Errorf("the refused fire must be recorded: before %+v after %+v", before, after)
	}
	must("resume", remoteID, "nightly")
}
