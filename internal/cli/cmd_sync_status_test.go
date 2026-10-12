package cli

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// Substrate continuity (sty_7562055c): `sync status`, and the state each sync
// operation records. The two-machine harness is sync_snapshot_test.go's.

func (m *snapMachine) status() (string, error) {
	m.use()
	cmd, buf := testCmd()
	err := runSyncStatus(cmd, m.url)
	return buf.String(), err
}

func (m *snapMachine) recorded() map[string]syncstate.SubstrateArea {
	m.t.Helper()
	st, _, err := syncstate.Load(config.GlobalDir(), m.repo)
	if err != nil {
		m.t.Fatal(err)
	}
	return st.SubstrateAreas
}

// published is a machine that has pushed one file in each of skills, workflows
// and documents, so all three are in step.
func publishedMachine(t *testing.T, url string) *snapMachine {
	t.Helper()
	a := newSnapMachine(t, url)
	a.write("skills/x.md", "x\n")
	a.write("workflows/w.md", "w\n")
	a.write("documents/d.md", "d\n")
	a.pushConfig()
	a.pushDocs()
	return a
}

var colSplit = regexp.MustCompile(` {2,}`)

// row is the columns of an area's line in `sync status` output.
func row(t *testing.T, out, area string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, area+" ") {
			return colSplit.Split(strings.TrimSpace(line), -1)
		}
	}
	t.Fatalf("no %s row in:\n%s", area, out)
	return nil
}

func wantState(t *testing.T, out, area, state string) []string {
	t.Helper()
	cols := row(t, out, area)
	if len(cols) < 5 || cols[1] != state {
		t.Fatalf("%s = %v, want state %q\n%s", area, cols, state, out)
	}
	return cols
}

// forgeRecord appends a snapshot record the way another machine's push would,
// without any of its blobs.
func (f *snapFake) forgeRecord(t *testing.T, area string, parent int, files map[string]string) int {
	t.Helper()
	rec, err := snapsync.NewRecord(parent, "forged", files)
	if err != nil {
		t.Fatal(err)
	}
	_, v, _ := f.cfg.put("probe", snapsync.RecordPath(area), rec.Encode())
	return v
}

func TestSyncStatusNeverSyncedExitsNonZero(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a := newSnapMachine(t, ts.URL)
	a.write("skills/x.md", "x\n")
	out, err := a.status()
	if err == nil {
		t.Fatalf("a machine that never synced must exit non-zero:\n%s", out)
	}
	for _, area := range []string{"skills", "workflows", "documents"} {
		cols := wantState(t, out, area, "never synced")
		if cols[4] != "satelle sync" {
			t.Errorf("%s next = %q", area, cols[4])
		}
	}
	if len(a.recorded()) != 3 {
		t.Errorf("status must record each verified area: %v", a.recorded())
	}
}

func TestSyncStatusInStepExitsZero(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a := publishedMachine(t, ts.URL)
	out, err := a.status()
	if err != nil {
		t.Fatalf("an in-step machine must exit zero: %v\n%s", err, out)
	}
	for _, area := range []string{"skills", "workflows", "documents"} {
		cols := wantState(t, out, area, "in step")
		if cols[2] != "snapshot 1" || cols[3] != "snapshot 1" {
			t.Errorf("%s versions = %v, want snapshot 1 / snapshot 1", area, cols)
		}
	}
	rec := a.recorded()
	for _, area := range []string{"skills", "workflows", "documents"} {
		if rec[area].State != snapsync.StateInStep || rec[area].CheckedAt.IsZero() {
			t.Errorf("recorded %s = %+v", area, rec[area])
		}
	}
}

func TestSyncStatusUnpushed(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a := publishedMachine(t, ts.URL)
	a.write("skills/x.md", "edited\n")
	out, err := a.status()
	if err == nil {
		t.Fatalf("unpushed work must exit non-zero:\n%s", out)
	}
	if cols := wantState(t, out, "skills", "unpushed"); cols[4] != "satelle sync" {
		t.Errorf("next = %q", cols[4])
	}
	wantState(t, out, "workflows", "in step")
}

