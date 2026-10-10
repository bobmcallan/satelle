package trunk_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/trunk"
)

// snapshot is everything Check promises not to change unless it fast-forwards:
// the trunk ref and the porcelain status of the tree that holds it. The
// porcelain is git's output byte for byte (rawGit), not trimmed.
type snapshot struct{ ref, porcelain string }

func take(t *testing.T, r testutil.TrunkRepos) snapshot {
	t.Helper()
	return snapshot{
		ref:       r.Git(t, r.Subject, "rev-parse", "refs/heads/main"),
		porcelain: rawGit(t, r.Subject, "status", "--porcelain"),
	}
}

// rawGit runs git in dir and returns its stdout untouched, so a comparison of
// two calls is byte for byte.
func rawGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %s (in %s): %v", strings.Join(args, " "), dir, err)
	}
	return string(out)
}

// repoSnapshot is every local ref and the raw porcelain status of dir: what a
// skipped check must leave exactly as it found.
func repoSnapshot(t *testing.T, dir string) string {
	t.Helper()
	return rawGit(t, dir, "for-each-ref") + "\x00" + rawGit(t, dir, "status", "--porcelain")
}

func TestCheckStates(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, r testutil.TrunkRepos) (dir string)
		opts      trunk.Options
		state     trunk.State
		ahead     int
		behind    int
		moved     bool
		lineHas   string
		reasonHas string
	}{
		{
			name:    "level",
			setup:   func(t *testing.T, r testutil.TrunkRepos) string { return r.Subject },
			opts:    trunk.Options{FastForward: true},
			state:   trunk.Level,
			lineHas: "main level with origin/main",
		},
		{
			name: "behind and fast-forwarded",
			setup: func(t *testing.T, r testutil.TrunkRepos) string {
				r.PublishFromPusher(t, "a.txt")
				r.PublishFromPusher(t, "b.txt")
				return r.Subject
			},
			opts:    trunk.Options{FastForward: true},
			state:   trunk.Behind,
			behind:  2,
			moved:   true,
			lineHas: "fast-forwarded main by 2 commit(s) ",
		},
		{
			name: "behind but fast-forward not requested",
			setup: func(t *testing.T, r testutil.TrunkRepos) string {
				r.PublishFromPusher(t, "a.txt")
				return r.Subject
			},
			state:     trunk.Behind,
			behind:    1,
			lineHas:   "behind origin/main by 1, not moved: fast-forward not requested",
			reasonHas: "not requested",
		},
		{
			name: "ahead",
			setup: func(t *testing.T, r testutil.TrunkRepos) string {
				r.Commit(t, r.Subject, "mine.txt", "mine\n")
				return r.Subject
			},
			opts:    trunk.Options{FastForward: true},
			state:   trunk.Ahead,
			ahead:   1,
			lineHas: "satelle: trunk 1 unpushed commit(s)",
		},
		{
			name: "diverged",
			setup: func(t *testing.T, r testutil.TrunkRepos) string {
				r.Commit(t, r.Subject, "mine.txt", "mine\n")
				r.PublishFromPusher(t, "a.txt")
				r.PublishFromPusher(t, "b.txt")
				return r.Subject
			},
			opts:    trunk.Options{FastForward: true},
			state:   trunk.Diverged,
			ahead:   1,
			behind:  2,
			lineHas: "diverged: 1 ahead, 2 behind",
		},
		{
			name: "dirty",
			setup: func(t *testing.T, r testutil.TrunkRepos) string {
				r.PublishFromPusher(t, "a.txt")
				if err := os.WriteFile(filepath.Join(r.Subject, "seed.txt"), []byte("edited\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return r.Subject
			},
			opts:    trunk.Options{FastForward: true},
			state:   trunk.Dirty,
			lineHas: "dirty tree on main",
		},
		{
			name: "offline",
			setup: func(t *testing.T, r testutil.TrunkRepos) string {
				r.Git(t, r.Subject, "remote", "set-url", "origin", filepath.Join(filepath.Dir(r.Remote), "gone.git"))
				return r.Subject
			},
			opts:      trunk.Options{FastForward: true},
			state:     trunk.Offline,
			lineHas:   "fetch from origin failed (proceeding): ",
			reasonHas: "gone.git",
		},
		{
			name: "behind from a linked worktree on another branch is reported, not moved",
			setup: func(t *testing.T, r testutil.TrunkRepos) string {
				r.PublishFromPusher(t, "a.txt")
				wt := filepath.Join(filepath.Dir(r.Subject), "linked")
				r.Git(t, r.Subject, "worktree", "add", "--quiet", "-b", "feature", wt)
				return wt
			},
			opts:      trunk.Options{FastForward: true},
			state:     trunk.Behind,
			behind:    1,
			lineHas:   "behind origin/main by 1, not moved: ",
			reasonHas: "not in this working tree",
		},
		{
			name: "behind with trunk checked out nowhere",
			setup: func(t *testing.T, r testutil.TrunkRepos) string {
				r.PublishFromPusher(t, "a.txt")
				r.Git(t, r.Subject, "checkout", "--quiet", "-b", "feature")
				return r.Subject
			},
			opts:      trunk.Options{FastForward: true},
			state:     trunk.Behind,
			behind:    1,
			reasonHas: "not checked out in any working tree",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			dir := tc.setup(t, r)
			before := take(t, r)

			got := trunk.Check(context.Background(), dir, tc.opts)

			if got.State != tc.state || got.Ahead != tc.ahead || got.Behind != tc.behind {
				t.Fatalf("report = %+v, want state %s ahead %d behind %d", got, tc.state, tc.ahead, tc.behind)
			}
			if got.FastForwarded != tc.moved {
				t.Fatalf("FastForwarded = %v, want %v (%+v)", got.FastForwarded, tc.moved, got)
			}
			if got.Remote != "origin" || got.Trunk != "main" {
				t.Fatalf("remote/trunk = %q/%q, want origin/main", got.Remote, got.Trunk)
			}
			if !strings.Contains(got.Line(), tc.lineHas) || !strings.HasPrefix(got.Line(), "satelle: trunk ") {
				t.Fatalf("line = %q, want prefix %q containing %q", got.Line(), "satelle: trunk ", tc.lineHas)
			}
			if !strings.Contains(got.Reason, tc.reasonHas) {
				t.Fatalf("reason = %q, want it to contain %q", got.Reason, tc.reasonHas)
			}
			if strings.Contains(got.Line(), "\n") {
				t.Fatalf("line spans several lines: %q", got.Line())
			}
			after := take(t, r)
			if tc.moved {
				remoteTip := r.Git(t, r.Remote, "rev-parse", "refs/heads/main")
				if after.ref != remoteTip || got.From != before.ref || got.To != remoteTip {
					t.Fatalf("after ff ref %s, from %s, to %s; want %s from %s", after.ref, got.From, got.To, remoteTip, before.ref)
				}
				if after.porcelain != "" {
					t.Fatalf("fast-forward left a dirty tree: %q", after.porcelain)
				}
				return
			}
			if after != before {
				t.Fatalf("a non-moving case changed the repository: before %+v after %+v", before, after)
			}
		})
	}
}

