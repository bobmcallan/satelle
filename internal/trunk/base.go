package trunk

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/worktree"
)

// BaseClass is what ClassifyBase made of a ref a caller is about to cut from or
// merge onto: whether it names the trunk, and when satelle cannot tell.
type BaseClass struct {
	// IsTrunk: the ref names the trunk in one of its four spellings.
	IsTrunk bool
	// Remote and Branch are the remote consulted and the resolved trunk name
	// (empty when unresolved or offline).
	Remote string
	Branch string
	// Unresolved: no trunk could be named, so no ref can be told from a
	// dependency branch. Offline: naming it needed the remote and the remote
	// could not be reached. Either way Stop is the report to apply.
	Unresolved bool
	Offline    bool
	Stop       Report
}

// ClassifyBase says whether base names dir's trunk. The trunk is resolved the way
// Check resolves it (the remote's HEAD ref, restored by `set-head --auto` when
// missing, else opts.Branch), so the two cannot disagree. base is recognised as
// <trunk>, refs/heads/<trunk>, <remote>/<trunk> or refs/remotes/<remote>/<trunk>;
// a name with another prefix, such as other/main when other is not the remote,
// is a bare branch of that name and so never the trunk. A repository with no
// such remote has no trunk to name: nothing is trunk and nothing is unresolved.
func ClassifyBase(ctx context.Context, dir, base string, opts Options) BaseClass {
	opts.ResolveHead = true
	bc := BaseClass{Remote: opts.Remote}
	if bc.Remote == "" {
		bc.Remote = DefaultRemote
	}
	if !HasRemote(ctx, dir, bc.Remote) {
		return bc
	}
	top, err := worktree.TopLevel(ctx, dir)
	if err != nil {
		return bc
	}
	name, stop := resolveTrunk(ctx, top, Report{Remote: bc.Remote}, opts)
	if stop != nil {
		bc.Stop = *stop
		bc.Unresolved, bc.Offline = stop.Unresolved, stop.State == Offline
		return bc
	}
	bc.Branch = name
	bc.IsTrunk = baseBranch(base, bc.Remote) == name
	return bc
}

// baseBranch strips a ref to the branch name it denotes.
func baseBranch(base, remote string) string {
	switch {
	case strings.HasPrefix(base, "refs/remotes/"+remote+"/"):
		return strings.TrimPrefix(base, "refs/remotes/"+remote+"/")
	case strings.HasPrefix(base, "refs/heads/"):
		return strings.TrimPrefix(base, "refs/heads/")
	case strings.HasPrefix(base, remote+"/"):
		return strings.TrimPrefix(base, remote+"/")
	}
	return base
}

// StopState is the key a caller's stop list is matched against for rep: the
// report's state, "unresolved" for a trunk that could not be named, and ""
// where nothing is wrong (level, nothing to compare, or a behind trunk the
// check brought in).
func StopState(rep Report) string {
	switch {
	case rep.Unresolved:
		return "unresolved"
	case rep.State == Level, rep.State == Skipped, rep.State == "":
		return ""
	case rep.State == Behind && rep.FastForwarded:
		return ""
	}
	return string(rep.State)
}

// Hint says how the operator clears a stopping state.
func (r Report) Hint() string {
	switch {
	case r.Unresolved:
		return fmt.Sprintf("pass --trunk-branch <name> or run git remote set-head %s --auto", r.Remote)
	}
	switch r.State {
	case Dirty:
		return fmt.Sprintf("commit or `git stash` the changes on %s, then run the command again", r.Trunk)
	case Diverged:
		return fmt.Sprintf("run `git pull --rebase %s %s` to replay your commits onto the remote's, then run the command again", r.Remote, r.Trunk)
	case Behind:
		return fmt.Sprintf("run `git pull --ff-only %s %s`, then run the command again", r.Remote, r.Trunk)
	case Ahead:
		return fmt.Sprintf("push them first: run `git push %s %s`, then run the command again", r.Remote, r.Trunk)
	case Offline:
		return fmt.Sprintf("restore access to %s, then run the command again (or list offline out of the stop set)", r.Remote)
	}
	return "reconcile the trunk, then run the command again"
}
