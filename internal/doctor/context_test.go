package doctor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/health"
)

// The context checks are injected authority: doctor calls them with the repo and
// its authored plane and folds their findings in, advisory ones included.
func TestCheckRunsInjectedContextChecks(t *testing.T) {
	root := newFixtureRepo(t, fixtureOpts{})
	var gotRoot, gotData string
	rep := Check(context.Background(), Opts{
		RepoRoot: root, DataDir: filepath.Join(root, ".satelle"),
		ContextChecks: func(_ context.Context, repoRoot, dataDir string) health.Findings {
			gotRoot, gotData = repoRoot, dataDir
			return health.Findings{health.Warn(health.IDContextSessionOverflow, "t", "harness x overflows")}
		},
	})
	if gotRoot != root || gotData != filepath.Join(root, ".satelle") {
		t.Fatalf("checks got repo=%q data=%q", gotRoot, gotData)
	}
	if !ids(rep)[health.IDContextSessionOverflow] {
		t.Fatalf("the injected finding must be in the report: %+v", rep.Findings)
	}
	if !rep.OK {
		t.Error("an advisory context finding must not make the repo unhealthy")
	}
}

// The semantic review spawns an agent, so it needs the flag: a reviewer that is
// wired but not asked for is never called.
func TestCheckRunsTheContextReviewerOnlyWhenAsked(t *testing.T) {
	root := newFixtureRepo(t, fixtureOpts{})
	calls := 0
	reviewer := func(context.Context, string, string) health.Findings {
		calls++
		return health.Findings{health.Warn(health.IDContextDuplicateSemantic, "t", "x restates y")}
	}
	rep := Check(context.Background(), Opts{RepoRoot: root, ContextReviewer: reviewer})
	if calls != 0 || ids(rep)[health.IDContextDuplicateSemantic] {
		t.Fatalf("plain doctor spawned the reviewer (%d call(s))", calls)
	}
	rep = Check(context.Background(), Opts{RepoRoot: root, ContextReview: true, ContextReviewer: reviewer})
	if calls != 1 || !ids(rep)[health.IDContextDuplicateSemantic] {
		t.Fatalf("--context-review must run it exactly once (%d call(s)): %+v", calls, rep.Findings)
	}
	// The flag without a wired reviewer is inert, not a crash.
	Check(context.Background(), Opts{RepoRoot: root, ContextReview: true})
}
