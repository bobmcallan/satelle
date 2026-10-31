package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/doctor"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// Tests for sty_04e8c778: an area whose hosted copy holds only files the installed
// binary supplies (unedited embedded defaults) is in step, whatever its local
// tree lacks — in `sync status`, `sync pull`, `sync reconcile` and doctor alike.

const tasksSyncToml = "[sync]\n" + boundProjectToml

// tasksMachine is a signed-in personal-scope checkout holding only the tasks
// README scaffold, with every other area in step so a test judges tasks alone.
func tasksMachine(t *testing.T) (*snapMachine, *snapFake) {
	t.Helper()
	testutil.IsolateHome(t)
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	m := &snapMachine{t: t, url: ts.URL, repo: syncConfigRepo(t, tasksSyncToml), state: filepath.Join(t.TempDir(), "document-sync-state.json")}
	m.write("tasks/README.md", "# tasks\n")
	m.inStepExcept(f, "tasks")
	return m, f
}

// inStepExcept records every config area but the named ones as synced from the
// machine's current files, on the hosted copy and in its base, so a test judges
// only the areas it names.
func (m *snapMachine) inStepExcept(f *snapFake, except ...string) {
	m.t.Helper()
	m.use()
	cfg, repoRoot, _, err := loadRepoConfig()
	if err != nil {
		m.t.Fatal(err)
	}
	for _, area := range config.ConfigAreas {
		if slices.Contains(except, area) {
			continue
		}
		files, err := config.AreaFiles(cfg, repoRoot, area, config.Ignore{})
		if err != nil {
			m.t.Fatal(err)
		}
		if len(files) == 0 {
			continue
		}
		shas := map[string]string{}
		for _, cf := range files {
			f.cfg.put("probe", cf.Path, cf.Content)
			shas[cf.Path] = snapsync.SHA(cf.Content)
		}
		m.adopt(area, f.forgeRecord(m.t, area, 0, shas), shas)
	}
}

// hostSnapshot puts the files on the hosted copy as a snapshot (parent = the
// current head) and returns its version and the sha of each path.
func hostSnapshot(t *testing.T, f *snapFake, parent int, files map[string]string) (int, map[string]string) {
	t.Helper()
	shas := map[string]string{}
	for p, body := range files {
		f.cfg.put("probe", p, []byte(body))
		shas[p] = snapsync.SHA([]byte(body))
	}
	return f.forgeRecord(t, "tasks", parent, shas), shas
}

// adopt records the base a machine holds after syncing the snapshot.
func (m *snapMachine) adopt(area string, version int, files map[string]string) {
	m.t.Helper()
	m.use()
	if err := hosted.SaveAreaBase(m.url, "probe", m.repo, area, hosted.AreaBase{Version: version, Files: files}); err != nil {
		m.t.Fatal(err)
	}
}

// taskDefaults is two stamped unedited embedded-default task copies by path.
func taskDefaults(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"tasks/tsk_substrate-audit.md": stampedDefault(embeddedBodyOf(t, "tasks", "tsk_substrate-audit")),
		"tasks/tsk_context-audit.md":   stampedDefault(embeddedBodyOf(t, "tasks", "tsk_context-audit")),
	}
}

func (m *snapMachine) writeAll(files map[string]string) {
	m.t.Helper()
	for p, body := range files {
		m.write(p, body)
	}
}

func copyFiles(files map[string]string, more map[string]string) map[string]string {
	out := map[string]string{}
	for p, b := range files {
		out[p] = b
	}
	for p, b := range more {
		out[p] = b
	}
	return out
}

// substrateFindings is what doctor's substrate check raises for the machine.
func (m *snapMachine) substrateFindings() health.Findings {
	m.t.Helper()
	m.use()
	_, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		m.t.Fatal(err)
	}
	return doctor.CheckSubstrateSync(repoRoot, dataDir, config.GlobalDir(), time.Now())
}

