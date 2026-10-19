package cli

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// fireRequests is the fire calls the stub server saw.
func fireRequests(s *stubServer) []seenRequest {
	var out []seenRequest
	for _, r := range s.requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/fire") {
			out = append(out, r)
		}
	}
	return out
}

// storySnapshot is everything a refused run must leave alone: the story's record
// and its ledger.
func storySnapshot(t *testing.T, id string) string {
	t.Helper()
	story, err := runRoot(t, "story", "get", id)
	if err != nil {
		t.Fatalf("story get: %v\n%s", err, story)
	}
	led, err := runRoot(t, "ledger", "list", "--story", id)
	if err != nil {
		t.Fatalf("ledger list: %v\n%s", err, led)
	}
	return story + "\n--\n" + led
}

// createRemoteEnteringAt scaffolds a remote whose segment enters at entry.
func createRemoteEnteringAt(t *testing.T, name, entry string) {
	t.Helper()
	if out, err := runRoot(t, "remote", "create", name); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	path := filepath.Join(remotedef.Dir(remoteDataDir(), name), "remote.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := "entry = \"backlog\"\nexit = \"done\""
	if !strings.Contains(string(b), old) {
		t.Fatalf("scaffold lacks the default segment:\n%s", b)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, "entry = \""+entry+"\"\nexit = \"done\"", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScaffoldedRemoteIsNotRefusedBySeatRule: a remote exactly as `remote create`
// writes it, against the repo's default local route, accepts a story at its
// segment's entry and the seat rule leaves its hand-off alone.
func TestScaffoldedRemoteIsNotRefusedBySeatRule(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	if out, err := runRoot(t, "remote", "create", "fresh"); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	def, err := loadRemoteDef("fresh")
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Segments) != 1 {
		t.Fatalf("want the scaffold's one default segment, got %+v", def.Segments)
	}
	seg := def.Segments[0]

	id := newStory(t, seg.Entry)
	ctx := context.Background()
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	if _, err := def.Accepts(seg.Entry, ""); err != nil {
		t.Errorf("the scaffold's segment refuses a story at its own entry %s: %v", seg.Entry, err)
	}
	local := localRoute{a.Store.Stories, a.Store.DocIndex}
	reason, err := remotedef.SeatReason(ctx, local, id, seg, seg.Entry)
	if err != nil || reason != "" {
		t.Errorf("SeatReason = %q, %v; the scaffold's default segment (%s → %s) must hold no seat on the default local route", reason, err, seg.Entry, seg.Exit)
	}
}

// TestRemoteRunSelectsAcceptingRemote: of two defined remotes only the one whose
// segment takes the story's status is selected and printed.
func TestRemoteRunSelectsAcceptingRemote(t *testing.T) {
	remoteWithTriggers(t) // edge enters at backlog
	createRemoteEnteringAt(t, "other", "in_progress")
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", id, "--server", srv.ts.URL)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "remote edge: segment backlog → done") {
		t.Errorf("the accepting remote is not printed:\n%s", out)
	}
	if strings.Contains(out, "remote other") {
		t.Errorf("a remote whose segment does not take the story was selected:\n%s", out)
	}
	if n := len(fireRequests(srv)); n != 0 {
		t.Errorf("without --remote-id nothing is fired, got %d fire request(s)", n)
	}
}

// TestRemoteRunNoRemote: no remote defined, or none accepting the story, is
// answered and changes nothing.
func TestRemoteRunNoRemote(t *testing.T) {
	for _, c := range []struct {
		name   string
		define bool
	}{{"no remotes defined", false}, {"no remote accepts the story", true}} {
		t.Run(c.name, func(t *testing.T) {
			if c.define {
				remoteWithTriggers(t) // edge enters at backlog; the story is at plan
			} else {
				repo := tempRepo(t)
				t.Chdir(repo)
				t.Setenv(hosted.SessionTokenEnv, "stub-token")
			}
			srv := newStubServer(t, "")
			id := newStory(t, "plan")
			before := storySnapshot(t, id)

			out, err := runRoot(t, "remote", "run", id, "--server", srv.ts.URL)
			if err == nil || !strings.Contains(err.Error(), "No remote; either configure one, or stop") {
				t.Fatalf("want the no-remote answer, got %v\n%s", err, out)
			}
			if after := storySnapshot(t, id); after != before {
				t.Errorf("the story changed:\nbefore: %s\nafter: %s", before, after)
			}
			if n := len(fireRequests(srv)); n != 0 {
				t.Errorf("got %d fire request(s)", n)
			}
		})
	}
}

// TestRemoteRunSignedOut: with no stored credential the answer names satelle
// login before any remote is selected, and nothing is sent.
func TestRemoteRunSignedOut(t *testing.T) {
	remoteWithTriggers(t)
	t.Setenv(hosted.SessionTokenEnv, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")
	before := storySnapshot(t, id)

	out, err := runRoot(t, "remote", "run", id, "--remote-id", stubRemoteID, "--server", srv.ts.URL)
	if err == nil || !strings.Contains(err.Error(), "satelle login") {
		t.Fatalf("want an answer naming satelle login, got %v\n%s", err, out)
	}
	if strings.Contains(out, "remote edge") {
		t.Errorf("a remote was selected while signed out:\n%s", out)
	}
	if after := storySnapshot(t, id); after != before {
		t.Errorf("the story changed:\nbefore: %s\nafter: %s", before, after)
	}
	if n := len(srv.requests()); n != 0 {
		t.Errorf("signed out, nothing may be sent, got %d request(s)", n)
	}
}

// TestRemoteRunRefusesSeatTakingSegment: a story whose status would hold an
// engagement seat locally is refused before anything is sent.
func TestRemoteRunRefusesSeatTakingSegment(t *testing.T) {
	remoteWithTriggers(t)
	createRemoteEnteringAt(t, "other", "in_progress")
	srv := newStubServer(t, "")
	id := newStory(t, "in_progress") // other enters at in_progress, an engaging state
	before := storySnapshot(t, id)

	out, err := runRoot(t, "remote", "run", id, "--remote-id", stubRemoteID, "--server", srv.ts.URL)
	if err == nil || !strings.Contains(err.Error(), "engagement seat") {
		t.Fatalf("want a refusal naming the engagement seat, got %v\n%s", err, out)
	}
	if after := storySnapshot(t, id); after != before {
		t.Errorf("the story changed:\nbefore: %s\nafter: %s", before, after)
	}
	if n := len(fireRequests(srv)); n != 0 {
		t.Errorf("got %d fire request(s)", n)
	}
}

// TestRemoteRunFires: with --remote-id the story goes through the fire path.
func TestRemoteRunFires(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", id, "--remote-id", stubRemoteID, "--server", srv.ts.URL)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	fires := fireRequests(srv)
	if len(fires) != 1 || fires[0].Body != `{"story_id":"`+id+`"}` {
		t.Fatalf("want one fire naming the story, got %+v", fires)
	}
	if !strings.Contains(out, "fired remote "+stubRemoteID) || !strings.Contains(out, "fire_1") {
		t.Errorf("the fire is not reported:\n%s", out)
	}
}

// TestRemoteRunPrintsCommand: without --remote-id the matching remote and the
// exact fire command are printed, and nothing is fired.
func TestRemoteRunPrintsCommand(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", id, "--server", srv.ts.URL)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{"remote edge: segment backlog → done", "satelle remote trigger fire <remote-id> --story " + id} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if n := len(fireRequests(srv)); n != 0 {
		t.Errorf("got %d fire request(s)", n)
	}
}
