package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// Substrate continuity doctor checks (sty_7562055c): offline judgement of the
// authored areas and documents against the last synced base and the last
// recorded hosted check.

type substrateFixture struct {
	t          *testing.T
	repo, data string
	home       string
	now        time.Time
	// onlySkills puts every area but skills in step before each check, so a
	// test judges skills alone.
	onlySkills bool
}

// newSubstrateFixture writes a repo whose satelle.toml is toml (data dir ".satelle"
// unless the toml relocates it) with a signed-in machine whose last resolve for
// project p was personal.
func newSubstrateFixture(t *testing.T, dataSub, toml string) *substrateFixture {
	t.Helper()
	home := testutil.IsolateHome(t)
	hosted.DocumentSyncStatePathOverride = filepath.Join(home, "doc-state.json")
	prev := signedIn
	signedIn = func(string) bool { return true }
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = ""; signedIn = prev })
	repo, data := writeRepoTOML(t, t.TempDir(), toml)
	if dataSub != ".satelle" {
		moved := filepath.Join(repo, dataSub)
		if err := os.Rename(data, moved); err != nil {
			t.Fatal(err)
		}
		data = moved
	}
	f := &substrateFixture{t: t, repo: repo, data: data, home: home, now: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), onlySkills: true}
	f.scope(config.PersonalScope, "p")
	return f
}

const substrateToml = "[sync]\nproject = \"p\"\nstale_after = \"24h\"\n"

// scope records the scope the last sync resolved for binding.
func (f *substrateFixture) scope(scope config.Scope, binding string) {
	f.t.Helper()
	if err := syncstate.RecordScope(f.home, f.repo, scope, binding, f.now); err != nil {
		f.t.Fatal(err)
	}
}

// othersInStep records every substrate area but skills as synced from its
// current local files and checked in step.
func (f *substrateFixture) othersInStep() {
	f.t.Helper()
	cfg, _, err := config.Load(filepath.Join(f.data, config.ConfigName))
	if err != nil {
		f.t.Fatal(err)
	}
	ign, err := config.LoadIgnore(f.data)
	if err != nil {
		return // the check reports the unreadable ignore itself
	}
	abs, _ := filepath.Abs(f.repo)
	for _, area := range config.SubstrateAreas(config.PersonalScope) {
		if area == "skills" {
			continue
		}
		local, err := snapsync.LocalShas(cfg, f.repo, area, ign)
		if err != nil {
			f.t.Fatal(err)
		}
		if err := hosted.SaveAreaBase(config.DefaultHostedServer, "p", abs, area, hosted.AreaBase{Version: 1, Files: local}); err != nil {
			f.t.Fatal(err)
		}
		f.record(area, snapsync.StateInStep, f.now.Add(-time.Hour))
	}
}