func TestSyncStatusBehindAndDiverged(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a := publishedMachine(t, ts.URL)
	b := newSnapMachine(t, ts.URL)
	b.deploy()
	b.pullDocs()
	a.write("skills/x.md", "from A\n")
	a.write("documents/d.md", "d from A\n")
	a.pushConfig()
	a.pushDocs()

	out, err := b.status()
	if err == nil {
		t.Fatalf("a machine behind must exit non-zero:\n%s", out)
	}
	if cols := wantState(t, out, "skills", "behind"); cols[4] != "satelle sync config deploy" {
		t.Errorf("skills next = %q", cols[4])
	}
	if cols := wantState(t, out, "documents", "behind"); cols[4] != "satelle sync documents pull" {
		t.Errorf("documents next = %q", cols[4])
	}
	wantState(t, out, "workflows", "in step")

	b.write("skills/x.md", "from B\n")
	out, _ = b.status()
	if cols := wantState(t, out, "skills", "diverged"); !strings.Contains(cols[4], "satelle sync reconcile") || strings.Contains(cols[4], "deploy") {
		t.Errorf("diverged next = %q, want reconcile and not deploy", cols[4])
	}
	if rec := b.recorded(); rec["skills"].State != "diverged" || rec["workflows"].State != "in step" {
		t.Errorf("recorded = %+v", rec)
	}
}

func TestSyncStatusUnmerged(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a := publishedMachine(t, ts.URL)
	b := newSnapMachine(t, ts.URL)
	b.deploy()
	a.write("skills/x.md", "from A\n")
	a.pushConfig()
	b.write("skills/x.md", "from B\n")
	b.deploy() // conflict: both sides changed x.md

	out, err := b.status()
	if err == nil {
		t.Fatalf("an unmerged area must exit non-zero:\n%s", out)
	}
	cols := wantState(t, out, "skills", "unmerged")
	if !strings.Contains(cols[4], "skills/x.md") || !strings.Contains(cols[4], "satelle sync reconcile") {
		t.Errorf("next must name reconcile and the unmerged path: %q", cols[4])
	}
}

// An orphaned record — a loser of a push race stays on the hosted copy as a dead
// head — is not a newer snapshot, so it must not read as behind.
func TestSyncStatusOrphanedRecordIsNotBehind(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	a := publishedMachine(t, ts.URL)
	if v := f.forgeRecord(t, "skills", 0, map[string]string{"skills/x.md": snapsync.SHA([]byte("lost\n"))}); v != 2 {
		t.Fatalf("forged record version = %d, want 2", v)
	}
	out, err := a.status()
	if err != nil {
		t.Fatalf("an orphaned record must not fail status: %v\n%s", err, out)
	}
	wantState(t, out, "skills", "in step")
}

// A hosted snapshot that names bytes the hosted copy does not hold yet leaves a
// pull incomplete: the versions agree afterwards, but a pull would still change
// something, so the area is behind.
func TestSyncStatusPendingPullIsBehind(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	a := publishedMachine(t, ts.URL)
	b := newSnapMachine(t, ts.URL)
	b.deploy()
	_ = a
	f.forgeRecord(t, "skills", 1, map[string]string{"skills/x.md": snapsync.SHA([]byte("not uploaded\n"))})

	b.deploy()
	base, ok := b.base("skills")
	if !ok || base.Version != 2 {
		t.Fatalf("test premise: the incomplete pull must advance the base to 2: %+v", base)
	}
	statusOut, err := b.status()
	if err == nil {
		t.Fatalf("a pending pull must exit non-zero:\n%s", statusOut)
	}
	cols := wantState(t, statusOut, "skills", "behind")
	if cols[2] != "snapshot 2" || cols[3] != "snapshot 2" {
		t.Errorf("versions = %v, want the base to have caught up with the hosted snapshot", cols)
	}
	if got := b.recorded()["skills"].State; got != "behind" {
		t.Errorf("recorded after the incomplete pull = %q, want behind", got)
	}
}

