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

func TestCheckSyncHealthLocalOnly(t *testing.T) {
	repo, data := writeRepoTOML(t, t.TempDir(), "[sync]\nall = \"local\"\nstale_after = \"24h\"\n")
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
	repo, data := writeRepoTOML(t, t.TempDir(), "[sync]\nstories = \"personal\"\nstale_after = \"1h\"\n")
	home := t.TempDir()
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
	repo2, data2 := writeRepoTOML(t, t.TempDir(), "[sync]\nstories = \"personal\"\nstale_after = \"720h\"\n")
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
	repo, data := writeRepoTOML(t, t.TempDir(), "[sync]\nstories = \"personal\"\nstale_after = \"24h\"\n")
	home := t.TempDir()
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
	repo, data := writeRepoTOML(t, t.TempDir(), "[sync]\nstories = \"personal\"\n")
	got := checkSyncHealth(repo, data, t.TempDir(), time.Now())
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
