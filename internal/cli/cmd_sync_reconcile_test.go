package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// Two-machine tests for `satelle sync reconcile` (sty_b0873ec3), in the snapshot
// sync harness: two checkouts, one fake hosted server, real commands.

func (m *snapMachine) reconcile(args ...string) (string, error) {
	m.use()
	cmd := newSyncReconcileCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"--server", m.url}, args...))
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// backupsDir is where this machine's runtime backups live.
func (m *snapMachine) backupsDir() string {
	m.t.Helper()
	m.use()
	cfg, repoRoot, _, err := loadRepoConfig()
	if err != nil {
		m.t.Fatal(err)
	}
	return filepath.Join(cfg.ResolveRuntimeDir(repoRoot).Dir, "backups")
}

// digest hashes every file under the roots (a file is its own root), so a test
// can assert a command changed nothing: the data dir, the runtime backups, the
// recorded base and the recorded sync state.
func (m *snapMachine) digest() string {
	m.t.Helper()
	h := sha256.New()
	for _, root := range []string{filepath.Join(m.repo, ".satelle"), m.backupsDir(), m.state, config.GlobalDir()} {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			body, _ := os.ReadFile(p)
			h.Write([]byte(p + "\x00"))
			h.Write(body)
			return nil
		})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m *snapMachine) mustReconcile(args ...string) string {
	m.t.Helper()
	out, err := m.reconcile(args...)
	if err != nil {
		m.t.Fatalf("reconcile %v: %v\n%s", args, err, out)
	}
	return out
}

// reconcileFixture leaves skills diverged: A published snapshot 2 and B changed
// the area since it last synced snapshot 1.
//
//	x.md       changed on both       y.md    changed on both
//	aonly.md   changed on hosted     anew.md added on hosted    del.md deleted on hosted
//	bonly.md   changed here          bnew.md added here
func reconcileFixture(t *testing.T) (a, b *snapMachine) {
	t.Helper()
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a, b = newSnapMachine(t, ts.URL), newSnapMachine(t, ts.URL)
	for _, f := range []string{"x", "y", "aonly", "bonly", "del", "stay"} {
		a.write("skills/"+f+".md", f+"0\n")
	}
	a.pushConfig()
	b.deploy()

	a.write("skills/x.md", "x from A\n")
	a.write("skills/y.md", "y from A\n")
	a.write("skills/aonly.md", "aonly1\n")
	a.write("skills/anew.md", "anew\n")
	a.remove("skills/del.md")
	a.pushConfig() // snapshot 2

	b.write("skills/x.md", "x from B\n")
	b.write("skills/y.md", "y from B\n")
	b.write("skills/bonly.md", "bonly1\n")
	b.write("skills/bnew.md", "bnew\n")
	return a, b
}

// AC1: the preview lists every differing path with its class and the default,
// and changes nothing.
func TestReconcilePreviewListsBothSidesAndWritesNothing(t *testing.T) {
	_, b := reconcileFixture(t)
	before := b.digest()
	out := b.mustReconcile()
	if after := b.digest(); after != before {
		t.Errorf("the preview changed the data dir, backups, base or recorded state\n%s", out)
	}
	for path, class := range map[string]string{
		"skills/x.md":     "changed on both",
		"skills/y.md":     "changed on both",
		"skills/aonly.md": "changed on hosted only",
		"skills/anew.md":  "added on hosted only",
		"skills/del.md":   "deleted on hosted only",
		"skills/bonly.md": "changed here only",
		"skills/bnew.md":  "added here only",
	} {
		line := reconcileLine(out, path)
		if !strings.Contains(line, class) {
			t.Errorf("%s: want class %q, got line %q\n%s", path, class, line, out)
		}
	}
	if !strings.Contains(reconcileLine(out, "skills/x.md"), "park the hosted copy as a conflict") {
		t.Errorf("the default for a path changed on both is not named:\n%s", out)
	}
	if !strings.Contains(out, "hosted snapshot 2") || !strings.Contains(out, "local base snapshot 1") {
		t.Errorf("the versions on each side are not named:\n%s", out)
	}
	if _, err := b.reconcile("--take-local", "skills/x.md"); err == nil || !strings.Contains(err.Error(), "--apply") {
		t.Errorf("a choice without --apply must be refused: %v", err)
	}
	// A named area narrows the preview.
	if narrowed := b.mustReconcile("workflows"); strings.Contains(narrowed, "skills/x.md") {
		t.Errorf("a named area must narrow the preview:\n%s", narrowed)
	}
}

