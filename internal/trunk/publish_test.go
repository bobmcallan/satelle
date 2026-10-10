package trunk_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/trunk"
)

// Publish through two clones of one bare remote (sty_6af229f1): the pusher is
// another machine, the subject the machine releasing. Every case runs twice —
// once with a single story's linear release, once with an epic container's
// release, whose history holds two child branches merged with --no-ff.

// slice is what the subject's release added on top of the seed: every commit
// of it, and the merge commits among them.
type slice struct{ commits, merges []string }

type shape struct {
	name  string
	build func(t *testing.T, r testutil.TrunkRepos) slice
}

var shapes = []shape{
	{"linear", func(t *testing.T, r testutil.TrunkRepos) slice {
		seed := r.Head(t, r.Subject)
		r.LinearSlice(t)
		return slice{commits: revList(t, r, r.Subject, seed+"..HEAD")}
	}},
	{"epic", func(t *testing.T, r testutil.TrunkRepos) slice {
		seed := r.Head(t, r.Subject)
		merges, _ := r.EpicSlice(t)
		return slice{commits: revList(t, r, r.Subject, seed+"..HEAD"), merges: merges}
	}},
}

func revList(t *testing.T, r testutil.TrunkRepos, dir, rng string) []string {
	t.Helper()
	return strings.Fields(r.Git(t, dir, "rev-list", rng))
}

func isAncestor(r testutil.TrunkRepos, dir, anc, of string) bool {
	_, err := r.GitErr(dir, "merge-base", "--is-ancestor", anc, of)
	return err == nil
}

func parentCount(t *testing.T, r testutil.TrunkRepos, dir, sha string) int {
	t.Helper()
	return len(strings.Fields(r.Git(t, dir, "rev-list", "--parents", "-n1", sha))) - 1
}

// script writes body as a shell script and returns the command that runs it.
func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run.sh")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return "sh " + p
}

func publishAs(t *testing.T, r testutil.TrunkRepos, opts trunk.PublishOptions) trunk.PublishReport {
	t.Helper()
	return trunk.Publish(context.Background(), r.Subject, opts)
}

// assertBothMachinesPresent checks, by ancestry, that the remote's head holds
// every release commit and every commit the pusher published, and that each
// epic merge still has its two parents.
func assertBothMachinesPresent(t *testing.T, r testutil.TrunkRepos, s slice) {
	t.Helper()
	remote := r.RemoteHead(t)
	for _, c := range s.commits {
		if !isAncestor(r, r.Subject, c, remote) {
			t.Errorf("release commit %s is not an ancestor of the pushed head %s", c, remote)
		}
	}
	for _, c := range revList(t, r, r.Pusher, "HEAD") {
		if !isAncestor(r, r.Subject, c, remote) {
			t.Errorf("the other machine's commit %s is not an ancestor of the pushed head %s", c, remote)
		}
	}
	for _, m := range s.merges {
		if n := parentCount(t, r, r.Subject, m); n != 2 {
			t.Errorf("epic merge %s has %d parents, want 2", m, n)
		}
	}
}

func TestPublish_AC1_BothMachinesCommits(t *testing.T) {
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			shim := testutil.GitShim(t)
			s := sh.build(t, r)
			r.PublishFromPusher(t, "other.txt")

			rep := publishAs(t, r, trunk.PublishOptions{Prove: "true", Rounds: 3})
			if !rep.OK() {
				t.Fatalf("publish failed: %s", rep.Error)
			}
			assertBothMachinesPresent(t, r, s)
			if got, want := r.RemoteHead(t), r.Head(t, r.Subject); got != want {
				t.Errorf("remote head %s != subject head %s", got, want)
			}
			if !rep.Merged || rep.Incoming < 1 {
				t.Errorf("report says merged=%v incoming=%d, want a merge of at least 1 commit", rep.Merged, rep.Incoming)
			}
			shim.AssertNoForce(t)
		})
	}
}