func (f *substrateFixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.data, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// base records what this checkout last synced for an area.
func (f *substrateFixture) base(area string, files, unmerged map[string]string) {
	f.t.Helper()
	b := hosted.AreaBase{Version: 1, Files: map[string]string{}, Unmerged: unmerged}
	for p, body := range files {
		b.Files[p] = snapsync.SHA([]byte(body))
	}
	if err := hosted.SaveAreaBase(config.DefaultHostedServer, "p", f.repo, area, b); err != nil {
		f.t.Fatal(err)
	}
}

func (f *substrateFixture) record(area, state string, at time.Time) {
	f.t.Helper()
	if err := syncstate.RecordSubstrate(f.home, f.repo, map[string]string{area: state}, at); err != nil {
		f.t.Fatal(err)
	}
}

func (f *substrateFixture) check() health.Findings {
	if f.onlySkills {
		f.othersInStep()
	}
	return CheckSubstrateSync(f.repo, f.data, f.home, f.now)
}

// find returns the findings with the id.
func find(fs health.Findings, id string) health.Findings {
	var out health.Findings
	for _, f := range fs {
		if f.ID == id {
			out = append(out, f)
		}
	}
	return out
}

func inStepFixture(t *testing.T) *substrateFixture {
	f := newSubstrateFixture(t, ".satelle", substrateToml)
	f.write("skills/x.md", "x\n")
	f.base("skills", map[string]string{"skills/x.md": "x\n"}, nil)
	f.record("skills", snapsync.StateInStep, f.now.Add(-time.Hour))
	return f
}

// AC9 (sty_b336c2b0): the substrate check judges from the recorded resolved
// scope and the explicit [sync] project binding — an unbound repo (whatever its
// directory is called), a bound repo whose last resolve was local, and a bound
// repo whose recorded scope was for another binding are all local; a recorded
// team or personal scope for the current binding is judged.
func TestSubstrateJudgesFromRecordedScopeAndBinding(t *testing.T) {
	localCases := []struct {
		name, toml string
		set        func(f *substrateFixture)
		why        string
	}{
		{"unbound repo", "[sync]\nstale_after = \"24h\"\n", func(f *substrateFixture) {}, "unbound"},
		{"unbound with a recorded scope", "[sync]\nstale_after = \"24h\"\n", func(f *substrateFixture) { f.scope(config.TeamScope, filepath.Base(f.repo)) }, "unbound"},
		{"bound, recorded local", substrateToml, func(f *substrateFixture) { f.scope(config.LocalScope, "p") }, "resolved for project p"},
		{"bound, recorded for another binding", substrateToml, func(f *substrateFixture) { f.scope(config.TeamScope, "other") }, "not yet resolved for project p"},
	}
	for _, c := range localCases {
		t.Run(c.name, func(t *testing.T) {
			f := newSubstrateFixture(t, ".satelle", c.toml)
			f.onlySkills = false
			c.set(f)
			f.write("skills/x.md", "never pushed\n")
			got := f.check()
			if !got.OK() || len(got) != 1 || len(find(got, health.IDSyncSubstrateOk)) != 1 {
				t.Fatalf("a local scope must be one info finding: %v", got)
			}
			if !strings.Contains(got[0].Detail, "sync scope local") || !strings.Contains(got[0].Detail, c.why) {
				t.Fatalf("detail %q must say local (%s)", got[0].Detail, c.why)
			}
		})
	}
	for _, scope := range []config.Scope{config.PersonalScope, config.TeamScope} {
		t.Run("bound, recorded "+scope.String(), func(t *testing.T) {
			f := newSubstrateFixture(t, ".satelle", substrateToml)
			f.scope(scope, "p")
			f.write("skills/x.md", "never pushed\n")
			if got := find(f.check(), health.IDSyncSubstrateUnpushed); len(got) != 1 || got[0].Artifact != "skills" {
				t.Fatalf("a recorded %s scope must judge the substrate: %v", scope, f.check())
			}
		})
	}
}

func TestSubstrateInStepIsInfo(t *testing.T) {
	f := inStepFixture(t)
	got := f.check()
	if !got.OK() || len(find(got, health.IDSyncSubstrateOk)) != 1 || len(got) != 1 {
		t.Fatalf("an in-step machine must be exactly one info finding: %v", got)
	}
}

// An opted-in area holding nothing a push would send (init scaffold only) has no
// base and no drift: with its in-step record it raises no finding (sty_09b6b0e4).
func TestSubstrateScaffoldOnlyAreaIsInStep(t *testing.T) {
	f := newSubstrateFixture(t, ".satelle", substrateToml)
	f.record("skills", snapsync.StateInStep, f.now.Add(-time.Hour))
	got := f.check()
	if !got.OK() || len(find(got, health.IDSyncSubstrateOk)) != 1 || len(got) != 1 {
		t.Fatalf("a scaffold-only area must be exactly one info finding: %v", got)
	}
}

func TestSubstrateUnpushedNamesAreaAndRemediation(t *testing.T) {
	f := inStepFixture(t)
	f.write("skills/x.md", "edited\n")
	got := find(f.check(), health.IDSyncSubstrateUnpushed)
	if len(got) != 1 || got[0].Severity != health.SeverityError {
		t.Fatalf("want one unpushed error: %v", got)
	}
	if got[0].Artifact != "skills" || !strings.Contains(got[0].Detail, "skills/x.md") || !strings.Contains(got[0].Remediation, "satelle sync") {
		t.Errorf("finding does not name the area and remediation: %+v", got[0])
	}
}

func TestSubstrateNeverSyncedFilesAreUnpushed(t *testing.T) {
	f := newSubstrateFixture(t, ".satelle", substrateToml)
	f.write("skills/x.md", "x\n")
	got := find(f.check(), health.IDSyncSubstrateUnpushed)
	if len(got) != 1 || got[0].Title != "Substrate never synced" {
		t.Fatalf("want one never-synced finding: %v", got)
	}
}

func TestSubstrateUnmergedReadFromTheAreaBase(t *testing.T) {
	f := inStepFixture(t)
	sha := snapsync.SHA([]byte("theirs\n"))
	f.base("skills", map[string]string{"skills/x.md": "x\n"}, map[string]string{"skills/x.md": sha})
	f.write(filepath.ToSlash(snapsync.CopyPath("skills", "skills/x.md")), "theirs\n")
	got := find(f.check(), health.IDSyncSubstrateUnmerged)
	if len(got) != 1 || got[0].Artifact != "skills" || !strings.Contains(got[0].Detail, "skills/x.md") {
		t.Fatalf("want one unmerged error naming the path: %v", got)
	}

	// Deleting the conflict copy is how the operator says "merged": the base no
	// longer reports it unmerged, exactly as the driver reads it.
	if err := os.Remove(filepath.Join(f.data, filepath.FromSlash(snapsync.CopyPath("skills", "skills/x.md")))); err != nil {
		t.Fatal(err)
	}
	if got := find(f.check(), health.IDSyncSubstrateUnmerged); len(got) != 0 {
		t.Fatalf("a deleted conflict copy still reads unmerged: %v", got)
	}
}

func TestSubstrateRecordedBehindAndDiverged(t *testing.T) {
	for _, state := range []string{snapsync.StateBehind, snapsync.StateDiverged} {
		f := inStepFixture(t)
		f.record("skills", state, f.now.Add(-time.Hour))
		got := find(f.check(), health.IDSyncSubstrateBehind)
		if len(got) != 1 || got[0].Artifact != "skills" || got[0].Title != "Substrate "+state {
			t.Fatalf("%s: %v", state, got)
		}
		// Behind is a plain pull; diverged is a choice between two sides' changes.
		want, not := "satelle sync pull", "satelle sync reconcile"
		if state == snapsync.StateDiverged {
			want, not = "satelle sync reconcile", "satelle sync pull"
			if !strings.Contains(got[0].Remediation, "then `satelle sync push`") {
				t.Errorf("diverged: remediation %q must end with the push", got[0].Remediation)
			}
		}
		if !strings.Contains(got[0].Remediation, want) || strings.Contains(got[0].Remediation, not) {
			t.Errorf("%s: remediation %q must name %q and not %q", state, got[0].Remediation, want, not)
		}
	}
}

func TestSubstrateStaleOrAbsentCheck(t *testing.T) {
	f := inStepFixture(t)
	f.record("skills", snapsync.StateInStep, f.now.Add(-48*time.Hour))
	got := find(f.check(), health.IDSyncSubstrateUnchecked)
	if len(got) != 1 || !strings.Contains(got[0].Detail, "threshold 24h0m0s") || !strings.Contains(got[0].Remediation, "satelle sync status") {
		t.Fatalf("a stale check must be an error naming the threshold: %v", got)
	}

	absent := newSubstrateFixture(t, ".satelle", substrateToml)
	absent.write("skills/x.md", "x\n")
	absent.base("skills", map[string]string{"skills/x.md": "x\n"}, nil)
	got = find(absent.check(), health.IDSyncSubstrateUnchecked)
	if len(got) != 1 || !strings.Contains(got[0].Detail, "never checked") {
		t.Fatalf("an absent check must be an error: %v", got)
	}
}

func TestSubstrateStaleAfterUnsetWarnsButStillJudges(t *testing.T) {
	f := newSubstrateFixture(t, ".satelle", "[sync]\nproject = \"p\"\n")
	f.write("skills/x.md", "x\n")
	f.base("skills", map[string]string{"skills/x.md": "x\n"}, nil)
	got := f.check()
	warns := got.WithSeverity(health.SeverityWarn)
	if len(warns) != 1 || warns[0].ID != health.IDSyncConfig || !strings.Contains(warns[0].Title, "staleness not judged") {
		t.Fatalf("want the not-judged warning: %v", got)
	}
	if !got.OK() || len(find(got, health.IDSyncSubstrateUnchecked)) != 0 {
		t.Fatalf("an absent check must not be an error when staleness is not judged: %v", got)
	}

	f.write("skills/x.md", "edited\n")
	f.record("skills", snapsync.StateBehind, f.now)
	got = f.check()
	if len(find(got, health.IDSyncSubstrateUnpushed)) != 1 || len(find(got, health.IDSyncSubstrateBehind)) != 1 {
		t.Fatalf("unpushed and recorded states must still be reported: %v", got)
	}
}

func TestSubstrateUnboundNamesLoginOrBind(t *testing.T) {
	f := inStepFixture(t)
	signedIn = func(string) bool { return false }
	got := find(f.check(), health.IDSyncSubstrateUnbound)
	if len(got) != 1 || got[0].Severity != health.SeverityError || !strings.Contains(got[0].Remediation, "satelle login") || !strings.Contains(got[0].Remediation, "satelle project bind") {
		t.Fatalf("want an unbound error naming login and bind: %v", got)
	}
}

// AC5: areas come from config.ConfigAreas + documents and the repo's resolved
// scope, never from this repo's layout — a relocated data dir and a recorded
// personal scope.
func TestSubstrateAreasComeFromConfigNotLayout(t *testing.T) {
	f := newSubstrateFixture(t, "meta", "data_dir = \"meta\"\n[sync]\nproject = \"p\"\nstale_after = \"24h\"\n")
	f.onlySkills = false
	f.write("skills/x.md", "x\n")
	want := config.SubstrateAreas(config.PersonalScope)
	if len(want) != len(config.ConfigAreas)+1 {
		t.Fatalf("SubstrateAreas under a personal scope: %v", want)
	}
	checked := map[string]bool{}
	for _, fd := range find(f.check(), health.IDSyncSubstrateUnchecked) {
		checked[fd.Artifact] = true
	}
	for _, area := range want {
		if !checked[area] {
			t.Errorf("area %q was not judged: %v", area, checked)
		}
	}
	unpushed := find(f.check(), health.IDSyncSubstrateUnpushed)
	found := false
	for _, fd := range unpushed {
		if fd.Artifact == "skills" && strings.Contains(fd.Detail, "skills/x.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("a skill under the relocated data dir was not seen: %v", unpushed)
	}
	for _, fd := range f.check() {
		if strings.Contains(fd.Detail+fd.Title, "claude") || strings.Contains(fd.Detail+fd.Title, "grok") {
			t.Errorf("provider-specific text in %+v", fd)
		}
	}
}

// Check (not just CheckSubstrateSync) refuses PASS while substrate is unpushed.
func TestCheckFailsUnpushedSubstrate(t *testing.T) {
	root := newFixtureRepo(t, fixtureOpts{
		extraFiles: map[string]string{"satelle.toml": substrateToml},
	})
	hosted.DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "doc-state.json")
	prev := signedIn
	signedIn = func(string) bool { return true }
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = ""; signedIn = prev })
	if err := syncstate.RecordScope(os.Getenv("SATELLE_HOME"), root, config.PersonalScope, "p", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".satelle", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".satelle", "skills", "x.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := check(t, root)
	if r.OK || !ids(r)[health.IDSyncSubstrateUnpushed] {
		t.Fatalf("unpushed substrate must not PASS: %v", r.Findings)
	}
}

