package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/substrate"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Tests for sty_097833e8: only substrate the operator edited or authored is
// versioned and synced; an unedited embedded default always comes from the
// installed binary.

const uneditedSyncToml = "[sync]\nskills = \"personal\"\nprinciples = \"personal\"\ntasks = \"personal\"\nworkflows = \"personal\"\n" + boundProjectToml

func embeddedBodyOf(t *testing.T, kind, name string) string {
	t.Helper()
	body, ok := embeddedDefault(kind, name)
	if !ok {
		t.Fatalf("no embedded default %s/%s", kind, name)
	}
	return body
}

func stampedDefault(body string) string { return applyEmbeddedStamp(body, embeddedSHA(body)) }

// writeUneditedFixture lays an unedited seed (current stamped), an older-stamped
// seed, a stampless identical seed, an unedited task, an edited seed, an edited
// task and an authored skill under repo/.satelle.
func writeUneditedFixture(t *testing.T, repo string) {
	t.Helper()
	step := embeddedBodyOf(t, "skills", "satelle-step-summary")
	goals := embeddedBodyOf(t, "principles", "satelle-agent-goals")
	adv := embeddedBodyOf(t, "skills", "satelle-workflow-advisor")
	audit := embeddedBodyOf(t, "tasks", "tsk_substrate-audit")
	ctx := embeddedBodyOf(t, "tasks", "tsk_context-audit")
	writeRepoFile(t, repo, ".satelle/skills/satelle-step-summary.md", stampedDefault(step))
	writeRepoFile(t, repo, ".satelle/principles/satelle-agent-goals.md", stampedDefault(goals+"\nan older default\n"))
	writeRepoFile(t, repo, ".satelle/skills/satelle-estimate-actual-review.md", embeddedBodyOf(t, "skills", "satelle-estimate-actual-review"))
	writeRepoFile(t, repo, ".satelle/tasks/tsk_substrate-audit.md", stampedDefault(audit))
	writeRepoFile(t, repo, ".satelle/skills/satelle-workflow-advisor.md", applyEmbeddedStamp(adv+"\noperator edit\n", embeddedSHA(adv)))
	writeRepoFile(t, repo, ".satelle/tasks/tsk_context-audit.md", applyEmbeddedStamp(ctx+"\noperator edit\n", embeddedSHA(ctx)))
	writeRepoFile(t, repo, ".satelle/skills/my-own-skill.md", "---\nname: my-own-skill\ntype: skill\n---\nauthored\n")
	writeRepoFile(t, repo, ".satelle/skills/secret.local.md", "SECRET\n")
}

var (
	fixtureSeedPaths = []string{
		"principles/satelle-agent-goals.md",
		"skills/satelle-estimate-actual-review.md",
		"skills/satelle-step-summary.md",
		"tasks/tsk_substrate-audit.md",
	}
	fixtureKeptPaths = []string{
		"skills/my-own-skill.md",
		"skills/satelle-workflow-advisor.md",
		"tasks/tsk_context-audit.md",
	}
)

// linesWith returns the output lines containing sub.
func linesWith(out, sub string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sub) {
			got = append(got, strings.TrimSpace(l))
		}
	}
	return got
}

// withheldUnder returns the paths printed under one withheld label.
func withheldUnder(out, label string) []string {
	var paths []string
	prefix := "withheld (" + label + "): "
	for _, l := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), prefix); ok {
			paths = append(paths, rest)
		}
	}
	sort.Strings(paths)
	return paths
}

const (
	labelDefault = "unedited embedded default — comes from the binary"
	labelLocal   = "never syncs, .local"
)