func TestSyncStatusUnavailableWithoutLogin(t *testing.T) {
	ts, _ := newSnapServer(t) // no credential seeded
	a := newSnapMachine(t, ts.URL)
	a.write("skills/x.md", "x\n")
	out, err := a.status()
	if err == nil {
		t.Fatalf("an unreadable hosted copy must exit non-zero:\n%s", out)
	}
	if cols := wantState(t, out, "skills", "unavailable"); cols[4] != "satelle login" {
		t.Errorf("next = %q, want satelle login", cols[4])
	}
	if rec := a.recorded(); len(rec) != 0 {
		t.Errorf("an unavailable answer verified nothing and must not be recorded: %v", rec)
	}
}

func TestSyncStatusAllLocalChecksNothing(t *testing.T) {
	tempRepo(t)
	out, err := runRoot(t, "sync", "status")
	if err != nil || !strings.Contains(out, "Nothing to check") {
		t.Fatalf("all-local status = %v\n%s", err, out)
	}
}

// AC2: every operation leaves the state the driver reports afterwards.
func TestSyncOperationsRecordDriverDerivedState(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)

	a := newSnapMachine(t, ts.URL)
	a.write("skills/x.md", "x\n")
	a.write("workflows/w.md", "w\n")
	a.write("documents/d.md", "d\n")
	a.pushConfig()
	if rec := a.recorded(); rec["skills"].State != "in step" || rec["workflows"].State != "in step" {
		t.Errorf("after a config push: %+v", rec)
	} else if _, ok := rec["documents"]; ok {
		t.Errorf("a config push must not record documents: %+v", rec)
	}
	a.pushDocs()
	if rec := a.recorded(); rec["documents"].State != "in step" {
		t.Errorf("after a documents push: %+v", rec)
	}

	// A push refused for being behind records behind, not in step.
	b := newSnapMachine(t, ts.URL)
	b.deploy()
	if rec := b.recorded(); rec["skills"].State != "in step" {
		t.Errorf("after a deploy: %+v", rec)
	}
	b.pullDocs()
	if rec := b.recorded(); rec["documents"].State != "in step" {
		t.Errorf("after a documents pull: %+v", rec)
	}
	a.write("skills/x.md", "from A\n")
	a.pushConfig()
	b.write("skills/other.md", "o\n")
	if _, err := b.tryPushConfig(); err == nil {
		t.Fatal("test premise: B's push must be refused as behind")
	}
	if got := b.recorded()["skills"].State; got != "diverged" {
		t.Errorf("after a refused push: %q, want diverged", got)
	}

	// A pull that leaves a conflict records unmerged.
	b.write("skills/x.md", "from B\n")
	b.deploy()
	if got := b.recorded()["skills"].State; got != "unmerged" {
		t.Errorf("after a pull that left a conflict: %q, want unmerged", got)
	}
	// ... and the unmerged paths are read from the area's synced base.
	if base, _ := b.base("skills"); len(base.Unmerged) != 1 {
		t.Errorf("base.Unmerged = %v", base.Unmerged)
	}
	st, _, _ := syncstate.Load(config.GlobalDir(), b.repo)
	if raw, _ := json.Marshal(st); strings.Contains(string(raw), "skills/x.md") {
		t.Errorf("unmerged paths must not be copied into sync state: %s", raw)
	}

	// A pull that leaves an incomplete entry records behind.
	c := newSnapMachine(t, ts.URL)
	c.deploy()
	f.forgeRecord(t, "workflows", 1, map[string]string{"workflows/w.md": snapsync.SHA([]byte("never uploaded\n"))})
	c.deploy()
	if got := c.recorded()["workflows"].State; got != "behind" {
		t.Errorf("after a pull that left an incomplete entry: %q, want behind", got)
	}
}