func TestPublish_AC2_ProofOnCombinedHead(t *testing.T) {
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			shim := testutil.GitShim(t)
			sh.build(t, r)
			base := r.Head(t, r.Subject)
			r.PublishFromPusher(t, "other.txt")
			seen := filepath.Join(t.TempDir(), "proved-sha")

			// The proof fails unless the other machine's file is in the tree, and
			// records the head it ran on.
			prove := script(t, fmt.Sprintf("test -f other.txt || exit 1\ngit rev-parse HEAD > %s\n", seen))
			rep := publishAs(t, r, trunk.PublishOptions{Prove: prove, Rounds: 3})
			if !rep.OK() {
				t.Fatalf("publish failed: %s", rep.Error)
			}
			b, err := os.ReadFile(seen)
			if err != nil {
				t.Fatalf("the proof never ran: %v", err)
			}
			proved := strings.TrimSpace(string(b))
			remote := r.RemoteHead(t)
			for name, got := range map[string]string{
				"Combined": rep.Combined, "Pushed": rep.Pushed, "Proved[last]": rep.Proved[len(rep.Proved)-1], "remote head": remote,
			} {
				if got != proved {
					t.Errorf("%s = %s, want the head the proof ran on (%s)", name, got, proved)
				}
			}
			if proved == base {
				t.Error("the proof ran on the pre-integration head")
			}
			if rep.Incoming < 1 {
				t.Errorf("Incoming = %d, want at least 1", rep.Incoming)
			}
			if !strings.Contains(rep.Line(), proved) {
				t.Errorf("Line() %q does not name the combined head %s", rep.Line(), proved)
			}
			shim.AssertNoForce(t)
		})
	}
}

// raceScript is a proof that, on the runs listed, lands one more commit from the
// other machine while the proof is running — the moment a push gets refused.
func raceScript(t *testing.T, r testutil.TrunkRepos, racingRuns string) string {
	t.Helper()
	count := filepath.Join(t.TempDir(), "runs")
	return script(t, fmt.Sprintf(`test -f other.txt || exit 1
n=$(cat %[1]s 2>/dev/null || echo 0)
n=$((n+1))
echo $n > %[1]s
case " %[2]s " in *" $n "*) ;; *) exit 0 ;; esac
echo race$n > %[3]s/race$n.txt
git -C %[3]s add race$n.txt
git -C %[3]s commit -q -m race$n
git -C %[3]s push -q origin main
`, count, racingRuns, r.Pusher))
}

const stampScript = `n=$(git rev-list --count HEAD)
echo $n > .version
git add .version
git commit -q -m "bump $n"
`

func TestPublish_AC3_RaceRetried(t *testing.T) {
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			shim := testutil.GitShim(t)
			s := sh.build(t, r)
			r.PublishFromPusher(t, "other.txt")

			rep := publishAs(t, r, trunk.PublishOptions{
				Prove: raceScript(t, r, "1"), Stamp: script(t, stampScript), Rounds: 3,
			})
			if !rep.OK() {
				t.Fatalf("publish failed: %s", rep.Error)
			}
			if rep.Rounds != 2 || len(rep.Proved) != 2 {
				t.Fatalf("rounds=%d proofs=%d, want 2 and 2", rep.Rounds, len(rep.Proved))
			}
			racing := r.Head(t, r.Pusher)
			if !isAncestor(r, r.Subject, racing, rep.Proved[1]) {
				t.Errorf("the second proof %s does not contain the racing commit %s", rep.Proved[1], racing)
			}
			if isAncestor(r, r.Subject, racing, rep.Proved[0]) {
				t.Error("the first proof already contained the racing commit; nothing raced")
			}
			assertBothMachinesPresent(t, r, s)

			// One bump in the pushed history, computed from the round-2 tree.
			remote := r.RemoteHead(t)
			bumps := 0
			for _, subj := range strings.Split(r.Git(t, r.Subject, "log", "--format=%s", remote), "\n") {
				if strings.HasPrefix(subj, "bump ") {
					bumps++
				}
			}
			if bumps != 1 {
				t.Errorf("pushed history has %d bump commits, want 1", bumps)
			}
			version := strings.TrimSpace(r.Git(t, r.Subject, "show", remote+":.version"))
			if want := r.Git(t, r.Subject, "rev-list", "--count", rep.Integrated); version != want {
				t.Errorf(".version = %s, want %s (the round-2 integrated tree)", version, want)
			}
			shim.AssertNoForce(t)
		})
	}
}

