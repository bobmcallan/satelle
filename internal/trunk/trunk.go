// Package trunk reports how a repository's local trunk branch stands against
// its remote, and — only when asked and only when it is safe — brings in the
// commits other machines have already published (sty_9f3e51d1).
//
// It is mechanism: it names a state and never decides what the caller does
// about it. The engage path, `satelle trunk sync` and the release and
// worktree-cutting stories all call this one unit, so they cannot disagree
// about what "behind" or "dirty" mean. It runs plain git only; the trunk name
// comes from the remote's HEAD ref (or the caller's Options.Branch), never from
// a constant.
package trunk

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/worktree"
)

// State is how local trunk stands against the remote's. Exactly one holds.
type State string

const (
	// Level: local trunk and the remote's are the same commit.
	Level State = "level"
	// Behind: the remote has commits local trunk lacks (and not the reverse).
	Behind State = "behind"
	// Ahead: local trunk has unpushed commits (and the remote has none new).
	Ahead State = "ahead"
	// Diverged: each side has commits the other lacks.
	Diverged State = "diverged"
	// Dirty: the working tree that has trunk checked out has uncommitted changes.
	Dirty State = "dirty"
	// Offline: the remote could not be fetched.
	Offline State = "offline"
	// Skipped: there is nothing to compare — not a git repo, no remote, or no
	// resolvable trunk.
	Skipped State = "skipped"
)

// DefaultRemote is the remote used when Options.Remote is empty.
const DefaultRemote = "origin"

// fetchTimeout bounds the one network call, so an unreachable remote that
// hangs reports offline instead of holding an engage.
const fetchTimeout = 30 * time.Second

// Options selects the remote and trunk and whether a safe fast-forward may run.
type Options struct {
	// Remote defaults to DefaultRemote.
	Remote string
	// Branch names the trunk when the remote's HEAD ref does not.
	Branch string
	// FastForward lets Check advance trunk with `merge --ff-only`. It never
	// does anything else to the tree.
	FastForward bool
}

