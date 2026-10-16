package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// sty_041bf275 AC4: the gate, session start and `satelle policy refresh` all
// resolve the subscribed-list cache through syncstate.PolicyPath, keyed on
// config.RepoKey, which collapses a repo's worktrees. A refresh run from the
// main tree is therefore read back from a linked worktree, whether by the root
// the gate holds (the invoking tree) or the one session start holds (RepoRoot).
func TestPolicyCacheIsOneFileFromEveryWorktree(t *testing.T) {
	isolateProcessEnv(t)
	main, wt := linkedPair(t)
	const sub = "[review]\ngate_create = false\n\n[[gate.command_policy]]\nname = \"cloud\"\nurl = \"https://example.com/p.toml\"\n"
	writeFile(t, filepath.Join(main, ".satelle", "satelle.toml"), sub)
	writeFile(t, filepath.Join(wt, ".satelle", "satelle.toml"), sub)

	t.Chdir(wt)
	a, err := Open()
	if err != nil {
		t.Fatalf("open worktree: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if got := a.PlaneConfig().Gate.CommandPolicies; len(got) != 1 || got[0].Name != "cloud" {
		t.Fatalf("the process of record's subscriptions = %+v", got)
	}

	global := config.GlobalDir()
	entry := syncstate.PolicySub{Source: "https://example.com/p.toml", SourceKind: "url", RulesTOML: "rules"}
	if err := syncstate.RecordPolicy(global, main, "cloud", entry, time.Now()); err != nil {
		t.Fatal(err)
	}
	want := syncstate.PolicyPath(global, main)
	for name, root := range map[string]string{"main tree": main, "linked worktree": wt, "session start's RepoRoot": a.RepoRoot} {
		if got := syncstate.PolicyPath(global, root); got != want {
			t.Errorf("%s resolves %s, want %s", name, got, want)
		}
		st, ok, err := syncstate.LoadPolicy(global, root)
		if err != nil || !ok || st.Subs["cloud"].RulesTOML != "rules" {
			t.Errorf("%s read %+v ok=%v err=%v, want the list the main tree recorded", name, st.Subs["cloud"], ok, err)
		}
	}
	// A stamp written from the worktree is the one the main tree sees.
	if err := syncstate.MarkPolicyAttempt(global, wt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := syncstate.LoadPolicy(global, main); st.CheckedAttempt.IsZero() || st.Subs["cloud"].RulesTOML != "rules" {
		t.Fatalf("main tree read %+v", st)
	}
}