// AC6: on a fresh clone, rehydrate restores satelle.toml from the settings area.
func TestSyncRehydrateRestoresSettingsFromSnapshot(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	const allPersonal = "[sync]\nall = \"personal\"\n" + boundProjectToml
	a := &snapMachine{t: t, url: ts.URL, repo: syncConfigRepo(t, allPersonal), state: t.TempDir() + "/state-a.json"}
	a.write("skills/x.md", "x\n")
	a.pushConfig()

	// A fresh clone: `project bind` leaves only the binding in satelle.toml.
	b := &snapMachine{t: t, url: ts.URL, repo: syncConfigRepo(t, boundProjectToml), state: t.TempDir() + "/state-b.json"}
	b.use()
	cmd, buf := testCmd()
	outcome, err := runSyncConfigDeployOutcome(cmd, ts.URL, "", 0)
	if err != nil {
		t.Fatalf("deploy: %v\n%s", err, buf.String())
	}
	if !outcome.SettingsRestored {
		t.Fatalf("satelle.toml was not restored from the settings snapshot:\n%s", buf.String())
	}
	if got := b.read("satelle.toml"); !strings.Contains(got, `all = "personal"`) || !strings.Contains(got, `project = "probe"`) {
		t.Errorf("restored satelle.toml = %q", got)
	}
	if b.has("backups/sync-conflicts/settings/satelle.toml") {
		t.Error("the hosted settings were parked as a conflict instead of restored")
	}
	if got := b.recorded()["settings"].State; got != "in step" {
		t.Errorf("recorded settings = %q", got)
	}
	if summary := rehydrateDoneSummary(outcome); !strings.Contains(summary, "settings: restored") {
		t.Errorf("rehydrate's closing line = %q", summary)
	}
}

func TestRehydrateNoteNamesHostedSettingsKey(t *testing.T) {
	tempRepo(t)
	note := rehydrateAllLocalNote(false)
	if !strings.Contains(note, "settings area") || !strings.Contains(note, "`satelle.toml`") || !strings.Contains(note, "satelle sync status") {
		t.Errorf("note = %q", note)
	}
	if strings.Contains(note, "settings/satelle.toml") {
		t.Errorf("the hosted key is satelle.toml, not settings/satelle.toml: %q", note)
	}
}

// AC5: the areas `sync status` judges come from config.ConfigAreas, documents
// and the repo's [sync] scopes — not from where this repo keeps its files. Here
// the data dir (and so the constitution path) is relocated and [sync] all opts
// everything in.
func TestSyncStatusTakesAreasFromConfigNotLayout(t *testing.T) {
	testutil.IsolateHome(t)
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	repo := t.TempDir()
	meta := filepath.Join(repo, "meta")
	writeRepoFile(t, repo, "meta/satelle.toml", "data_dir = \"meta\"\n[sync]\nall = \"personal\"\n"+boundProjectToml)
	writeRepoFile(t, repo, "meta/constitution.md", "# c\n")
	writeRepoFile(t, repo, "meta/skills/x.md", "x\n")
	t.Setenv("SATELLE_CONFIG", filepath.Join(meta, "satelle.toml"))
	hosted.DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "doc-state.json")
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = "" })

	cmd, buf := testCmd()
	if err := runSyncStatus(cmd, ts.URL); err == nil {
		t.Fatalf("nothing is synced yet:\n%s", buf.String())
	}
	areas := append(append([]string{}, config.ConfigAreas...), "documents")
	for _, area := range areas {
		wantState(t, buf.String(), area, "never synced")
	}

	cmd, buf = testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}
	cmd, buf = testCmd()
	_ = runSyncStatus(cmd, ts.URL)
	for _, area := range []string{"constitution", "skills", "settings"} {
		wantState(t, buf.String(), area, "in step")
	}
	for _, area := range []string{"principles", "tasks"} {
		wantState(t, buf.String(), area, "never synced")
	}
}