func TestCheckDirtyBeforeFetch(t *testing.T) {
	r := testutil.NewTrunkRepos(t)
	r.Git(t, r.Subject, "remote", "set-url", "origin", filepath.Join(filepath.Dir(r.Remote), "gone.git"))
	if err := os.WriteFile(filepath.Join(r.Subject, "untracked.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := trunk.Check(context.Background(), r.Subject, trunk.Options{FastForward: true})
	if got.State != trunk.Dirty {
		t.Fatalf("state = %s (%s), want dirty even though the remote is unreachable", got.State, got.Reason)
	}
}

func TestCheckOfflineChangesNothingAndKeepsLastKnownCounts(t *testing.T) {
	r := testutil.NewTrunkRepos(t)
	r.Commit(t, r.Subject, "mine.txt", "mine\n")
	r.Git(t, r.Subject, "remote", "set-url", "origin", filepath.Join(filepath.Dir(r.Remote), "gone.git"))
	before := take(t, r)
	got := trunk.Check(context.Background(), r.Subject, trunk.Options{FastForward: true})
	if got.State != trunk.Offline || got.Ahead != 1 || got.Behind != 0 {
		t.Fatalf("report = %+v, want offline with last-known 1 ahead, 0 behind", got)
	}
	if !strings.Contains(got.Reason, "gone.git") {
		t.Fatalf("reason %q lacks the git error text", got.Reason)
	}
	if after := take(t, r); after != before {
		t.Fatalf("offline changed the repository: %+v -> %+v", before, after)
	}
}

func TestCheckSkipped(t *testing.T) {
	t.Run("not a git repository", func(t *testing.T) {
		testutil.NewTrunkRepos(t) // pins git's configuration for this test
		dir := t.TempDir()
		got := trunk.Check(context.Background(), dir, trunk.Options{FastForward: true})
		if got.State != trunk.Skipped || !got.Quiet() {
			t.Fatalf("report = %+v, want a quiet skipped", got)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("a skipped check wrote into a non-repository: %v %v", entries, err)
		}
	})
	t.Run("no remote", func(t *testing.T) {
		r := testutil.NewTrunkRepos(t)
		dir := filepath.Join(t.TempDir(), "solo")
		r.Git(t, filepath.Dir(dir), "init", "--quiet", "-b", "main", dir)
		r.Commit(t, dir, "solo.txt", "solo\n")
		before := repoSnapshot(t, dir)
		got := trunk.Check(context.Background(), dir, trunk.Options{FastForward: true})
		if got.State != trunk.Skipped || !strings.Contains(got.Reason, "no remote") {
			t.Fatalf("report = %+v, want skipped: no remote", got)
		}
		if after := repoSnapshot(t, dir); after != before {
			t.Fatalf("skipped check changed the repo:\nbefore %q\nafter  %q", before, after)
		}
	})
	t.Run("unresolvable trunk ref", func(t *testing.T) {
		r := testutil.NewTrunkRepos(t)
		r.Git(t, r.Subject, "remote", "set-head", "origin", "--delete")
		r.PublishFromPusher(t, "unresolved.txt")
		before := repoSnapshot(t, r.Subject)
		got := trunk.Check(context.Background(), r.Subject, trunk.Options{FastForward: true})
		if got.State != trunk.Skipped || !strings.Contains(got.Reason, "cannot resolve trunk") {
			t.Fatalf("report = %+v, want skipped: cannot resolve trunk", got)
		}
		if after := repoSnapshot(t, r.Subject); after != before {
			t.Fatalf("skipped check changed the repo:\nbefore %q\nafter  %q", before, after)
		}
		// The caller's branch is the fallback, and a trunk is never assumed.
		got = trunk.Check(context.Background(), r.Subject, trunk.Options{Branch: "main"})
		if got.State != trunk.Behind || got.Behind != 1 || got.Trunk != "main" {
			t.Fatalf("with Options.Branch: report = %+v, want behind origin/main by 1", got)
		}
	})
	t.Run("unknown remote", func(t *testing.T) {
		r := testutil.NewTrunkRepos(t)
		r.PublishFromPusher(t, "unknown.txt")
		before := repoSnapshot(t, r.Subject)
		got := trunk.Check(context.Background(), r.Subject, trunk.Options{Remote: "upstream", FastForward: true})
		if got.State != trunk.Skipped || !strings.Contains(got.Reason, "upstream") {
			t.Fatalf("report = %+v, want skipped naming the remote", got)
		}
		if after := repoSnapshot(t, r.Subject); after != before {
			t.Fatalf("skipped check changed the repo:\nbefore %q\nafter  %q", before, after)
		}
	})
}

// A remote whose default branch is not "main" resolves from its HEAD ref: the
// trunk name is never a constant.
func TestCheckTrunkComesFromTheRemoteHead(t *testing.T) {
	r := testutil.NewTrunkRepos(t)
	r.Git(t, r.Pusher, "push", "--quiet", "origin", "main:trunk")
	r.Git(t, r.Remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
	dir := filepath.Join(t.TempDir(), "clone")
	r.Git(t, filepath.Dir(dir), "clone", "--quiet", r.Remote, dir)
	got := trunk.Check(context.Background(), dir, trunk.Options{})
	if got.Trunk != "trunk" || got.State != trunk.Level {
		t.Fatalf("report = %+v, want level on trunk", got)
	}
}