func TestPublish_AC3_BoundExhausted(t *testing.T) {
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			shim := testutil.GitShim(t)
			sh.build(t, r)
			base := r.Head(t, r.Subject)
			r.PublishFromPusher(t, "other.txt")

			rep := publishAs(t, r, trunk.PublishOptions{Prove: raceScript(t, r, "1 2 3 4"), Rounds: 2})
			if rep.OK() {
				t.Fatal("publish succeeded though the remote moved every round")
			}
			for _, want := range []string{"moved 2 times", "not pushed", "bound 2"} {
				if !strings.Contains(rep.Error, want) {
					t.Errorf("error %q does not say %q", rep.Error, want)
				}
			}
			if rep.Pushed != "" || rep.Rounds != 2 {
				t.Errorf("pushed=%q rounds=%d, want nothing pushed after 2 rounds", rep.Pushed, rep.Rounds)
			}
			if got, want := r.RemoteHead(t), r.Head(t, r.Pusher); got != want {
				t.Errorf("remote head %s != the other machine's latest %s", got, want)
			}
			assertRestored(t, r, base)
			shim.AssertNoForce(t)
		})
	}
}

// assertRestored checks a failed publish left the subject on base with a clean
// tree and no merge in progress.
func assertRestored(t *testing.T, r testutil.TrunkRepos, base string) {
	t.Helper()
	if got := r.Head(t, r.Subject); got != base {
		t.Errorf("subject head %s, want the release head %s restored", got, base)
	}
	if st := r.Git(t, r.Subject, "status", "--porcelain"); st != "" {
		t.Errorf("subject tree is dirty after a failed publish:\n%s", st)
	}
	if _, err := os.Stat(filepath.Join(r.Subject, ".git", "MERGE_HEAD")); err == nil {
		t.Error("a merge is still in progress in the subject")
	}
}

func TestPublish_ConflictStops(t *testing.T) {
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			shim := testutil.GitShim(t)
			sh.build(t, r)
			r.Commit(t, r.Subject, "seed.txt", "mine\n")
			base := r.Head(t, r.Subject)
			r.Commit(t, r.Pusher, "seed.txt", "theirs\n")
			r.Git(t, r.Pusher, "push", "--quiet", "origin", "main")
			before := r.RemoteHead(t)
			pushed := len(shim.Pushes())

			rep := publishAs(t, r, trunk.PublishOptions{Prove: "true", Rounds: 3})
			if rep.OK() {
				t.Fatal("publish succeeded over a content conflict")
			}
			for _, want := range []string{"slice conflicts with origin/main", "seed.txt"} {
				if !strings.Contains(rep.Error, want) {
					t.Errorf("error %q does not say %q", rep.Error, want)
				}
			}
			if r.RemoteHead(t) != before {
				t.Error("the remote moved though the slice conflicted")
			}
			assertRestored(t, r, base)
			if n := len(shim.Pushes()) - pushed; n != 0 {
				t.Errorf("%d push(es) issued for a conflicting slice", n)
			}
		})
	}
}

