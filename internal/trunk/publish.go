package trunk

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/worktree"
)

// Publish is the one mechanism a release uses to put its commits on a trunk
// other machines have moved (sty_6af229f1): it brings the remote's trunk in,
// proves the combined head, pushes without force, and starts again when the
// push is refused because the trunk moved once more.
//
// It is mechanism. What "proved" means is the caller's Prove command and the
// version bump is the caller's Stamp command; neither is compiled in. Every git
// call goes through the same runner Check uses and no push carries a force
// option or a forced refspec.

// PublishOptions selects the remote and trunk and the commands a round runs.
type PublishOptions struct {
	// Remote defaults to DefaultRemote.
	Remote string
	// Branch names the trunk when the remote's HEAD ref does not.
	Branch string
	// Prove is a shell command run on the integrated (and stamped) tree. A
	// non-zero exit stops the publish. Required: there is no default proof.
	Prove string
	// Stamp is an optional shell command run on the integrated tree before
	// Prove. It computes the release's version bump from that tree and commits
	// it, so a bump never conflicts with another machine's.
	Stamp string
	// Rounds bounds how many times a refused push is answered by integrating
	// again. At least 1.
	Rounds int
}

// PublishReport is what Publish did. Error is empty exactly when the push
// succeeded.
type PublishReport struct {
	Remote string `json:"remote,omitempty"`
	Trunk  string `json:"trunk,omitempty"`
	// Base is the release head the publish started from; a failed publish leaves
	// the tree here.
	Base string `json:"base,omitempty"`
	// Integrated is the head after the remote's commits were brought in, before
	// Stamp ran, in the last round.
	Integrated string `json:"integrated,omitempty"`
	// Combined is the head the last proof ran on. It is the head that was pushed
	// when the publish succeeded.
	Combined string `json:"combined,omitempty"`
	// Pushed is the head the remote accepted; empty unless the push succeeded.
	Pushed string `json:"pushed,omitempty"`
	// Rounds is how many rounds ran; Incoming is how many remote commits the
	// last round brought in; Merged is whether it needed a merge or a
	// fast-forward at all.
	Rounds   int  `json:"rounds"`
	Incoming int  `json:"incoming"`
	Merged   bool `json:"merged"`
	// Proved is the head every successful proof ran on, in order.
	Proved   []string `json:"proved,omitempty"`
	ProveCmd string   `json:"prove_cmd,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// OK reports whether the push succeeded.
func (r PublishReport) OK() bool { return r.Error == "" }

// Line renders the one-line report; the ledger row's body is it.
func (r PublishReport) Line() string {
	if !r.OK() {
		return "satelle: trunk publish failed: " + oneLine(r.Error)
	}
	return fmt.Sprintf("satelle: trunk published %s (combined %s, rounds %d, incoming %d, proved by %s)",
		r.Pushed, r.Combined, r.Rounds, r.Incoming, oneLine(r.ProveCmd))
}

// Publish integrates dir's trunk with the remote's, proves it, and pushes it.
//
// Each round fetches the remote's trunk and, when it has commits local trunk
// lacks, merges it in (a merge, never a rebase: no commit the release made is
// rewritten, so an epic's child merges stay in the pushed history). Then Stamp
// runs, then Prove, then one plain push. A push refused because the trunk moved
// puts the tree back at the release head and runs another round, up to
// opts.Rounds; the bound spent, the publish stops and nothing is forced. A
// merge conflict, a failed Stamp or Prove, or any other push error also puts
// the tree back and stops. Every failure leaves local trunk at the release head
// the publish began with and the remote untouched.
func Publish(ctx context.Context, dir string, opts PublishOptions) PublishReport {
	rep := PublishReport{Remote: opts.Remote, ProveCmd: opts.Prove}
	if rep.Remote == "" {
		rep.Remote = DefaultRemote
	}
	fail := func(format string, a ...any) PublishReport {
		rep.Error = fmt.Sprintf(format, a...)
		return rep
	}
	switch {
	case strings.TrimSpace(opts.Prove) == "":
		return fail("no configured proof for the combined tree: set [trunk] prove or pass --prove")
	case opts.Rounds < 1:
		return fail("rounds must be at least 1, got %d", opts.Rounds)
	}
	top, err := worktree.TopLevel(ctx, dir)
	if err != nil {
		return fail("not a git repository: %s", dir)
	}
	remotes, err := git(ctx, top, "remote")
	if err != nil || !contains(strings.Fields(remotes), rep.Remote) {
		return fail("remote %s is not configured", rep.Remote)
	}
	rep.Trunk = trunkName(ctx, top, rep.Remote, opts.Branch)
	if rep.Trunk == "" {
		return fail("cannot resolve trunk: %s has no HEAD ref and no branch was given", rep.Remote)
	}
	if cur, _ := git(ctx, top, "symbolic-ref", "--short", "-q", "HEAD"); cur != rep.Trunk {
		return fail("this working tree has %q checked out, not trunk %s", cur, rep.Trunk)
	}
	if status, serr := git(ctx, top, "status", "--porcelain", "--untracked-files=no"); serr != nil || status != "" {
		return fail("uncommitted changes on %s: commit them before publishing", rep.Trunk)
	}
	if rep.Base, err = git(ctx, top, "rev-parse", "HEAD"); err != nil {
		return fail("%v", err)
	}
	remoteRef := "refs/remotes/" + rep.Remote + "/" + rep.Trunk
	restore := func() { _, _ = git(ctx, top, "reset", "--hard", "--quiet", rep.Base) }

	for round := 1; round <= opts.Rounds; round++ {
		rep.Rounds = round
		fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		_, ferr := git(fctx, top, "fetch", "--quiet", rep.Remote, "+refs/heads/"+rep.Trunk+":"+remoteRef)
		cancel()
		if ferr != nil {
			return fail("fetch from %s failed: %v", rep.Remote, ferr)
		}
		_, behind, _, _, cerr := compare(ctx, top, "HEAD", remoteRef)
		if cerr != nil {
			return fail("%v", cerr)
		}
		rep.Incoming, rep.Merged = behind, behind > 0
		if behind > 0 {
			if _, merr := git(ctx, top, "merge", "--no-edit", "--quiet", remoteRef); merr != nil {
				// Only unmerged paths make a conflict; any other refusal (no
				// committer identity, a hook, a lock) is a failed merge.
				files, _ := git(ctx, top, "diff", "--name-only", "--diff-filter=U")
				_, _ = git(ctx, top, "merge", "--abort")
				restore()
				if paths := strings.Join(strings.Fields(files), ", "); paths != "" {
					return fail("slice conflicts with %s/%s: %s", rep.Remote, rep.Trunk, paths)
				}
				return fail("merge of %s/%s failed: %s", rep.Remote, rep.Trunk, oneLine(merr.Error()))
			}
		}
		if rep.Integrated, err = git(ctx, top, "rev-parse", "HEAD"); err != nil {
			restore()
			return fail("%v", err)
		}
		if opts.Stamp != "" {
			if out, serr := shell(ctx, top, opts.Stamp); serr != nil {
				restore()
				return fail("stamp failed: %v: %s", serr, tailLine(out))
			}
			if status, _ := git(ctx, top, "status", "--porcelain", "--untracked-files=no"); status != "" {
				restore()
				return fail("stamp left uncommitted changes; it must commit the bump it computes")
			}
		}
		head, err := git(ctx, top, "rev-parse", "HEAD")
		if err != nil {
			restore()
			return fail("%v", err)
		}
		if out, perr := shell(ctx, top, opts.Prove); perr != nil {
			restore()
			return fail("proof failed on combined head %s: %v: %s", head, perr, tailLine(out))
		}
		if now, _ := git(ctx, top, "rev-parse", "HEAD"); now != head {
			restore()
			return fail("proof moved HEAD from %s to %s; it must prove the head, not change it", head, now)
		}
		rep.Combined = head
		rep.Proved = append(rep.Proved, head)

		_, perr := git(ctx, top, "push", rep.Remote, "HEAD:refs/heads/"+rep.Trunk)
		if perr == nil {
			rep.Pushed = head
			return rep
		}
		restore()
		if !refusedForMovedTrunk(perr) {
			return fail("push to %s/%s failed: %v", rep.Remote, rep.Trunk, perr)
		}
	}
	return fail("%s/%s moved %d times; not pushed (bound %d)", rep.Remote, rep.Trunk, opts.Rounds, opts.Rounds)
}

// refusedForMovedTrunk reports whether a failed push was the remote declining a
// non-fast-forward: the trunk moved after the fetch, so another round answers it.
func refusedForMovedTrunk(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "fetch first") ||
		strings.Contains(msg, "[rejected]")
}

// tailLine is the last of a command's output on one line, so a failed proof's
// message carries its ending and not a whole test log.
func tailLine(out string) string {
	const keep = 400
	out = oneLine(out)
	if len(out) > keep {
		return "..." + out[len(out)-keep:]
	}
	return out
}
