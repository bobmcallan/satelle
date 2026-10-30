package cli

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/trunk"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/worktree"
)

// After a work-state pull, a story's status is a claim about code. This report
// names each pulled story whose claim the git remote cannot be shown to back, so
// the agent knows what it can continue and what is stranded (sty_78e20d15). It
// changes nothing: the route and the ledger say what a story claims, git says
// whether the code is there, and what to do about a gap is the operator's call.
// Git backs a claim either through the recorded head or through a commit on the
// remote trunk whose subject names the story — a head rebased or squashed onto
// trunk, or never recorded, still leaves the story's code there (sty_e5cba77e).
// Like the push hold it never touches the network — "on the remote" is whatever
// the remote-tracking refs say as of the last fetch.

// headProbe is one recorded head's relation to this repository, asked once per sha.
type headProbe struct {
	inRepo   bool
	onRemote bool
	err      error
}

func probeHead(dir, sha string) headProbe {
	has, err := worktree.HasCommit(dir, sha)
	if err != nil || !has {
		return headProbe{err: err}
	}
	on, err := worktree.OnRemote(dir, sha)
	return headProbe{inRepo: true, onRemote: on, err: err}
}

// reconcilePulledStories returns the report lines for the given story ids: one
// line per in-flight story and per story whose post-work head is missing or off
// the remote, then one summary line counting the stories that left work with no
// recorded head (listed one per line when verbose). A story a remote trunk commit
// names is in none of them.
func reconcilePulledStories(ctx context.Context, a *app.App, ids []string, verbose bool) []string {
	var lines, noHead []string
	probes := map[string]headProbe{}
	gitErr := ""
	trunkRef, trunkResolved := "", false
	onTrunk := func(id string) bool {
		if !trunkResolved {
			trunkRef, trunkResolved = trunk.RemoteTrunkRef(ctx, a.RepoRoot, a.PlaneConfig().Trunk.Branch), true
		}
		if trunkRef == "" || gitErr != "" {
			return false
		}
		named, err := worktree.TrunkCommitNames(a.RepoRoot, trunkRef, id)
		if err != nil {
			gitErr = err.Error()
		}
		return named
	}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(ids))) {
		it, err := a.Store.Stories.Get(ctx, id)
		if err != nil {
			continue // not a story on this machine: nothing to check
		}
		ws, err := verb.StoryWorkState(ctx, it)
		if err != nil {
			lines = append(lines, fmt.Sprintf("reachability unavailable (%s): %s", err, id))
			continue
		}
		switch {
		case ws.Skip:
		case ws.Unavailable != "":
			lines = append(lines, fmt.Sprintf("reachability unavailable (%s): %s", ws.Unavailable, id))
		case ws.InFlight:
			if onTrunk(id) {
				continue
			}
			since := ws.EnteredAt
			if since.IsZero() {
				since = it.UpdatedAt
			}
			where := ws.BaselineTree
			if where == "" {
				where = "the machine that engaged it"
			}
			lines = append(lines, fmt.Sprintf("stranded %s (%s): in %s since %s — no commit recorded after work started; its code may exist only in %s",
				id, it.Status, it.Status, since.UTC().Format(time.RFC3339), where))
		case !ws.LeftWork:
		case ws.PostWorkHead == "":
			if !onTrunk(id) {
				noHead = append(noHead, id)
			}
		case gitErr != "":
		default:
			p, ok := probes[ws.PostWorkHead]
			if !ok {
				p = probeHead(a.RepoRoot, ws.PostWorkHead)
				probes[ws.PostWorkHead] = p
			}
			switch {
			case p.err != nil:
				gitErr = p.err.Error()
			case (!p.inRepo || !p.onRemote) && onTrunk(id):
			case !p.inRepo:
				lines = append(lines, fmt.Sprintf("stranded %s (%s): head %s is not in this repository", id, it.Status, short8(ws.PostWorkHead)))
			case !p.onRemote:
				lines = append(lines, fmt.Sprintf("stranded %s (%s): head %s is not on any remote branch", id, it.Status, short8(ws.PostWorkHead)))
			}
		}
	}
	if gitErr != "" {
		lines = append(lines, fmt.Sprintf("reachability unavailable (%s)", gitErr))
	}
	if len(noHead) > 0 {
		if verbose {
			lines = append(lines, fmt.Sprintf("%d stories left work with no recorded head:", len(noHead)))
			for _, id := range noHead {
				lines = append(lines, "  "+id)
			}
		} else {
			lines = append(lines, fmt.Sprintf("%d stories left work with no recorded head (--verbose to list)", len(noHead)))
		}
	}
	return lines
}