func reconcileLine(out, sub string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

// AC2: --diff prints the hosted copy against the local one, labelled with the
// hosted snapshot, and shows an absent side.
func TestReconcileDiffShowsBothSides(t *testing.T) {
	_, b := reconcileFixture(t)
	before := b.digest()

	out := b.mustReconcile("--diff", "skills/x.md")
	for _, want := range []string{"--- hosted (snapshot 2) skills/x.md", "+++ local skills/x.md", "-x from A", "+x from B", "changed on both"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff missing %q:\n%s", want, out)
		}
	}
	// The <area>/<path> form names the same path.
	if qualified := b.mustReconcile("--diff", "skills/skills/x.md"); !strings.Contains(qualified, "-x from A") {
		t.Errorf("<area>/<path> not accepted:\n%s", qualified)
	}
	out = b.mustReconcile("--diff", "skills/anew.md")
	if !strings.Contains(out, "(absent on local)") || !strings.Contains(out, "-anew") {
		t.Errorf("a path only on hosted must show local as absent:\n%s", out)
	}
	out = b.mustReconcile("--diff", "skills/bnew.md")
	if !strings.Contains(out, "(absent on hosted)") || !strings.Contains(out, "+bnew") {
		t.Errorf("a path only here must show hosted as absent:\n%s", out)
	}
	if _, err := b.reconcile("--diff", "skills/nope.md"); err == nil || !strings.Contains(err.Error(), "skills/nope.md") {
		t.Errorf("an unknown path must be refused by name: %v", err)
	}
	if after := b.digest(); after != before {
		t.Error("--diff changed something")
	}
}

