package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

func writeRepoTOML(t *testing.T, dir, body string) (repo, data string) {
	t.Helper()
	repo = dir
	data = filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "satelle.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, data
}

// recordScope stands in for a sync that resolved the repo's scope for binding.
func recordScope(t *testing.T, home, repo string, scope config.Scope, binding string) {
	t.Helper()
	if err := syncstate.RecordScope(home, repo, scope, binding, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// boundPersonal writes a repo bound to project p whose last resolve was personal.
func boundPersonal(t *testing.T, home, toml string) (repo, data string) {
	t.Helper()
	repo, data = writeRepoTOML(t, t.TempDir(), "[sync]\nproject = \"p\"\n"+toml)
	recordScope(t, home, repo, config.PersonalScope, "p")
	return repo, data
}

func TestCheckSyncHealthLocalOnly(t *testing.T) {
	repo, data := writeRepoTOML(t, t.TempDir(), "[sync]\nstale_after = \"24h\"\n")
	got := checkSyncHealth(repo, data, t.TempDir(), time.Now())
	if !got.OK() {
		t.Fatalf("local-only must PASS: %v", got)
	}
	if len(got.WithSeverity(health.SeverityInfo)) == 0 {
		t.Fatal("expected local-only info finding")
	}
	if n := len(got.WithSeverity(health.SeverityError)); n != 0 {
		t.Fatalf("errors = %d, want 0: %v", n, got)
	}
}

func TestCheckSyncHealthStaleAndFresh(t *testing.T) {
	home := t.TempDir()
	repo, data := boundPersonal(t, home, "stale_after = \"1h\"\n")
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	if err := syncstate.RecordPush(home, repo, true, "", "", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	stale := checkSyncHealth(repo, data, home, now)
	if stale.OK() {
		t.Fatalf("stale must not PASS: %v", stale)
	}
	found := false
	for _, f := range stale {
		if f.ID == health.IDSyncUnbacked && containsAll(f.Detail, repo, "threshold") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing unbacked finding naming partition: %v", stale)
	}

	// Same state, 30d threshold — pass.
	repo2, data2 := boundPersonal(t, home, "stale_after = \"720h\"\n")
	if err := syncstate.RecordPush(home, repo2, true, "", "", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	fresh := checkSyncHealth(repo2, data2, home, now)
	if !fresh.OK() {
		t.Fatalf("720h threshold should PASS: %v", fresh)
	}
	okFound := false
	wantStamp := now.Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	for _, f := range fresh {
		if f.ID == health.IDSyncOk && strings.Contains(f.Detail, wantStamp) {
			okFound = true
		}
	}
	if !okFound {
		t.Fatalf("fresh path must report last successful push %s: %v", wantStamp, fresh)
	}
}

func TestCheckSyncHealthStandingFailure(t *testing.T) {
	home := t.TempDir()
	repo, data := boundPersonal(t, home, "stale_after = \"24h\"\n")
	now := time.Now()
	if err := syncstate.RecordPush(home, repo, false, "hosted down", "", now); err != nil {
		t.Fatal(err)
	}
	got := checkSyncHealth(repo, data, home, now)
	if got.OK() {
		t.Fatalf("standing failure must not PASS: %v", got)
	}
	found := false
	for _, f := range got {
		if f.ID == health.IDSyncFailing && containsAll(f.Detail, "hosted down") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing failing finding: %v", got)
	}
}

func TestCheckSyncHealthMissingThresholdWarns(t *testing.T) {
	home := t.TempDir()
	repo, data := boundPersonal(t, home, "")
	got := checkSyncHealth(repo, data, home, time.Now())
	if !got.OK() {
		t.Fatalf("absent stale_after must not error: %v", got)
	}
	if len(got.WithSeverity(health.SeverityWarn)) == 0 {
		t.Fatal("expected warn for missing stale_after")
	}
	for _, f := range got {
		if f.ID == health.IDSyncUnbacked {
			t.Fatalf("must not emit unbacked without a threshold: %v", got)
		}
	}
}

// sty_642275eb: a config still carrying the retired [sync] hold_stale_after key
// loads and passes, and doctor says nothing about holds.
func TestCheckSyncHealthSaysNothingOfARetiredHoldKey(t *testing.T) {
	repo, data := writeRepoTOML(t, t.TempDir(), "[sync]\nstale_after = \"24h\"\nhold_stale_after = \"2h\"\n")
	got := checkSyncHealth(repo, data, t.TempDir(), time.Now())
	if !got.OK() {
		t.Fatalf("a retired key must not fail the check: %v", got)
	}
	for _, f := range got {
		if strings.Contains(strings.ToLower(f.ID+f.Title+f.Detail), "hold") {
			t.Errorf("doctor emitted a hold-related finding: %+v", f)
		}
	}
}

func TestWorkstateStaleAfterIsNotCompiledIn(t *testing.T) {
	_, has, err := config.WorkstateStaleAfter(config.Config{})
	if err != nil || has {
		t.Fatalf("empty config: has=%v err=%v", has, err)
	}
	d, has, err := config.WorkstateStaleAfter(config.Config{Sync: map[string]string{"stale_after": "90m"}})
	if err != nil || !has || d != 90*time.Minute {
		t.Fatalf("got %v has=%v err=%v", d, has, err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// AC9 (sty_b336c2b0): the work-state check judges from the recorded resolved
// scope and the explicit [sync] project binding. An unbound repo is local even
// when a scope was recorded under its directory name; a bound repo whose last
// resolve was local, or was made for another binding, is local; a recorded team
// scope for the current binding is judged.
func TestCheckSyncHealthJudgesFromRecordedScopeAndBinding(t *testing.T) {
	now := time.Now()
	stale := now.Add(-48 * time.Hour)
	cases := []struct {
		name, toml string
		scope      config.Scope
		binding    string // "" records nothing; "<dir>" is the repo directory name
		wantLocal  bool
		why        string
	}{
		{"unbound", "[sync]\nstale_after = \"24h\"\n", config.TeamScope, "", true, "unbound"},
		{"unbound, scope recorded under the directory name", "[sync]\nstale_after = \"24h\"\n", config.TeamScope, "<dir>", true, "unbound"},
		{"bound, recorded local", "[sync]\nproject = \"p\"\nstale_after = \"24h\"\n", config.LocalScope, "p", true, "resolved for project p"},
		{"bound, recorded for another binding", "[sync]\nproject = \"p\"\nstale_after = \"24h\"\n", config.TeamScope, "other", true, "not yet resolved for project p"},
		{"bound, recorded team", "[sync]\nproject = \"p\"\nstale_after = \"24h\"\n", config.TeamScope, "p", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			repo, data := writeRepoTOML(t, t.TempDir(), c.toml)
			if err := syncstate.RecordPush(home, repo, true, "", "", stale); err != nil {
				t.Fatal(err)
			}
			switch c.binding {
			case "":
			case "<dir>":
				recordScope(t, home, repo, c.scope, filepath.Base(repo))
			default:
				recordScope(t, home, repo, c.scope, c.binding)
			}
			got := checkSyncHealth(repo, data, home, now)
			local := got.WithSeverity(health.SeverityInfo)
			if c.wantLocal {
				if !got.OK() || len(got) != 1 || local[0].ID != health.IDSyncLocal || !strings.Contains(local[0].Detail, c.why) {
					t.Fatalf("want one local info naming %q: %v", c.why, got)
				}
				return
			}
			if got.OK() {
				t.Fatalf("a recorded team scope must judge the 48h-old push: %v", got)
			}
			found := false
			for _, f := range got {
				found = found || f.ID == health.IDSyncUnbacked
			}
			if !found {
				t.Fatalf("missing %s: %v", health.IDSyncUnbacked, got)
			}
		})
	}
}
