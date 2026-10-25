//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// The epic-branch lane's two close checks (sty_b9d9a3df) read git for real — a
// TrunkRepos remote, a branch pushed to it, trunk moved or not — and read the
// work-item store through a `satelle` stub that answers in the shapes the real
// verbs print. The end-to-end proof against the real verbs and the governed
// transition is tests/epic_branch_lane_test.go.

const epicChildID = "sty_eee55555"

// laneStory is a work item as `satelle story get --json` / `story list --json`
// print it: one key per line, tags one per line.
type laneStory struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Category string   `json:"category"`
	ParentID string   `json:"parent_id,omitempty"`
	Tags     []string `json:"tags"`
}

// laneWorld is what the stub answers: the stories, and each container's member
// lines as `satelle story route` prints them.
type laneWorld struct {
	stories []laneStory
	members map[string][]string // container id -> "<id> — <status>"
}

// epicWorld is the child plus its epic-parent, the child carrying tags.
func epicWorld(tags ...string) laneWorld {
	return laneWorld{
		stories: []laneStory{
			{ID: "sty_eeec0001", Kind: "story", Category: "epic-parent", Tags: []string{"epic:t"}},
			{ID: epicChildID, Kind: "story", Category: "feature", Tags: append([]string{"epic:t"}, tags...)},
		},
		members: map[string][]string{"sty_eeec0001": {epicChildID + " — in_progress"}},
	}
}

// writeLaneStub installs bin/satelle in repo answering story get, story list,
// story route and trunk sync from the world.
func writeLaneStub(t *testing.T, repo string, w laneWorld) {
	t.Helper()
	data := filepath.Join(repo, "bin", "data")
	for _, s := range w.stories {
		b, _ := json.MarshalIndent(s, "", "  ")
		mustWrite(t, filepath.Join(data, "get-"+s.ID+".json"), string(b)+"\n")
		for _, tag := range s.Tags {
			// A list keeps every story carrying the tag, so rewrite it each time.
			var hit []laneStory
			for _, o := range w.stories {
				for _, ot := range o.Tags {
					if ot == tag {
						hit = append(hit, o)
						break
					}
				}
			}
			lb, _ := json.MarshalIndent(hit, "", "  ")
			mustWrite(t, filepath.Join(data, "list-"+strings.ReplaceAll(tag, ":", "_")+".json"), string(lb)+"\n")
		}
	}
	for c, lines := range w.members {
		body := "# Route — " + c + "\n\n## Members\n\n"
		for _, l := range lines {
			body += "- " + l + "\n"
		}
		mustWrite(t, filepath.Join(data, "route-"+c+".txt"), body)
	}
	mustWrite(t, filepath.Join(data, "sync.json"),
		"{\n  \"state\": \"level\",\n  \"remote\": \"origin\",\n  \"trunk\": \"main\"\n}\n")
	script := `#!/usr/bin/env bash
d="$(dirname "$0")/data"
case "$1 $2" in
  "story get") cat "$d/get-$3.json" ;;
  "story list") cat "$d/list-${4//:/_}.json" ;;
  "story route") cat "$d/route-$3.txt" ;;
  "trunk sync") cat "$d/sync.json" ;;
  *) echo "unexpected: $*" >&2; exit 99 ;;
esac
`
	shim := filepath.Join(repo, "bin", "satelle")
	mustWrite(t, shim, script)
	if err := os.Chmod(shim, 0o755); err != nil {
		t.Fatal(err)
	}
}

// laneRepo is the subject clone of a fresh TrunkRepos remote adopted into repo.
func laneRepo(t *testing.T, repo string) testutil.TrunkRepos {
	t.Helper()
	r := testutil.NewTrunkRepos(t)
	return r.Adopt(t, repo)
}

// pushBranch commits one file on a new branch epic/<id>, pushes the branch only
// and returns to trunk, leaving trunk untouched.
func pushBranch(t *testing.T, r testutil.TrunkRepos, id string) {
	t.Helper()
	r.Git(t, r.Subject, "checkout", "--quiet", "-b", "epic/"+id)
	r.Commit(t, r.Subject, id+".txt", id+"\n")
	r.Git(t, r.Subject, "push", "--quiet", "origin", "epic/"+id)
	r.Git(t, r.Subject, "checkout", "--quiet", "main")
}

// mergeAndPublish merges epic/<id> into trunk with --no-ff and pushes trunk.
func mergeAndPublish(t *testing.T, r testutil.TrunkRepos, id string) {
	t.Helper()
	r.Git(t, r.Subject, "merge", "--quiet", "--no-ff", "-m", "merge epic/"+id, "epic/"+id)
	r.Git(t, r.Subject, "push", "--quiet", "origin", "main")
}

// laneSetup composes a world with the git steps that follow it.
func laneSetup(w laneWorld, git func(*testing.T, testutil.TrunkRepos)) func(*testing.T, string) {
	return func(t *testing.T, repo string) {
		t.Helper()
		r := laneRepo(t, repo)
		writeLaneStub(t, repo, w)
		if git != nil {
			git(t, r)
		}
	}
}

func childrenPayload(container string, members ...string) string {
	return fmt.Sprintf(`{"story":{"id":"%s","tags":["epic:t"]},"from":"merging","to":"done","children":[%s]}`,
		container, strings.Join(members, ","))
}