// AC3 + AC4: each chosen path takes the chosen side, an unchosen path changed on
// both sides is parked, a refused choice writes nothing, and what each choice
// replaces is saved first.
func TestReconcileApplyHonoursChoicesAndParksUnchosen(t *testing.T) {
	_, b := reconcileFixture(t)

	// Refusals name the path and change nothing.
	before := b.digest()
	out, err := b.reconcile("--apply", "--take-local", "skills/x.md", "--take-hosted", "skills/nope.md")
	if err == nil || !strings.Contains(err.Error(), "skills/nope.md") {
		t.Fatalf("a choice naming a path outside the area must be refused by name: %v\n%s", err, out)
	}
	if _, err := b.reconcile("--apply", "--take-local", "skills/y.md", "--take-hosted", "skills/stay.md"); err == nil || !strings.Contains(err.Error(), "skills/stay.md") || !strings.Contains(err.Error(), "does not differ") {
		t.Fatalf("a choice naming a path that does not differ must be refused by name: %v", err)
	}
	if _, err := b.reconcile("--apply", "--take-local", "skills/x.md", "--take-hosted", "skills/x.md"); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("a path chosen both ways must be refused: %v", err)
	}
	if b.digest() != before {
		t.Fatal("a refused choice changed the tree, backups or recorded state")
	}

	out = b.mustReconcile("--apply",
		"--take-local", "skills/x.md", // changed on both: keep mine
		"--take-hosted", "skills/bonly.md", // changed here only: replace mine
		"--take-local", "skills/anew.md", // added on hosted: do not have it
		"--take-hosted", "skills/bnew.md", // added here only: drop it
		"--take-local", "skills/del.md", // deleted on hosted: keep it
	)
	for path, want := range map[string]string{
		"skills/x.md":     "x from B\n",
		"skills/y.md":     "y from B\n", // unchosen: kept, conflict parked
		"skills/aonly.md": "aonly1\n",   // unchosen: the plain pull carried it over
		"skills/bonly.md": "bonly0\n",
	} {
		if got := b.read(path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if b.has("skills/anew.md") || b.has("skills/bnew.md") {
		t.Error("a hosted addition taken local, or a local addition taken hosted, is still on disk")
	}
	if b.read("skills/del.md") != "del0\n" {
		t.Errorf("a hosted deletion taken local must keep the file, got %q", b.read("skills/del.md"))
	}
	if got := b.read("backups/sync-conflicts/skills/y.md"); got != "y from A\n" {
		t.Errorf("an unchosen path changed on both sides must be parked as a conflict, copy = %q", got)
	}
	if b.has("backups/sync-conflicts/skills/x.md") {
		t.Error("a chosen conflict left a conflict copy")
	}
	base, _ := b.base("skills")
	if base.Version != 2 || base.Unmerged["skills/y.md"] == "" || base.Unmerged["skills/x.md"] != "" {
		t.Errorf("base after the reconcile = %+v: want snapshot 2 with only y.md unmerged", base)
	}

	// AC4: what each choice replaced is saved, and reported.
	saved := b.backupsDir() + "/sync-replaced"
	for rel, want := range map[string]string{
		"hosted/skills/skills/x.md":     "x from A\n",
		"local/skills/skills/bonly.md":  "bonly1\n",
		"hosted/skills/skills/anew.md":  "anew\n",
		"local/skills/skills/bnew.md":   "bnew\n",
		"local/skills/skills/aonly.md":  "", // the default pull replaced it too (not a choice): not saved
		"hosted/skills/skills/bonly.md": "",
	} {
		got, err := os.ReadFile(filepath.Join(saved, rel))
		if want == "" {
			if err == nil {
				t.Errorf("%s was saved but no choice replaced it", rel)
			}
			continue
		}
		if err != nil || string(got) != want {
			t.Errorf("saved %s = %q (%v), want %q", rel, got, err, want)
		}
		if !strings.Contains(out, filepath.Join(saved, rel)) {
			t.Errorf("the saved path %s is not reported:\n%s", filepath.Join(saved, rel), out)
		}
	}
}

// AC4: taking hosted saves the local bytes, taking local saves the hosted bytes,
// for a documents area as well as a config area.
func TestReconcileSavesOverriddenSideForDocuments(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a, b := newSnapMachine(t, ts.URL), newSnapMachine(t, ts.URL)
	a.write("documents/d.md", "d0\n")
	a.write("documents/e.md", "e0\n")
	a.pushDocs()
	b.pullDocs()
	a.write("documents/d.md", "d from A\n")
	a.write("documents/e.md", "e from A\n")
	a.pushDocs()
	b.write("documents/d.md", "d from B\n")
	b.write("documents/e.md", "e from B\n")

	b.mustReconcile("--apply", "--take-local", "documents/d.md", "--take-hosted", "documents/e.md")
	if b.read("documents/d.md") != "d from B\n" || b.read("documents/e.md") != "e from A\n" {
		t.Errorf("d=%q e=%q", b.read("documents/d.md"), b.read("documents/e.md"))
	}
	saved := b.backupsDir() + "/sync-replaced"
	for rel, want := range map[string]string{
		"hosted/documents/documents/d.md": "d from A\n",
		"local/documents/documents/e.md":  "e from B\n",
	} {
		if got, err := os.ReadFile(filepath.Join(saved, rel)); err != nil || string(got) != want {
			t.Errorf("saved %s = %q (%v), want %q", rel, got, err, want)
		}
	}
}

// AC4: a save that fails leaves the tree as it was — nothing is staged.
func TestReconcileSaveFailureStagesNothing(t *testing.T) {
	_, b := reconcileFixture(t)
	// A file where the backups directory belongs makes every save fail.
	backups := b.backupsDir()
	if err := os.MkdirAll(filepath.Dir(backups), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backups, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := b.digest()
	out, err := b.reconcile("--apply", "--take-hosted-all", "skills")
	if err == nil {
		t.Fatalf("a failing save must fail the reconcile:\n%s", out)
	}
	if b.digest() != before {
		t.Error("a reconcile whose save failed changed the tree, base or recorded state")
	}
	if b.read("skills/x.md") != "x from B\n" || b.read("skills/aonly.md") != "aonly0\n" || b.has("skills/anew.md") {
		t.Error("files changed although the save failed")
	}
}

// AC4: every area's saves happen before any area stages. Only the documents save
// fails here (a file sits where its directory belongs) while the config area's
// would succeed, so a reconcile that restored the config area first would leave a
// partly reconciled tree.
func TestReconcileDocumentsSaveFailureStagesNoConfigArea(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a, b := newSnapMachine(t, ts.URL), newSnapMachine(t, ts.URL)
	a.write("skills/s.md", "s0\n")
	a.write("documents/d.md", "d0\n")
	a.pushConfig()
	a.pushDocs()
	b.deploy()
	b.pullDocs()
	a.write("skills/s.md", "s from A\n")
	a.write("documents/d.md", "d from A\n")
	a.pushConfig()
	a.pushDocs()
	b.write("skills/s.md", "s from B\n")
	b.write("documents/d.md", "d from B\n")

	replacedLocal := filepath.Join(b.backupsDir(), "sync-replaced", "local")
	if err := os.MkdirAll(replacedLocal, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replacedLocal, "documents"), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := b.digest()
	out, err := b.reconcile("--apply", "--take-hosted-all", "skills", "--take-hosted-all", "documents")
	if err == nil {
		t.Fatalf("a failing documents save must fail the reconcile:\n%s", out)
	}
	if b.digest() != before {
		t.Errorf("a reconcile whose documents save failed changed the tree, backups, base or recorded state:\n%s", out)
	}
	if b.read("skills/s.md") != "s from B\n" || b.read("documents/d.md") != "d from B\n" {
		t.Errorf("files changed although a save failed: s=%q d=%q", b.read("skills/s.md"), b.read("documents/d.md"))
	}
}

// AC5: after A applies its choices and pushes without --force, a second machine
// that reconciles ends with the same files, and every reconciled area is in step
// on both.
func TestReconcileTwoMachinesEndInStep(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a, b := newSnapMachine(t, ts.URL), newSnapMachine(t, ts.URL)
	for _, rel := range []string{"skills/x.md", "skills/aonly.md", "workflows/w.md", "documents/d.md"} {
		a.write(rel, "base\n")
	}
	a.pushConfig()
	a.pushDocs()
	b.deploy()
	b.pullDocs()

	// A is the machine that moved on; B, the other, edited too.
	a.write("skills/x.md", "x from A\n")
	a.write("skills/aonly.md", "aonly from A\n")
	a.write("documents/d.md", "d from A\n")
	a.pushConfig()
	a.pushDocs()
	b.write("skills/x.md", "x from B\n")
	b.write("skills/bnew.md", "bnew\n")
	b.write("documents/d.md", "d from B\n")
	b.write("workflows/w.md", "w from B\n")

	// Plain pushes are refused, and B's reconcile takes its own side everywhere.
	if _, err := b.tryPushConfig(); err == nil {
		t.Fatal("test premise: B's push must be refused while diverged")
	}
	out := b.mustReconcile("--apply", "--take-local-all", "skills", "--take-local-all", "documents")
	for _, area := range []string{"skills", "documents"} {
		if !strings.Contains(out, area+": unpushed — next: satelle sync") {
			t.Errorf("%s: the state after a reconcile that kept local changes is not named:\n%s", area, out)
		}
	}
	b.pushConfig() // no --force
	b.pushDocs()

	out = a.mustReconcile("--apply") // no choices: take the plain pull
	for _, rel := range []string{"skills/x.md", "skills/aonly.md", "skills/bnew.md", "workflows/w.md", "documents/d.md"} {
		if a.read(rel) != b.read(rel) {
			t.Errorf("%s: A=%q B=%q", rel, a.read(rel), b.read(rel))
		}
	}
	if a.read("skills/x.md") != "x from B\n" {
		t.Errorf("A did not receive B's choice: %q\n%s", a.read("skills/x.md"), out)
	}
	for name, m := range map[string]*snapMachine{"A": a, "B": b} {
		statusOut, err := m.status()
		if err != nil {
			t.Fatalf("%s: not in step: %v\n%s", name, err, statusOut)
		}
		for _, area := range []string{"skills", "workflows", "documents"} {
			wantState(t, statusOut, area, "in step")
		}
	}
	if got, want := listTree(t, filepath.Join(a.repo, ".satelle", "skills")), listTree(t, filepath.Join(b.repo, ".satelle", "skills")); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("skills file sets differ: A=%v B=%v", got, want)
	}
}

// AC6: the settings and agents areas reconcile through the deploy's per-file
// handling: the local project binding survives, the hosted agents layer is taken
// as it was pushed, and this machine's agents.local.toml is left alone.
func TestReconcileSettingsAndAgentsKeepLocalValues(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	const toml = "[sync]\nagents = \"personal\"\nsettings = \"personal\"\n" + boundProjectToml
	mk := func() *snapMachine {
		m := &snapMachine{t: t, url: ts.URL, repo: syncConfigRepo(t, toml), state: filepath.Join(t.TempDir(), "document-sync-state.json")}
		_ = os.Remove(filepath.Join(m.repo, ".satelle", "agents.toml"))
		m.write("workflows/agents.toml", pushableAgents)
		return m
	}
	a, b := mk(), mk()
	b.write("workflows/agents.local.toml", localAgentsFixture)
	a.pushConfig()
	b.deploy()

	sonnet := strings.Replace(pushableAgents, `model   = "opus"`, `model   = "sonnet"`, 1)
	a.write("workflows/agents.toml", sonnet)
	a.write("satelle.toml", toml+"[backup]\nlocal_only = true\n")
	a.pushConfig()
	b.write("workflows/agents.toml", strings.Replace(pushableAgents, `model   = "opus"`, `model   = "haiku"`, 1))
	b.write("satelle.toml", toml+"# edited on B\n")

	out := b.mustReconcile("--apply", "--take-hosted-all", "agents", "--take-hosted-all", "settings")
	if agents := b.read("workflows/agents.toml"); agents != sonnet {
		t.Errorf("the hosted agents layer must be taken as pushed:\n%s\n--- output\n%s", agents, out)
	}
	if got := b.read("workflows/agents.local.toml"); got != localAgentsFixture {
		t.Errorf("reconcile touched agents.local.toml:\n%s", got)
	}
	settings := b.read("satelle.toml")
	if !strings.Contains(settings, "local_only = true") {
		t.Errorf("the hosted settings were not taken:\n%s", settings)
	}
	if !strings.Contains(settings, `project = "probe"`) {
		t.Errorf("the local project binding did not survive:\n%s", settings)
	}
	if !strings.Contains(out, `preserved as "probe"`) {
		t.Errorf("the binding preservation is not reported:\n%s", out)
	}
	// What the hosted side replaced on B is saved, as it was on disk.
	got, err := os.ReadFile(filepath.Join(b.backupsDir(), "sync-replaced", "local", "settings", "satelle.toml"))
	if err != nil || !strings.Contains(string(got), "# edited on B") {
		t.Errorf("B's replaced settings not saved: %q %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(b.backupsDir(), "sync-replaced", "local", "agents", "workflows", "agents.toml"))
	if err != nil || !strings.Contains(string(got), `model   = "haiku"`) {
		t.Errorf("B's replaced agents not saved with its on-disk bytes: %q %v", got, err)
	}
}

// A conflict an earlier pull parked is settled by a choice: the copy is gone and
// the path is no longer unmerged.
func TestReconcileSettlesAnEarlierConflict(t *testing.T) {
	_, b := reconcileFixture(t)
	b.deploy() // parks x.md and y.md
	if !b.has("backups/sync-conflicts/skills/x.md") {
		t.Fatal("test premise: the deploy must park a conflict copy")
	}
	b.mustReconcile("--apply", "--take-local", "skills/x.md", "--take-hosted", "skills/y.md")
	if b.has("backups/sync-conflicts/skills/x.md") || b.has("backups/sync-conflicts/skills/y.md") {
		t.Error("a settled conflict left its copy behind")
	}
	if b.read("skills/x.md") != "x from B\n" || b.read("skills/y.md") != "y from A\n" {
		t.Errorf("x=%q y=%q", b.read("skills/x.md"), b.read("skills/y.md"))
	}
	if got, err := os.ReadFile(filepath.Join(b.backupsDir(), "sync-replaced", "local", "skills", "skills", "y.md")); err != nil || string(got) != "y from B\n" {
		t.Errorf("the replaced local y.md was not saved: %q %v", got, err)
	}
	base, _ := b.base("skills")
	if len(base.Unmerged) != 0 {
		t.Errorf("still unmerged after settling: %v", base.Unmerged)
	}
}

// The diff of the unified-diff helper itself.
func TestPrintUnifiedDiff(t *testing.T) {
	var buf bytes.Buffer
	printUnifiedDiff(&buf, "old", "new", "a\nb\nc\nd\ne\nf\ng\nh\n", "a\nb\nC\nd\ne\nf\ng\nh\nz")
	got := buf.String()
	for _, want := range []string{"--- old\n+++ new\n", "@@ -1,8 +1,9 @@", " a\n", "-c\n+C\n", "+z\n\\ No newline at end of file\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	buf.Reset()
	printUnifiedDiff(&buf, "old", "new", "same\n", "same\n")
	if strings.Contains(buf.String(), "@@") {
		t.Errorf("equal inputs must have no hunk:\n%s", buf.String())
	}
	buf.Reset()
	printUnifiedDiff(&buf, "old", "new", "", "only\n")
	if !strings.Contains(buf.String(), "@@ -0,0 +1,1 @@") || !strings.Contains(buf.String(), "+only\n") {
		t.Errorf("an empty old side:\n%s", buf.String())
	}
}