// TestSyncConfigPushSendsNoUneditedDefault (AC1, AC2): a push through the hosted
// fake sends the edited and authored files, none of the unedited defaults, and
// names each exclusion under its own label.
func TestSyncConfigPushSendsNoUneditedDefault(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	repo := syncConfigRepo(t, uneditedSyncToml)
	writeUneditedFixture(t, repo)
	pointAt(t, repo)

	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}
	out := buf.String()

	sent := map[string]bool{}
	for _, p := range f.putLog() {
		if path, ok := strings.CutPrefix(p, "config:"); ok {
			sent[path] = true
		}
	}
	for _, p := range fixtureKeptPaths {
		if !sent[p] {
			t.Errorf("%s (edited/authored) was not pushed; sent %v", p, sent)
		}
	}
	for _, p := range fixtureSeedPaths {
		if sent[p] {
			t.Errorf("%s is an unedited embedded default and was pushed", p)
		}
	}
	if sent["skills/secret.local.md"] {
		t.Error(".local file was pushed")
	}
	for p := range sent {
		if strings.HasPrefix(p, "backups/") {
			continue // snapshot records
		}
		if raw, _, ok := f.cfg.get("probe", p, 0); ok {
			disk, _ := os.ReadFile(filepath.Join(repo, ".satelle", filepath.FromSlash(p)))
			if string(raw) != string(disk) {
				t.Errorf("%s was altered on the way out", p)
			}
		}
	}

	if got := withheldUnder(out, labelDefault); !reflect.DeepEqual(got, fixtureSeedPaths) {
		t.Errorf("unedited-default list = %v, want %v\n%s", got, fixtureSeedPaths, out)
	}
	if got := withheldUnder(out, labelLocal); !reflect.DeepEqual(got, []string{"skills/secret.local.md"}) {
		t.Errorf(".local list = %v\n%s", got, out)
	}
	// Each list is its own: no seed under the .local label, no .local file under the
	// default label.
	for _, l := range linesWith(out, labelLocal) {
		for _, p := range fixtureSeedPaths {
			if strings.Contains(l, p) {
				t.Errorf("seed %s listed under the .local label: %s", p, l)
			}
		}
	}
}

// TestSyncConfigPushDryRunNamesUneditedDefaults (AC2): the dry run lists the
// defaults under their own label and never as push candidates.
func TestSyncConfigPushDryRunNamesUneditedDefaults(t *testing.T) {
	repo := syncConfigRepo(t, uneditedSyncToml)
	writeUneditedFixture(t, repo)
	pointAt(t, repo)
	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, "https://example.invalid", "", true); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, buf.String())
	}
	out := buf.String()
	if got := withheldUnder(out, labelDefault); !reflect.DeepEqual(got, fixtureSeedPaths) {
		t.Errorf("unedited-default list = %v, want %v\n%s", got, fixtureSeedPaths, out)
	}
	for _, p := range fixtureSeedPaths {
		if strings.Contains(out, p+" ") && strings.Contains(out, "-> personal") {
			for _, l := range linesWith(out, p) {
				if strings.Contains(l, "-> personal") {
					t.Errorf("%s offered as a push candidate: %s", p, l)
				}
			}
		}
	}
	if !strings.Contains(out, "Would push 3 config file(s)") {
		t.Errorf("dry run should count only the edited and authored files:\n%s", out)
	}
}