// wantTasksQuiet asserts the area is settled everywhere an operator reads it: the
// recorded state, and doctor's substrate check.
func (m *snapMachine) wantTasksQuiet() {
	m.t.Helper()
	if got := m.recorded()["tasks"].State; got != snapsync.StateInStep {
		m.t.Errorf("recorded tasks state = %q, want %q", got, snapsync.StateInStep)
	}
	for _, x := range m.substrateFindings() {
		if x.ID == health.IDSyncSubstrateBehind || x.ID == health.IDSyncSubstrateUnpushed || x.Severity == health.SeverityError {
			m.t.Errorf("doctor raised %s: %s", x.ID, x.Detail)
		}
	}
}

// wantPullQuiet runs a pull that must exit zero and suggest no next step.
func (m *snapMachine) wantPullQuiet() string {
	m.t.Helper()
	out, err := m.pull()
	if err != nil {
		m.t.Fatalf("sync pull: %v\n%s", err, out)
	}
	for _, bad := range []string{"next: satelle sync pull", "still behind", "next:"} {
		if strings.Contains(out, bad) {
			m.t.Errorf("sync pull printed %q:\n%s", bad, out)
		}
	}
	return out
}

func (m *snapMachine) wantNoDifferences() {
	m.t.Helper()
	out, err := m.reconcile("tasks")
	if err != nil {
		m.t.Fatalf("reconcile: %v\n%s", err, out)
	}
	if !strings.Contains(out, "tasks: no differences") || strings.Contains(out, "deleted here only") {
		m.t.Errorf("reconcile must find no differences:\n%s", out)
	}
}

func (m *snapMachine) taskFiles() []string {
	m.t.Helper()
	var out []string
	for _, p := range listTree(m.t, m.path("tasks")) {
		out = append(out, p)
	}
	return out
}

// AC2, AC4: the tree holds the stamped unedited defaults and the scaffold; the base
// and the hosted snapshot hold the defaults. Nothing differs.
func TestSyncTasksHoldingUneditedDefaultsIsInStep(t *testing.T) {
	m, f := tasksMachine(t)
	defaults := taskDefaults(t)
	m.writeAll(defaults)
	v, shas := hostSnapshot(t, f, 0, defaults)
	m.adopt("tasks", v, shas)

	out, err := m.status()
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if cols := wantState(t, out, "tasks", "in step"); cols[4] != "-" {
		t.Errorf("tasks next = %q, want -", cols[4])
	}
	m.wantPullQuiet()
	m.wantNoDifferences()
	m.wantTasksQuiet()
}

// AC3, AC4: the hosted copy holds unedited defaults this tree has no file for.
func TestSyncTasksPullWritesNothingForAHostedUneditedDefault(t *testing.T) {
	for _, tc := range []struct {
		name    string
		baseHas bool
	}{{"base holds it", true}, {"no base", false}} {
		t.Run(tc.name, func(t *testing.T) {
			m, f := tasksMachine(t)
			v, shas := hostSnapshot(t, f, 0, taskDefaults(t))
			if tc.baseHas {
				m.adopt("tasks", v, shas)
			}

			out, err := m.status()
			if err != nil {
				t.Fatalf("status: %v\n%s", err, out)
			}
			wantState(t, out, "tasks", "in step")
			for i := 0; i < 2; i++ {
				m.wantPullQuiet()
				if got := m.taskFiles(); len(got) != 1 || got[0] != "README.md" {
					t.Fatalf("pull %d wrote %v, want only README.md", i+1, got)
				}
			}
			m.wantNoDifferences()
			m.wantTasksQuiet()
		})
	}
}

