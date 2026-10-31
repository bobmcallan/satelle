package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// engagedForName creates a repo (cwd) and files a performing story engaged from
// tree ("" = the repo itself). It returns the story id.
func engagedForName(t *testing.T, tree string) string {
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
	recordEngagementOn(t, db, story.ID, "in_progress", tree, "")
	return story.ID
}

// The story segment names the story engaged FROM THIS TREE: a story engaged from
// another worktree is not this tree's, so sameTree keeps it out of this tree's
// name.
func TestSessionNameStorySegment(t *testing.T) {
	cases := []struct {
		name      string
		otherTree bool
		wantStory bool
	}{
		{"engaged from this tree", false, true},
		{"engaged from a different worktree", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree := ""
			if c.otherTree {
				tree = t.TempDir()
			}
			id := engagedForName(t, tree)
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