// A merge that fails without leaving unmerged paths (here: no committer
// identity) is a failed merge, not a conflict.
func TestPublish_MergeFailureIsNotAConflict(t *testing.T) {
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			shim := testutil.GitShim(t)
			sh.build(t, r)
			r.Commit(t, r.Subject, "mine.txt", "mine\n")
			base := r.Head(t, r.Subject)
			r.Commit(t, r.Pusher, "theirs.txt", "theirs\n")
			r.Git(t, r.Pusher, "push", "--quiet", "origin", "main")
			before := r.RemoteHead(t)
			pushed := len(shim.Pushes())
			r.Git(t, r.Subject, "config", "--unset", "user.name")
			r.Git(t, r.Subject, "config", "--unset", "user.email")
			r.Git(t, r.Subject, "config", "user.useConfigOnly", "true")
			for _, k := range []string{"EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL"} {
				t.Setenv(k, "")
				os.Unsetenv(k)
			}

			rep := publishAs(t, r, trunk.PublishOptions{Prove: "true", Rounds: 3})
			if rep.OK() {
				t.Fatal("publish succeeded though the merge could not be committed")
			}
			if !strings.Contains(rep.Error, "merge of origin/main failed") {
				t.Errorf("error %q does not say the merge failed", rep.Error)
			}
			if strings.Contains(rep.Error, "conflicts") {
				t.Errorf("error %q reports a conflict though no path was unmerged", rep.Error)
			}
			if r.RemoteHead(t) != before {
				t.Error("the remote moved though the merge failed")
			}
			assertRestored(t, r, base)
			if n := len(shim.Pushes()) - pushed; n != 0 {
				t.Errorf("%d push(es) issued after a failed merge", n)
			}
		})
	}
}

func TestPublish_AC4_LevelRemote(t *testing.T) {
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			s := sh.build(t, r)
			base := r.Head(t, r.Subject)
			shim := testutil.GitShim(t)
			pushes := r.PushCounter(t)
			dir := t.TempDir()
			runs, seen := filepath.Join(dir, "runs"), filepath.Join(dir, "sha")
			prove := script(t, fmt.Sprintf("echo run >> %s\ngit rev-parse HEAD > %s\n", runs, seen))

			rep := publishAs(t, r, trunk.PublishOptions{Prove: prove, Rounds: 3})
			if !rep.OK() {
				t.Fatalf("publish failed: %s", rep.Error)
			}
			// One push.
			if n := pushes(); n != 1 {
				t.Errorf("remote accepted %d pushes, want 1", n)
			}
			if n := len(shim.Pushes()); n != 1 {
				t.Errorf("%d push invocations, want 1", n)
			}
			// No commits beyond the release's own.
			if extra := revList(t, r, r.Subject, base+".."+r.RemoteHead(t)); len(extra) != 0 || rep.Merged {
				t.Errorf("publish added commits %v (merged=%v) on a level remote", extra, rep.Merged)
			}
			// One proof run, on the release head.
			b, _ := os.ReadFile(runs)
			if n := strings.Count(string(b), "run"); n != 1 {
				t.Errorf("proof ran %d times, want 1", n)
			}
			if len(rep.Proved) != 1 || rep.Proved[0] != base {
				t.Errorf("Proved = %v, want [%s]", rep.Proved, base)
			}
			if got, _ := os.ReadFile(seen); strings.TrimSpace(string(got)) != base {
				t.Errorf("the proof ran on %q, want the release head %s", got, base)
			}
			// Pushed head == local release head.
			for name, got := range map[string]string{"remote head": r.RemoteHead(t), "Pushed": rep.Pushed, "Combined": rep.Combined} {
				if got != base {
					t.Errorf("%s = %s, want the local release head %s", name, got, base)
				}
			}
			if rep.Rounds != 1 || rep.Incoming != 0 {
				t.Errorf("rounds=%d incoming=%d, want 1 and 0", rep.Rounds, rep.Incoming)
			}
			for _, c := range s.commits {
				if !isAncestor(r, r.Subject, c, r.RemoteHead(t)) {
					t.Errorf("release commit %s is not on the remote", c)
				}
			}
			shim.AssertNoForce(t)
		})
	}
}