// AC3: a hosted default in a stamp the binary cannot predict is fetched once,
// proven an unedited default and recorded; the area is then in step and a second
// pull does nothing.
func TestSyncTasksPullRecordsAnOlderStampedDefault(t *testing.T) {
	m, f := tasksMachine(t)
	audit := embeddedBodyOf(t, "tasks", "tsk_substrate-audit")
	const path = "tasks/tsk_substrate-audit.md"
	older := stampedDefault(audit + "\nan older default line\n")
	_, shas := hostSnapshot(t, f, 0, map[string]string{path: older})

	out, _ := m.status()
	wantState(t, out, "tasks", "behind")

	pulled := m.wantPullQuiet()
	if !strings.Contains(pulled, "kept binary default") {
		t.Errorf("the default the binary supplies is not reported:\n%s", pulled)
	}
	if got := m.taskFiles(); len(got) != 1 {
		t.Errorf("pull wrote %v", got)
	}
	base, ok := m.base("tasks")
	if !ok || base.Defaults[path] != shas[path] {
		t.Errorf("base = %+v, want the proven default recorded for %s", base, path)
	}
	out, err := m.status()
	if err != nil {
		t.Fatalf("status after the pull: %v\n%s", err, out)
	}
	wantState(t, out, "tasks", "in step")
	m.wantPullQuiet()
	m.wantNoDifferences()
	m.wantTasksQuiet()
}

// AC9: an authored task beside hosted unedited defaults, base equal to the hosted
// snapshot: in step (not unpushed), pull exits zero, doctor raises nothing.
func TestSyncTasksAuthoredBesideHostedDefaultsIsInStep(t *testing.T) {
	m, f := tasksMachine(t)
	defaults := taskDefaults(t)
	hostedFiles := copyFiles(defaults, map[string]string{"tasks/tsk_mine.md": "---\nid: tsk_mine\ntype: task\n---\n# mine\n"})
	for _, onDisk := range []bool{true, false} {
		name := "defaults absent from the tree"
		if onDisk {
			name = "defaults on disk"
		}
		t.Run(name, func(t *testing.T) {
			m, f = tasksMachine(t)
			if onDisk {
				m.writeAll(defaults)
			}
			m.write("tasks/tsk_mine.md", hostedFiles["tasks/tsk_mine.md"])
			v, shas := hostSnapshot(t, f, 0, hostedFiles)
			m.adopt("tasks", v, shas)

			out, err := m.status()
			if err != nil {
				t.Fatalf("status: %v\n%s", err, out)
			}
			wantState(t, out, "tasks", "in step")
			m.wantPullQuiet()
			m.wantNoDifferences()
			m.wantTasksQuiet()
		})
	}
}

// AC6: pushing an authored task beside hosted defaults, then editing it and
// pushing again, keeps the task in the hosted snapshot; reconcile never calls an
// existing edited task deleted here.
func TestSyncTasksPushKeepsAnEditedTaskBesideDefaults(t *testing.T) {
	m, f := tasksMachine(t)
	defaults := taskDefaults(t)
	m.writeAll(defaults)
	v, shas := hostSnapshot(t, f, 0, defaults)
	m.adopt("tasks", v, shas)

	const mine = "tasks/tsk_mine.md"
	m.write(mine, "---\nid: tsk_mine\ntype: task\n---\n# mine\n")
	first := m.pushConfig()
	if strings.Contains(first, "deleted") || strings.Contains(first, "removed") {
		t.Errorf("a push must not list a dropped default as a deletion:\n%s", first)
	}
	m.wantNoDifferences()

	edited := "---\nid: tsk_mine\ntype: task\n---\n# mine, edited\n"
	m.write(mine, edited)
	out, err := m.reconcile("tasks")
	if err != nil || strings.Contains(out, "deleted here only") {
		t.Errorf("an existing edited task is not deleted here: %v\n%s", err, out)
	}
	m.pushConfig()

	raw, _, ok := f.cfg.get("probe", snapsync.RecordPath("tasks"), 0)
	if !ok {
		t.Fatal("no tasks snapshot record")
	}
	rec, err := snapsync.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Files[mine] != snapsync.SHA([]byte(edited)) {
		t.Errorf("the claimed snapshot lost the edited task: %v", rec.Files)
	}
	for p := range defaults {
		if _, has := rec.Files[p]; has {
			t.Errorf("the claimed snapshot carries the unedited default %s", p)
		}
	}
	m.wantNoDifferences()
	m.wantTasksQuiet()
}

