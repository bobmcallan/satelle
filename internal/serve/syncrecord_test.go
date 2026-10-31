package serve

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// syncRepo is a repo bound to project p whose last sync resolved scope for that
// binding (a local scope is an unbound repo, which records nothing).
func syncRepo(t *testing.T, home, scope string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[sync]\nproject = \"p\"\n"
	if scope == "local" {
		body = "[sync]\n"
	}
	if err := os.WriteFile(filepath.Join(dir, config.ConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if scope != "local" {
		sc, err := config.ParseScope(scope)
		if err != nil {
			t.Fatal(err)
		}
		if err := syncstate.RecordResolvedScope(home, repo, sc, "p", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// A pass over a local repo records only the scope: the seeded success and
// failure stand as they were (sty_3df5f95a).
func TestScopedRecordLocalRecordsOnlyScope(t *testing.T) {
	t0 := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		seed   func(home, repo string, success bool, reason, scope string, now time.Time) error
		record func(home string) recordFunc
		leg    func(syncstate.State) (time.Time, string)
	}{
		"push": {syncstate.RecordPush, recordPushFunc, func(s syncstate.State) (time.Time, string) {
			return s.PushLastSuccess, s.PushReason
		}},
		"snapshot": {syncstate.RecordSnapshot, recordSnapshotFunc, func(s syncstate.State) (time.Time, string) {
			return s.SnapshotLastSuccess, s.SnapshotReason
		}},
	} {
		t.Run(name, func(t *testing.T) {
			home := testutil.IsolateHome(t)
			repo := syncRepo(t, home, "local")
			if err := c.seed(home, repo, true, "", "", t0); err != nil {
				t.Fatal(err)
			}
			if err := c.seed(home, repo, false, "hosted 505", "", t0.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			c.record(home)(repo, true, "")
			st, ok, err := syncstate.Load(home, repo)
			if err != nil || !ok {
				t.Fatalf("load: ok=%v err=%v", ok, err)
			}
			if st.Scope != "local" {
				t.Fatalf("Scope = %q, want local", st.Scope)
			}
			if last, reason := c.leg(st); !last.Equal(t0) || reason != "hosted 505" {
				t.Fatalf("skipped local pass changed the leg: last success %v, reason %q", last, reason)
			}
		})
	}
}

// An opted-in repo records its pass with the resolved scope, as before.
func TestScopedRecordOptedInRecordsPass(t *testing.T) {
	home := testutil.IsolateHome(t)
	repo := syncRepo(t, home, "personal")
	recordPushFunc(home)(repo, false, "hosted 505")
	st, _, err := syncstate.Load(home, repo)
	if err != nil {
		t.Fatal(err)
	}
	if st.Scope != "personal" || st.PushReason != "hosted 505" {
		t.Fatalf("push failure: %+v", st)
	}
	recordSnapshotFunc(home)(repo, true, "")
	if st, _, _ = syncstate.Load(home, repo); st.Scope != "personal" || st.SnapshotLastSuccess.IsZero() {
		t.Fatalf("snapshot success: %+v", st)
	}
}