func TestPublish_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, r testutil.TrunkRepos)
		opts  trunk.PublishOptions
		want  string
	}{
		{"no proof", func(*testing.T, testutil.TrunkRepos) {}, trunk.PublishOptions{Rounds: 3}, "no configured proof"},
		{"no rounds", func(*testing.T, testutil.TrunkRepos) {}, trunk.PublishOptions{Prove: "true"}, "rounds must be at least 1"},
		{"dirty tree", func(t *testing.T, r testutil.TrunkRepos) {
			if err := os.WriteFile(filepath.Join(r.Subject, "seed.txt"), []byte("edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, trunk.PublishOptions{Prove: "true", Rounds: 3}, "uncommitted changes"},
		{"off trunk", func(t *testing.T, r testutil.TrunkRepos) {
			r.Git(t, r.Subject, "checkout", "--quiet", "-b", "side")
		}, trunk.PublishOptions{Prove: "true", Rounds: 3}, "not trunk main"},
		{"no remote", func(t *testing.T, r testutil.TrunkRepos) {}, trunk.PublishOptions{Prove: "true", Rounds: 3, Remote: "nowhere"}, "remote nowhere is not configured"},
		{"proof fails", func(t *testing.T, r testutil.TrunkRepos) { r.LinearSlice(t) }, trunk.PublishOptions{Prove: "echo broken; exit 3", Rounds: 3}, "proof failed"},
		{"stamp fails", func(t *testing.T, r testutil.TrunkRepos) { r.LinearSlice(t) }, trunk.PublishOptions{Prove: "true", Stamp: "exit 1", Rounds: 3}, "stamp failed"},
		{"stamp leaves changes", func(t *testing.T, r testutil.TrunkRepos) { r.LinearSlice(t) }, trunk.PublishOptions{Prove: "true", Stamp: "echo x >> story.txt", Rounds: 3}, "uncommitted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			c.setup(t, r)
			before := r.RemoteHead(t)
			base := r.Head(t, r.Subject)
			rep := publishAs(t, r, c.opts)
			if rep.OK() || !strings.Contains(rep.Error, c.want) {
				t.Fatalf("publish error = %q (ok=%v), want one containing %q", rep.Error, rep.OK(), c.want)
			}
			if r.RemoteHead(t) != before {
				t.Error("the remote moved on a refused publish")
			}
			if c.name == "proof fails" || c.name == "stamp fails" || c.name == "stamp leaves changes" {
				assertRestored(t, r, base)
			}
		})
	}
}

func TestPublish_Line(t *testing.T) {
	ok := trunk.PublishReport{Pushed: "abc", Combined: "abc", Rounds: 2, Incoming: 3, ProveCmd: "go test ./..."}
	if got, want := ok.Line(), "satelle: trunk published abc (combined abc, rounds 2, incoming 3, proved by go test ./...)"; got != want {
		t.Errorf("Line() = %q, want %q", got, want)
	}
	bad := trunk.PublishReport{Error: "origin/main moved 2 times; not pushed (bound 2)"}
	if got, want := bad.Line(), "satelle: trunk publish failed: origin/main moved 2 times; not pushed (bound 2)"; got != want {
		t.Errorf("Line() = %q, want %q", got, want)
	}
}

// The no-force promise rests on one git runner: publish.go must not start a
// process of its own.
func TestPublishSourceStartsNoProcessOfItsOwn(t *testing.T) {
	b, err := os.ReadFile("publish.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "exec.Command") {
		t.Error("publish.go calls exec.Command; every process must go through the shared git/shell runners")
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, `"push"`) {
			continue
		}
		for _, force := range []string{`--force`, `"-f"`, `"+refs`, `"+HEAD`} {
			if strings.Contains(line, force) {
				t.Errorf("the push line carries a force token %s: %s", force, line)
			}
		}
	}
}