// AC7: hosted bytes for an embedded-default task path that are not an unedited
// default are written, with or without a base, and after an earlier pull classed
// the path a binary default.
func TestSyncTasksPullWritesHostedBytesThatAreNotTheDefault(t *testing.T) {
	const path = "tasks/tsk_substrate-audit.md"
	edited := stampedDefault(embeddedBodyOf(t, "tasks", "tsk_substrate-audit")) + "\noperator edit\n"

	t.Run("first pull with no base", func(t *testing.T) {
		m, f := tasksMachine(t)
		hostSnapshot(t, f, 0, map[string]string{path: edited})
		out, _ := m.status()
		wantState(t, out, "tasks", "behind")
		m.wantPullQuiet()
		if got := m.read(path); got != edited {
			t.Errorf("the hosted edit was not written: %q", got)
		}
		out, err := m.status()
		if err != nil {
			t.Fatalf("status: %v\n%s", err, out)
		}
		wantState(t, out, "tasks", "in step")
	})

	t.Run("after an earlier pull classed it a binary default", func(t *testing.T) {
		m, f := tasksMachine(t)
		v, _ := hostSnapshot(t, f, 0, taskDefaults(t))
		m.wantPullQuiet()
		if m.has(path) {
			t.Fatal("test premise: the default was written")
		}
		hostSnapshot(t, f, v, map[string]string{path: edited})
		out, _ := m.status()
		wantState(t, out, "tasks", "behind")
		m.wantPullQuiet()
		if got := m.read(path); got != edited {
			t.Errorf("the hosted edit was not written: %q", got)
		}
	})
}

// AC5: reconcile names a directory-area path by its server key once, and its
// --diff resolves that name; the settings area (a single file) is still named
// <area>/<path>.
func TestSyncReconcileNamesAServerKeyOnce(t *testing.T) {
	testutil.IsolateHome(t)
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	toml := "[sync]\n" + boundProjectToml
	m := &snapMachine{t: t, url: ts.URL, repo: syncConfigRepo(t, toml), state: filepath.Join(t.TempDir(), "document-sync-state.json")}
	m.write("tasks/tsk_a.md", "---\nid: tsk_a\ntype: task\n---\nlocal\n")
	hostSnapshot(t, f, 0, map[string]string{"tasks/tsk_a.md": "---\nid: tsk_a\ntype: task\n---\nhosted\n"})
	f.cfg.put("probe", "satelle.toml", []byte("[sync]\n"))
	f.forgeRecord(t, "settings", 0, map[string]string{"satelle.toml": snapsync.SHA([]byte("[sync]\n"))})

	out, err := m.reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out)
	}
	if !strings.Contains(out, "tasks/tsk_a.md") || strings.Contains(out, "tasks/tasks/") {
		t.Errorf("the preview must name tasks/tsk_a.md once:\n%s", out)
	}
	diff, err := m.reconcile("--diff", "tasks/tsk_a.md")
	if err != nil {
		t.Fatalf("--diff tasks/tsk_a.md: %v\n%s", err, diff)
	}
	if !strings.Contains(diff, "tasks/tsk_a.md — ") || strings.Contains(diff, "tasks/tasks/") {
		t.Errorf("the diff header must name tasks/tsk_a.md once:\n%s", diff)
	}
	if _, err := m.reconcile("--diff", "tasks/tasks/tsk_a.md"); err == nil {
		t.Error("the doubled tasks/tasks/tsk_a.md names no path")
	}
	diff, err = m.reconcile("--diff", "settings/satelle.toml")
	if err != nil {
		t.Fatalf("--diff settings/satelle.toml: %v\n%s", err, diff)
	}
	if !strings.Contains(diff, "settings/satelle.toml — ") {
		t.Errorf("the settings diff header = \n%s", diff)
	}
	if _, err := os.Stat(m.path("tasks/tsk_a.md")); err != nil {
		t.Errorf("reconcile preview touched the tree: %v", err)
	}
}
