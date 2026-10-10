package trunk_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/trunk"
)

// ClassifyBase is table-tested for every spelling in four situations: the
// remote's HEAD ref resolves, it is restored by set-head --auto, it cannot be
// resolved and there is no hint, and it cannot be resolved but a hint names the
// trunk (sty_92337a13).
func TestClassifyBase(t *testing.T) {
	modes := []struct {
		name  string
		setup func(t *testing.T, r testutil.TrunkRepos)
		opts  trunk.Options
		// kind: how a name that is the trunk, and one that is not, classify.
		unresolved bool
	}{
		{name: "resolved", setup: func(*testing.T, testutil.TrunkRepos) {}},
		{name: "set-head auto", setup: func(t *testing.T, r testutil.TrunkRepos) {
			r.Git(t, r.Subject, "remote", "set-head", "origin", "--delete")
		}},
		{name: "unresolved, no hint", unresolved: true, setup: unresolveRemote},
		{name: "unresolved, hint", setup: unresolveRemote, opts: trunk.Options{Branch: "main"}},
	}
	rows := []struct {
		base    string
		isTrunk bool
	}{
		{"main", true},
		{"refs/heads/main", true},
		{"origin/main", true},
		{"refs/remotes/origin/main", true},
		{"dep/x", false},
		{"origin/dep/x", false},
		{"epic/local", false},
		{"refs/heads/epic/local", false},
		{"other/main", false}, // other is not a remote: a branch named other/main
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			m.setup(t, r)
			for _, row := range rows {
				got := trunk.ClassifyBase(context.Background(), r.Subject, row.base, m.opts)
				if m.unresolved {
					if !got.Unresolved || got.IsTrunk || !got.Stop.Unresolved {
						t.Errorf("%s: %+v, want unresolved and not the trunk", row.base, got)
					}
					continue
				}
				if got.Unresolved || got.Offline || got.IsTrunk != row.isTrunk || got.Branch != "main" || got.Remote != "origin" {
					t.Errorf("%s: %+v, want IsTrunk=%v on origin/main", row.base, got, row.isTrunk)
				}
			}
		})
	}
}

func unresolveRemote(t *testing.T, r testutil.TrunkRepos) {
	t.Helper()
	r.Git(t, r.Subject, "remote", "set-head", "origin", "--delete")
	r.Git(t, r.Remote, "symbolic-ref", "HEAD", "refs/heads/gone")
}

func TestClassifyBaseOfflineAndNoRemote(t *testing.T) {
	r := testutil.NewTrunkRepos(t)
	r.Git(t, r.Subject, "remote", "set-head", "origin", "--delete")
	r.Git(t, r.Subject, "remote", "set-url", "origin", t.TempDir()+"/missing.git")
	got := trunk.ClassifyBase(context.Background(), r.Subject, "main", trunk.Options{})
	if !got.Offline || got.Unresolved || got.IsTrunk || got.Stop.State != trunk.Offline {
		t.Errorf("unreachable remote: %+v, want offline", got)
	}
	// A hint outranks an unreachable remote: the fetch reports offline itself.
	got = trunk.ClassifyBase(context.Background(), r.Subject, "main", trunk.Options{Branch: "main"})
	if got.Offline || !got.IsTrunk {
		t.Errorf("unreachable remote with a hint: %+v, want main to be the trunk", got)
	}

	solo := t.TempDir()
	r.Git(t, solo, "init", "--quiet", "-b", "main")
	got = trunk.ClassifyBase(context.Background(), solo, "main", trunk.Options{})
	if got.IsTrunk || got.Unresolved || got.Offline {
		t.Errorf("no remote: %+v, want nothing to classify", got)
	}
}

func TestStopState(t *testing.T) {
	cases := []struct {
		rep  trunk.Report
		want string
	}{
		{trunk.Report{State: trunk.Level}, ""},
		{trunk.Report{State: trunk.Skipped, Reason: "no remote configured"}, ""},
		{trunk.Report{State: trunk.Skipped, Unresolved: true}, "unresolved"},
		{trunk.Report{State: trunk.Behind, FastForwarded: true}, ""},
		{trunk.Report{State: trunk.Behind}, "behind"},
		{trunk.Report{State: trunk.Ahead}, "ahead"},
		{trunk.Report{State: trunk.Diverged}, "diverged"},
		{trunk.Report{State: trunk.Dirty}, "dirty"},
		{trunk.Report{State: trunk.Offline}, "offline"},
	}
	for _, c := range cases {
		if got := trunk.StopState(c.rep); got != c.want {
			t.Errorf("StopState(%+v) = %q, want %q", c.rep, got, c.want)
		}
	}
}

func TestHintNamesTheWayOut(t *testing.T) {
	for _, c := range []struct {
		rep  trunk.Report
		want string
	}{
		{trunk.Report{State: trunk.Ahead, Remote: "origin", Trunk: "main"}, "push them first"},
		{trunk.Report{State: trunk.Skipped, Unresolved: true, Remote: "origin"}, "--trunk-branch <name> or run git remote set-head origin --auto"},
		{trunk.Report{State: trunk.Dirty, Trunk: "main"}, "stash"},
	} {
		if got := c.rep.Hint(); !strings.Contains(got, c.want) || strings.Contains(got, " --branch") {
			t.Errorf("Hint(%+v) = %q, want %q and no --branch", c.rep, got, c.want)
		}
	}
}

// Check opts in to set-head --auto: without it the missing HEAD ref stays
// unresolved, as the engage path needs.
func TestCheckResolveHeadIsOptIn(t *testing.T) {
	r := testutil.NewTrunkRepos(t)
	r.Git(t, r.Subject, "remote", "set-head", "origin", "--delete")
	got := trunk.Check(context.Background(), r.Subject, trunk.Options{})
	if got.State != trunk.Skipped || !got.Unresolved {
		t.Fatalf("without ResolveHead: %+v, want skipped and unresolved", got)
	}
	got = trunk.Check(context.Background(), r.Subject, trunk.Options{ResolveHead: true})
	if got.State != trunk.Level || got.Trunk != "main" {
		t.Fatalf("with ResolveHead: %+v, want level on main", got)
	}
}