// Report is what Check found. State is the one-word answer; the rest is the
// evidence behind it.
type Report struct {
	State  State  `json:"state"`
	Reason string `json:"reason,omitempty"`
	// Ahead and Behind are local trunk's commits the remote lacks, and the
	// reverse. After a fast-forward, Behind is the number of commits brought in.
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
	Remote string `json:"remote,omitempty"`
	Trunk  string `json:"trunk,omitempty"`
	// FastForwarded is true only when this call moved trunk.
	FastForwarded bool `json:"fast_forwarded"`
	// From and To are local trunk's sha and the remote's. After a fast-forward
	// they are the old and the new tip.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Quiet reports whether the state needs no word to the operator: nothing to
// compare, or nothing different.
func (r Report) Quiet() bool { return r.State == "" || r.State == Level || r.State == Skipped }

// Line renders the one-line report. The engage path and `satelle trunk sync`
// print this same text, and the ledger row's body is it.
func (r Report) Line() string { return "satelle: trunk " + r.Detail() }

// Detail is Line without its "satelle: trunk " prefix.
func (r Report) Detail() string {
	switch r.State {
	case Level:
		return fmt.Sprintf("%s level with %s/%s", r.Trunk, r.Remote, r.Trunk)
	case Skipped:
		return "skipped: " + oneLine(r.Reason)
	case Offline:
		return fmt.Sprintf("fetch from %s failed (proceeding): %s", r.Remote, oneLine(r.Reason))
	case Dirty:
		return fmt.Sprintf("dirty tree on %s", r.Trunk)
	case Ahead:
		return fmt.Sprintf("%d unpushed commit(s)", r.Ahead)
	case Diverged:
		return fmt.Sprintf("diverged: %d ahead, %d behind", r.Ahead, r.Behind)
	case Behind:
		if r.FastForwarded {
			return fmt.Sprintf("fast-forwarded %s by %d commit(s) %s..%s", r.Trunk, r.Behind, short(r.From), short(r.To))
		}
		return fmt.Sprintf("behind %s/%s by %d, not moved: %s", r.Remote, r.Trunk, r.Behind, oneLine(r.Reason))
	}
	return string(r.State)
}

// Check compares dir's repository's local trunk with its remote's.
//
// The working tree that has trunk checked out is inspected for uncommitted
// changes BEFORE anything touches the network, so a dirty trunk is reported
// even when the remote is unreachable. The only git writes are the fetch of
// trunk's tracking ref and, when opts.FastForward holds and the state is
// Behind and dir's own working tree has trunk checked out and clean, one
// `merge --ff-only`. In every other case the trunk ref and the working tree are
// untouched: Check never merges, rebases, resets, stashes or commits.
func Check(ctx context.Context, dir string, opts Options) Report {
	rep := Report{Remote: opts.Remote}
	if rep.Remote == "" {
		rep.Remote = DefaultRemote
	}
	top, err := worktree.TopLevel(ctx, dir)
	if err != nil {
		return skipped(rep, "not a git repository: "+dir)
	}
	remotes, err := git(ctx, top, "remote")
	if err != nil {
		return skipped(rep, err.Error())
	}
	have := strings.Fields(remotes)
	if len(have) == 0 {
		return skipped(rep, "no remote configured")
	}
	if !contains(have, rep.Remote) {
		return skipped(rep, fmt.Sprintf("remote %s is not configured", rep.Remote))
	}
	rep.Trunk = trunkName(ctx, top, rep.Remote, opts.Branch)
	if rep.Trunk == "" {
		return skipped(rep, fmt.Sprintf("cannot resolve trunk: %s has no HEAD ref and no branch was given", rep.Remote))
	}
	localRef := "refs/heads/" + rep.Trunk
	remoteRef := "refs/remotes/" + rep.Remote + "/" + rep.Trunk

	// Dirty first: decided from the checkout that holds trunk, before any fetch.
	checkout := trunkCheckout(ctx, top, localRef)
	if checkout != "" {
		status, serr := git(ctx, checkout, "status", "--porcelain")
		if serr != nil {
			return skipped(rep, serr.Error())
		}
		if status != "" {
			rep.State = Dirty
			rep.Reason = fmt.Sprintf("uncommitted changes in %s", checkout)
			return rep
		}
	}

	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	_, ferr := git(fctx, top, "fetch", "--quiet", rep.Remote, "+refs/heads/"+rep.Trunk+":"+remoteRef)
	if ferr != nil {
		// The last-known counts come from the tracking ref the previous fetch
		// left, when there is one. Nothing is changed.
		rep.State = Offline
		rep.Reason = ferr.Error()
		rep.Ahead, rep.Behind, rep.From, rep.To, _ = compare(ctx, top, localRef, remoteRef)
		return rep
	}
	var cerr error
	rep.Ahead, rep.Behind, rep.From, rep.To, cerr = compare(ctx, top, localRef, remoteRef)
	if cerr != nil {
		return skipped(rep, cerr.Error())
	}
	switch {
	case rep.Ahead == 0 && rep.Behind == 0:
		rep.State = Level
	case rep.Behind == 0:
		rep.State = Ahead
	case rep.Ahead == 0:
		rep.State = Behind
		fastForward(ctx, top, checkout, remoteRef, opts.FastForward, &rep)
	default:
		rep.State = Diverged
	}
	return rep
}

// fastForward moves trunk to the remote's tip when, and only when, dir's own
// working tree has trunk checked out (clean, by the check above). It records
// why it did not move otherwise.
func fastForward(ctx context.Context, top, checkout, remoteRef string, want bool, rep *Report) {
	switch {
	case !want:
		rep.Reason = "fast-forward not requested"
	case checkout == "":
		rep.Reason = fmt.Sprintf("%s is not checked out in any working tree", rep.Trunk)
	case !sameDir(checkout, top):
		rep.Reason = fmt.Sprintf("%s is checked out in %s, not in this working tree", rep.Trunk, checkout)
	default:
		if _, err := git(ctx, top, "merge", "--ff-only", "--quiet", remoteRef); err != nil {
			rep.Reason = err.Error()
			return
		}
		rep.FastForwarded = true
	}
}

// compare counts the commits each side has that the other lacks and returns
// both tips. An error means a ref is missing — no local trunk branch, or no
// tracking ref yet.
func compare(ctx context.Context, top, localRef, remoteRef string) (ahead, behind int, from, to string, err error) {
	from, err = git(ctx, top, "rev-parse", "--verify", "--quiet", localRef)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("no local branch %s", strings.TrimPrefix(localRef, "refs/heads/"))
	}
	to, err = git(ctx, top, "rev-parse", "--verify", "--quiet", remoteRef)
	if err != nil {
		return 0, 0, from, "", fmt.Errorf("no remote-tracking ref %s", strings.TrimPrefix(remoteRef, "refs/remotes/"))
	}
	out, err := git(ctx, top, "rev-list", "--left-right", "--count", localRef+"..."+remoteRef)
	if err != nil {
		return 0, 0, from, to, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, from, to, fmt.Errorf("unexpected rev-list output %q", out)
	}
	ahead, _ = strconv.Atoi(f[0])
	behind, _ = strconv.Atoi(f[1])
	return ahead, behind, from, to, nil
}

// trunkName is the remote's default branch (its HEAD ref), else fallback.
func trunkName(ctx context.Context, top, remote, fallback string) string {
	if out, err := git(ctx, top, "symbolic-ref", "--short", "-q", "refs/remotes/"+remote+"/HEAD"); err == nil {
		if name := strings.TrimPrefix(out, remote+"/"); name != "" && name != out {
			return name
		}
	}
	return fallback
}

// trunkCheckout is the working tree that has localRef checked out ("" if none),
// read from `git worktree list`, symlink-resolved like worktree.TopLevel.
func trunkCheckout(ctx context.Context, top, localRef string) string {
	out, err := git(ctx, top, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	var path string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "branch "+localRef && path != "":
			if resolved, rerr := filepath.EvalSymlinks(path); rerr == nil {
				path = resolved
			}
			return filepath.Clean(path)
		}
	}
	return ""
}

func sameDir(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func skipped(rep Report, reason string) Report {
	rep.State = Skipped
	rep.Reason = reason
	return rep
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// oneLine folds git's multi-line error text into the single line the report
// promises.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// git runs git in dir and returns trimmed stdout. On failure the error carries
// git's own stderr text. GIT_DIR and friends are dropped from the environment
// so a caller inside a hook cannot redirect the check to another repository,
// and prompts are off so an unreachable remote cannot wait on a terminal.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// shell runs a caller-configured command (a proof, a stamp) with `sh -c` in dir
// and returns its combined output. It carries the same environment as git, so a
// hook caller cannot redirect it to another repository.
func shell(ctx context.Context, dir, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_PREFIX",
			"GIT_TERMINAL_PROMPT", "LC_ALL":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}