// A path .satelleignore lists is never a difference (sty_73765f96): one that was
// never pushed, one whose local content differs from the recorded base and one
// deleted since it all leave the area in step. Each is a difference without the
// ignore.
func TestSubstrateIgnoredPathIsNeverDrift(t *testing.T) {
	cases := []struct {
		name string
		set  func(f *substrateFixture)
	}{
		{"never pushed", func(f *substrateFixture) { f.write("skills/secret.md", "mine\n") }},
		{"differs from the recorded base", func(f *substrateFixture) {
			f.base("skills", map[string]string{"skills/x.md": "x\n", "skills/secret.md": "base\n"}, nil)
			f.write("skills/secret.md", "edited\n")
		}},
		{"deleted since the recorded base", func(f *substrateFixture) {
			f.base("skills", map[string]string{"skills/x.md": "x\n", "skills/secret.md": "base\n"}, nil)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := inStepFixture(t)
			c.set(f)
			if got := find(f.check(), health.IDSyncSubstrateUnpushed); len(got) != 1 {
				t.Fatalf("without an ignore the path must read as unpushed: %v", f.check())
			}
			f.write(config.IgnoreFileName, "secret.md\n")
			got := f.check()
			if !got.OK() || len(find(got, health.IDSyncSubstrateUnpushed)) != 0 || len(got) != 1 {
				t.Fatalf("an ignored path must leave the substrate in step: %v", got)
			}
		})
	}
}

// An unreadable .satelleignore is reported, not read as empty.
func TestSubstrateUnreadableIgnoreIsNotJudged(t *testing.T) {
	f := inStepFixture(t)
	if err := os.Mkdir(filepath.Join(f.data, config.IgnoreFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := f.check(); len(find(got, health.IDSyncConfig)) == 0 {
		t.Fatalf("want a sync config warning for an unreadable %s: %v", config.IgnoreFileName, got)
	}
}
