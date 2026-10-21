package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/lease"
)

// seatForName creates a repo (cwd), files a performing story and seats it from
// tree ("" = the repo itself), optionally aged past the heartbeat TTL (stale). It
// returns the story id.
func seatForName(t *testing.T, tree string, stale bool) string {
	t.Helper()
	db, create := attributionRepo(t)
	if tree == "" {
		repo, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		tree = repo
	}
	story := create("named", "in_progress")
	ctx := context.Background()
	if _, _, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: story.ID, Kind: "story", Owner: lease.ResolveOwner(), State: "in_progress",
		StorySeat: true, Worktree: tree,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Leases.Confirm(ctx, story.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	if stale {
		if err := db.Leases.SetHeartbeat(ctx, story.ID, time.Now().UTC().Add(-2*lease.HeartbeatTTL)); err != nil {
			t.Fatal(err)
		}
	}
	return story.ID
}

// The story segment needs a LIVE seat held FROM THIS TREE: a stale seat names no
// story, and neither does a single live seat held in another worktree — with one
// live seat resolveSeatsFor answers "engaged" for any tree, so only sameTree keeps
// the other tree's story out of this tree's name.
func TestSessionNameStorySegment(t *testing.T) {
	cases := []struct {
		name      string
		otherTree bool
		stale     bool
		wantStory bool
	}{
		{"live seat in this tree", false, false, true},
		{"stale seat in this tree", false, true, false},
		{"single live seat in a different worktree", true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree := ""
			if c.otherTree {
				tree = t.TempDir()
			}
			id := seatForName(t, tree, c.stale)
			repo, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			out, err := runRoot(t, "session", "name")
			if err != nil {
				t.Fatal(err)
			}
			want := hosted.ShortHost() + " · " + config.RepoName(repo)
			if c.wantStory {
				want += " · " + id
			}
			if got := strings.TrimSpace(out); got != want {
				t.Fatalf("session name = %q, want %q", got, want)
			}
		})
	}
}

// A harness with no launch name flag (grok, cursor) records an explicit
// unavailable naming it; one that declares a flag (claude, pi) prints the name,
// pi included although it has no launcher.
func TestSessionNameHarness(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	for _, h := range []string{agentcli.HarnessGrok, agentcli.HarnessCursor} {
		out, err := runRoot(t, "session", "name", "--harness", h)
		if err != nil {
			t.Fatal(err)
		}
		if want := "title: unavailable (" + h + " has no launch name flag)"; strings.TrimSpace(out) != want {
			t.Errorf("%s: %q, want %q", h, out, want)
		}
	}
	for _, h := range []string{agentcli.HarnessClaude, agentcli.HarnessPi} {
		out, err := runRoot(t, "session", "name", "--harness", h)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "unavailable") || !strings.Contains(out, config.RepoName(repo)) {
			t.Errorf("%s: %q", h, out)
		}
	}
}