// hostedOlderPrinciple puts an older-stamped satelle-agent-goals on the hosted
// copy — what a machine on an older binary pushed — plus an authored principle,
// and (when snapshot) a snapshot record naming both.
func hostedOlderPrinciple(t *testing.T, f *snapFake, snapshot bool) {
	t.Helper()
	goals := embeddedBodyOf(t, "principles", "satelle-agent-goals")
	older := stampedDefault(goals + "\nan older default\n")
	f.cfg.put("probe", "principles/satelle-agent-goals.md", []byte(older))
	f.cfg.put("probe", "principles/team-rule.md", []byte("team rule\n"))
	if !snapshot {
		return
	}
	rec, err := snapsync.NewRecord(0, "older-machine", map[string]string{
		"principles/satelle-agent-goals.md": snapsync.SHA([]byte(older)),
		"principles/team-rule.md":           snapsync.SHA([]byte("team rule\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.put("probe", snapsync.RecordPath("principles"), rec.Encode())
}

// effectiveProvenance is `substrate list --effective` for one principle over the
// machine's tree.
func effectiveProvenance(t *testing.T, m *snapMachine, name string) string {
	t.Helper()
	m.use()
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := os.MkdirAll(m.path("principles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"principles": m.path("principles")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, err := substrate.List(context.Background(), db.DocIndex)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Kind == "principles" && r.Name == name {
			return r.Provenance
		}
	}
	return "<absent>"
}

func newPrincipleMachine(t *testing.T, url string) *snapMachine {
	t.Helper()
	m := newSnapMachine(t, url)
	// A fresh clone on the new binary: no principle files, principles synced.
	m.repo = syncConfigRepo(t, "[sync]\nprinciples = \"personal\"\n"+boundProjectToml)
	return m
}

// TestPullKeepsTheBinaryDefault (AC3): the hosted snapshot holds an older-stamped
// satelle-agent-goals and this tree has none. After a pull no file exists, an
// authored principle still arrives, and the effective item is the binary's own
// default.
func TestPullKeepsTheBinaryDefault(t *testing.T) {
	for _, snapshot := range []bool{true, false} {
		name := "snapshot"
		if !snapshot {
			name = "every head"
		}
		t.Run(name, func(t *testing.T) {
			ts, f := newSnapServer(t)
			seedCred(t, ts.URL)
			hostedOlderPrinciple(t, f, snapshot)
			m := newPrincipleMachine(t, ts.URL)

			out := m.deploy()

			if m.has("principles/satelle-agent-goals.md") {
				t.Errorf("a hosted unedited default was written over the tree:\n%s", out)
			}
			if got := m.read("principles/team-rule.md"); got != "team rule\n" {
				t.Errorf("an authored principle did not arrive: %q\n%s", got, out)
			}
			if len(listCopies(t, m.path(""))) != 0 {
				t.Errorf("a conflict copy was parked: %v", listCopies(t, m.path("")))
			}
			if !strings.Contains(out, "kept binary default") {
				t.Errorf("the skipped default is not reported:\n%s", out)
			}
			if got := effectiveProvenance(t, m, "satelle-agent-goals"); got != "default" {
				t.Errorf("effective provenance = %q, want default", got)
			}
		})
	}
}

// listCopies is every parked conflict copy under the data dir.
func listCopies(t *testing.T, dataDir string) []string {
	t.Helper()
	var out []string
	for _, p := range listTree(t, dataDir) {
		if strings.HasPrefix(p, "backups/sync-conflicts") || strings.Contains(p, "conflict") {
			out = append(out, p)
		}
	}
	return out
}

// TestPullDoesNotParkAnEditedLocalAgainstAHostedDefault (AC3): a local edit of a
// seed, with the hosted copy an older unedited default, stays — no conflict.
func TestPullDoesNotParkAnEditedLocalAgainstAHostedDefault(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	hostedOlderPrinciple(t, f, true)
	m := newPrincipleMachine(t, ts.URL)
	goals := embeddedBodyOf(t, "principles", "satelle-agent-goals")
	edited := goals + "\nmy edit\n"
	m.write("principles/satelle-agent-goals.md", edited)

	out := m.deploy()
	if got := m.read("principles/satelle-agent-goals.md"); got != edited {
		t.Errorf("the local edit was replaced:\n%s", out)
	}
	if strings.Contains(out, "conflict (changed on both sides)") {
		t.Errorf("a hosted unedited default was parked as a conflict:\n%s", out)
	}
	if b, ok := m.base("principles"); ok && len(b.Unmerged) != 0 {
		t.Errorf("an unmerged marker was left: %v", b.Unmerged)
	}
}

// TestStatusInStepWithAnUneditedSeedHostedLacks (AC6): at the CLI, the machine
// holds an unedited seed the hosted copy does not carry, and the area reports in
// step.
func TestStatusInStepWithAnUneditedSeedHostedLacks(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	m := newSnapMachine(t, ts.URL)
	m.write("skills/mine.md", "authored\n")
	m.pushConfig()
	m.write("skills/satelle-step-summary.md", stampedDefault(embeddedBodyOf(t, "skills", "satelle-step-summary")))

	m.use()
	cfg, repoRoot, _, err := loadRepoConfig()
	if err != nil {
		t.Fatal(err)
	}
	local, err := snapsync.LocalShas(cfg, repoRoot, "skills")
	if err != nil {
		t.Fatal(err)
	}
	if _, has := local["skills/satelle-step-summary.md"]; has {
		t.Fatalf("an unedited seed is in the area's pushed view: %v", local)
	}
	out := m.pushConfig()
	if strings.Contains(out, "skills: snapshot 2") {
		t.Errorf("an unedited seed made the area look unpushed:\n%s", out)
	}
}

// TestPruneAndMigrateShareOneSeedEnumeration (AC4): an unedited task copy is in
// prune's dry run and removed by --yes; an edited or authored task is never
// touched; migrate's prune list is prune's.
func TestPruneAndMigrateShareOneSeedEnumeration(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	writeUneditedFixture(t, repo)
	writeRepoFile(t, repo, ".satelle/tasks/tsk_mine.md", "---\nid: tsk_mine\ntype: task\n---\n# mine\n")

	var out strings.Builder
	if err := runSubstratePrune(context.Background(), &out, strings.NewReader(""), dataDir, BackupOpts{LocalOnly: true}, false, "", ""); err != nil {
		t.Fatal(err)
	}
	listed := listedPaths(out.String())
	if !reflect.DeepEqual(listed, fixtureSeedPaths) {
		t.Errorf("prune dry run lists %v, want %v\n%s", listed, fixtureSeedPaths, out.String())
	}
	if !fileExists(filepath.Join(dataDir, "tasks", "tsk_substrate-audit.md")) {
		t.Fatal("dry run removed a file")
	}

	plan := planMigrate(config.Config{}, repo, dataDir)
	if !reflect.DeepEqual(plan.PruneSeeds, listed) {
		t.Errorf("migrate prune list %v != prune list %v", plan.PruneSeeds, listed)
	}

	out.Reset()
	if err := runSubstratePrune(context.Background(), &out, strings.NewReader(""), dataDir, BackupOpts{LocalOnly: true, BackupsDir: t.TempDir()}, true, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, rel := range fixtureSeedPaths {
		if fileExists(filepath.Join(dataDir, filepath.FromSlash(rel))) {
			t.Errorf("prune --yes left unedited %s", rel)
		}
	}
	for _, rel := range append(append([]string{}, fixtureKeptPaths...), "tasks/tsk_mine.md") {
		if !fileExists(filepath.Join(dataDir, filepath.FromSlash(rel))) {
			t.Errorf("prune --yes removed edited/authored %s", rel)
		}
	}
}

// listedPaths reads the "  - path" lines of a prune plan.
func listedPaths(out string) []string {
	var paths []string
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(l, "  - "); ok {
			paths = append(paths, strings.TrimSpace(p))
		}
	}
	sort.Strings(paths)
	return paths
}

// TestInitDoesNotSeedEmbeddedTasks (AC5): init writes no tsk_*.md for the embedded
// tasks, and they still resolve from the store with no file on disk.
func TestInitDoesNotSeedEmbeddedTasks(t *testing.T) {
	withVerbWiring(t)
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	tasksDir := filepath.Join(repo, ".satelle", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tsk_") {
			t.Errorf("init wrote %s — default tasks are virtual", e.Name())
		}
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	verb.SetTaskDir(tasksDir)
	t.Cleanup(func() { verb.SetTaskDir("") })
	if _, _, _, err := verb.SyncTasks(context.Background(), db.Stories, time.Now()); err != nil {
		t.Fatalf("SyncTasks: %v", err)
	}
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "tasks" {
			continue
		}
		it, err := db.Stories.Get(context.Background(), d.Name)
		if err != nil || it.Kind != workitem.KindTask {
			t.Errorf("embedded task %s does not resolve from the store: %v %+v", d.Name, err, it)
		}
	}
	if entries, _ := os.ReadDir(tasksDir); len(filterTaskFiles(entries)) != 0 {
		t.Error("resolving the embedded tasks wrote a file")
	}
}

func filterTaskFiles(entries []os.DirEntry) []string {
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tsk_") {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestInitConvergesUneditedTaskCopy (AC5): a tsk_*.md stamped against an older
// embedded body is rewritten to the current body with a matching stamp; one the
// operator edited is left byte for byte.
func TestInitConvergesUneditedTaskCopy(t *testing.T) {
	repo := t.TempDir()
	audit := embeddedBodyOf(t, "tasks", "tsk_substrate-audit")
	older := audit + "\nan older default line\n"
	unedited := ".satelle/tasks/tsk_substrate-audit.md"
	writeRepoFile(t, repo, unedited, stampedDefault(older))
	ctxBody := embeddedBodyOf(t, "tasks", "tsk_context-audit")
	editedCopy := applyEmbeddedStamp(ctxBody+"\noperator edit\n", embeddedSHA(ctxBody))
	edited := ".satelle/tasks/tsk_context-audit.md"
	writeRepoFile(t, repo, edited, editedCopy)

	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(repo, unedited))
	if err != nil {
		t.Fatalf("the unedited task copy is gone: %v", err)
	}
	stripped, stamp, stamped := stripEmbeddedStamp(string(got))
	if !stamped || stripped != audit {
		t.Errorf("the unedited task copy was not converged to the current embedded body:\n%s", got)
	}
	if stamp != embeddedSHA(audit) || !isUneditedSeed(string(got), audit) {
		t.Errorf("converged copy carries stamp %q, want %q", stamp, embeddedSHA(audit))
	}
	if b, _ := os.ReadFile(filepath.Join(repo, edited)); string(b) != editedCopy {
		t.Errorf("an operator-edited task copy was rewritten:\n%s", b)
	}
	if fileExists(filepath.Join(repo, ".satelle/tasks/tsk_reviewer-objective-audit.md")) {
		t.Error("converging created a task file that was absent")
	}
}

// TestUpgradeConvergeIsNotASubstrateChange (AC8): init converging an unedited seed
// from embedded body A to body B leaves the bundle a push would send, and the sha
// view status reads, exactly as they were.
func TestUpgradeConvergeIsNotASubstrateChange(t *testing.T) {
	repo := syncConfigRepo(t, uneditedSyncToml)
	writeUneditedFixture(t, repo)
	pointAt(t, repo)
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		t.Fatal(err)
	}
	bundleBefore, err := config.ConfigFiles(cfg, repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	shasBefore, err := snapsync.LocalShas(cfg, repoRoot, "principles")
	if err != nil {
		t.Fatal(err)
	}

	lines := convergeOnDiskDefaults(dataDir, BackupOpts{LocalOnly: true, BackupsDir: t.TempDir()})
	goalsPath := filepath.Join(dataDir, "principles", "satelle-agent-goals.md")
	on, _ := os.ReadFile(goalsPath)
	goals := embeddedBodyOf(t, "principles", "satelle-agent-goals")
	if stripped, _, _ := stripEmbeddedStamp(string(on)); stripped != goals {
		t.Fatalf("test premise: the older seed was not converged (%v):\n%s", lines, on)
	}

	bundleAfter, err := config.ConfigFiles(cfg, repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	shasAfter, err := snapsync.LocalShas(cfg, repoRoot, "principles")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bundleBefore, bundleAfter) {
		t.Errorf("converging a seed changed the push bundle:\nbefore %v\nafter  %v", bundleBefore.UneditedDefaults, bundleAfter.UneditedDefaults)
	}
	if !reflect.DeepEqual(shasBefore, shasAfter) {
		t.Errorf("converging a seed changed LocalShas: %v -> %v", shasBefore, shasAfter)
	}
}