func memberJSON(id, status string) string {
	return fmt.Sprintf(`{"id":"%s","status":"%s"}`, id, status)
}

func init() {
	pushed := func(t *testing.T, r testutil.TrunkRepos) { pushBranch(t, r, epicChildID) }
	parentWorld := laneWorld{
		stories: []laneStory{
			{ID: "sty_eeeb0001", Kind: "story", Category: "parent", Tags: []string{}},
			{ID: epicChildID, Kind: "story", Category: "feature", ParentID: "sty_eeeb0001", Tags: []string{}},
		},
		members: map[string][]string{"sty_eeeb0001": {epicChildID + " — in_progress"}},
	}

	fenceFixtures["satelle-epic-branch-check"] = []fenceCase{
		{
			name:       "accept a member whose branch is pushed and not on trunk",
			sid:        epicChildID,
			pathPrefix: "bin",
			setup:      laneSetup(epicWorld(), pushed),
			wantExit:   0,
			wantStdout: "not yet on main",
		},
		{
			name:       "reject a member with no pushed branch",
			sid:        epicChildID,
			pathPrefix: "bin",
			setup:      laneSetup(epicWorld(), nil),
			wantExit:   1,
			wantStdout: "has no epic/" + epicChildID,
		},
		{
			name: "reject a member whose branch is already on trunk",
			sid:  epicChildID, pathPrefix: "bin",
			setup: laneSetup(epicWorld(), func(t *testing.T, r testutil.TrunkRepos) {
				pushBranch(t, r, epicChildID)
				mergeAndPublish(t, r, epicChildID)
			}),
			wantExit:   1,
			wantStdout: "already on main",
		},
		{
			name:       "accept a lane:trunk member without a branch",
			sid:        epicChildID,
			pathPrefix: "bin",
			setup:      laneSetup(epicWorld("lane:trunk"), nil),
			wantExit:   0,
			wantStdout: "trunk lane",
		},
		{
			name: "accept a story in no container",
			sid:  epicChildID, pathPrefix: "bin",
			setup: laneSetup(laneWorld{stories: []laneStory{
				{ID: epicChildID, Kind: "story", Category: "feature", Tags: []string{}},
			}}, nil),
			wantExit:   0,
			wantStdout: "in no container",
		},
		{
			name:       "accept a parent's child by parent_id when its branch is pushed",
			sid:        epicChildID,
			pathPrefix: "bin",
			setup:      laneSetup(parentWorld, pushed),
			wantExit:   0,
			wantStdout: "not yet on main",
		},
		{
			name:       "reject a parent's child by parent_id with no branch",
			sid:        epicChildID,
			pathPrefix: "bin",
			setup:      laneSetup(parentWorld, nil),
			wantExit:   1,
			wantStdout: "has no epic/" + epicChildID,
		},
	}

	merged := func(w laneWorld, git func(*testing.T, testutil.TrunkRepos)) func(*testing.T, string) {
		return laneSetup(w, git)
	}
	const container = "sty_eeec0001"
	fenceFixtures["satelle-epic-merged-check"] = []fenceCase{
		{
			name:       "reject while a branch-lane member's branch is not on trunk",
			sid:        container,
			stdin:      childrenPayload(container, memberJSON(epicChildID, "done")),
			pathPrefix: "bin",
			setup:      merged(epicWorld(), pushed),
			wantExit:   1,
			wantStdout: epicChildID + ": epic/" + epicChildID,
		},
		{
			name:       "accept once the member's branch is merged and published",
			sid:        container,
			stdin:      childrenPayload(container, memberJSON(epicChildID, "done")),
			pathPrefix: "bin",
			setup: merged(epicWorld(), func(t *testing.T, r testutil.TrunkRepos) {
				pushBranch(t, r, epicChildID)
				mergeAndPublish(t, r, epicChildID)
			}),
			wantExit:   0,
			wantStdout: "1 branch-lane member(s)",
		},
		{
			name:       "reject a member with no branch on the remote",
			sid:        container,
			stdin:      childrenPayload(container, memberJSON(epicChildID, "done")),
			pathPrefix: "bin",
			setup:      merged(epicWorld(), nil),
			wantExit:   1,
			wantStdout: "has no epic/" + epicChildID,
		},
		{
			name:       "skip a lane:trunk member",
			sid:        container,
			stdin:      childrenPayload(container, memberJSON(epicChildID, "done")),
			pathPrefix: "bin",
			setup:      merged(epicWorld("lane:trunk"), nil),
			wantExit:   0,
			wantStdout: "0 branch-lane member(s)",
		},
		{
			name:       "skip a cancelled member",
			sid:        container,
			stdin:      childrenPayload(container, memberJSON(epicChildID, "cancelled")),
			pathPrefix: "bin",
			setup:      merged(epicWorld(), nil),
			wantExit:   0,
			wantStdout: "0 branch-lane member(s)",
		},
		{
			name:       "accept a container with no members",
			sid:        container,
			stdin:      `{"story":{"id":"` + container + `"},"from":"merging","to":"done"}`,
			pathPrefix: "bin",
			setup:      merged(epicWorld(), nil),
			wantExit:   0,
			wantStdout: "nothing to merge",
		},
	}
}
