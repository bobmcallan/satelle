package cli

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/doctor"
	"github.com/bobmcallan/satelle/internal/health"
)

// tierRepo is a bound repo with v1 of the team-owned files pushed and deployed
// as its project tier.
func tierRepo(t *testing.T) (repo, server string) {
	t.Helper()
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)
	repo = agentsPushRepo(t, "[sync]\n"+boundProjectToml, tierAgents("team-v1"))
	writeRepoFile(t, repo, ".satelle/skills/team-skill.md", tierSkill("v1"))
	tierPush(t, ts.URL)
	tierDeploy(t, ts.URL)
	return repo, ts.URL
}

func runtimeDirOf(t *testing.T, repo string) string {
	t.Helper()
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return cfg.ResolveRuntimeDir(repo).Dir
}

// sessionStart runs the context hook with no event field (it emits as
// SessionStart) and returns stdout and stderr.
func sessionStart(t *testing.T) (string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	if err := runHookContext(&out, &errb, "claude", []byte("{}")); err != nil {
		t.Fatalf("runHookContext must fail open, got %v", err)
	}
	return out.String(), errb.String()
}

func readRepoFile(t *testing.T, repo, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertTreeIsTier(t *testing.T, stage, repo, v string) {
	t.Helper()
	if got := readRepoFile(t, repo, ".satelle/skills/team-skill.md"); got != tierSkill(v) {
		t.Errorf("%s: team-skill.md = %q, want the tier's %s", stage, got, v)
	}
	if got := readRepoFile(t, repo, ".satelle/"+config.AgentsRel); got != tierAgents("team-"+v) {
		t.Errorf("%s: agents.toml = %q, want the tier's", stage, got)
	}
}

// AC1: session start replaces each differing team-owned file with the tier's
// copy, leaves a file the tier does not hold alone, and doctor then sees no
// tree copy that differs.
func TestWriteDownSessionStartReplacesDiffering(t *testing.T) {
	repo, _ := tierRepo(t)
	localTierEdits(t, repo)
	sessionStart(t)
	assertTreeIsTier(t, "session start", repo, "v1")
	if !strings.Contains(readRepoFile(t, repo, ".satelle/skills/repo-only.md"), "repo only") {
		t.Error("repo-only.md must be untouched")
	}
	proc, root, _, err := config.LoadRepoProcess(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range doctor.CheckProjectTier(proc, root) {
		if f.ID == health.IDTeamTreeDiffers {
			t.Errorf("doctor still reports a differing tree copy: %s", f.Detail)
		}
	}
}

// AC1: a team script is written executable; the init-owned hook stays the
// tree's.
func TestWriteDownSessionStartTeamScript(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)
	repo := agentsPushRepo(t, "[sync]\n[team]\npaths = [\"agents.toml\", \"skills/**\", \"scripts/*.sh\", \"hooks/*\"]\n"+boundProjectToml, tierAgents("team-v1"))
	writeRepoFile(t, repo, ".satelle/scripts/reviewer-token.sh", "#!/bin/sh\necho team-token\n")
	writeRepoFile(t, repo, ".satelle/hooks/satelle-hook.sh", "#!/bin/sh\necho init-hook\n")
	tierPush(t, ts.URL)
	tierDeploy(t, ts.URL)
	writeRepoFile(t, repo, ".satelle/scripts/reviewer-token.sh", "#!/bin/sh\necho drifted\n")
	writeRepoFile(t, repo, ".satelle/hooks/satelle-hook.sh", "#!/bin/sh\necho local-hook\n")
	sessionStart(t)

	script := filepath.Join(repo, ".satelle", "scripts", "reviewer-token.sh")
	if got := readRepoFile(t, repo, ".satelle/scripts/reviewer-token.sh"); got != "#!/bin/sh\necho team-token\n" {
		t.Errorf("script = %q, want the tier's", got)
	}
	if info, err := os.Stat(script); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("script must be executable: %v %v", info.Mode(), err)
	}
	if got := readRepoFile(t, repo, ".satelle/hooks/satelle-hook.sh"); got != "#!/bin/sh\necho local-hook\n" {
		t.Errorf("init-owned hook must be untouched, got %q", got)
	}
}

// AC2: bare sync writes the tier down before its push legs, so the config push
// carries the team version and not the local edit.
func TestWriteDownBareSyncReplacesDiffering(t *testing.T) {
	repo, server := tierRepo(t)
	writeRepoFile(t, repo, ".satelle/satelle.toml", "[sync]\n"+boundProjectToml)
	localTierEdits(t, repo)
	out, err := runRoot(t, "sync", "--server", server)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	assertTreeIsTier(t, "bare sync", repo, "v1")
	if !strings.Contains(out, "file(s) written from the project tier") {
		t.Errorf("sync must report the write-down:\n%s", out)
	}
	resp, err := http.Get(server + "/api/v1/projects/probe/config/skills/team-skill.md")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != tierSkill("v1") {
		t.Errorf("hosted team-skill.md = %q, want the team version", body)
	}
}

// AC2: with every area local, bare sync still writes the tier down and still
// says there is nothing to sync; --dry-run writes nothing.
func TestWriteDownBareSyncAllLocalAndDryRun(t *testing.T) {
	repo, _ := tierRepo(t)
	writeRepoFile(t, repo, ".satelle/satelle.toml", boundProjectToml)
	localTierEdits(t, repo)

	out, err := runRoot(t, "sync", "--dry-run")
	if err != nil {
		t.Fatalf("sync --dry-run: %v\n%s", err, out)
	}
	if got := readRepoFile(t, repo, ".satelle/skills/team-skill.md"); got != tierSkill("local-edit") {
		t.Errorf("--dry-run must write nothing, team-skill.md = %q", got)
	}
	if !strings.Contains(out, "would write") || !strings.Contains(out, "skills/team-skill.md") {
		t.Errorf("--dry-run must list what it would write:\n%s", out)
	}

	out, err = runRoot(t, "sync")
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	assertTreeIsTier(t, "all-local sync", repo, "v1")
	if !strings.Contains(out, "Nothing to sync") {
		t.Errorf("all-local sync output:\n%s", out)
	}
}

// AC3: a locally edited copy is backed up before it is replaced, and the
// session-start output and the bare-sync stderr name the file and the backup.
func TestWriteDownBacksUpLocalEdit(t *testing.T) {
	repo, _ := tierRepo(t)
	backup := filepath.Join(runtimeDirOf(t, repo), "backups", string(BackupKindTeamReplaced), "skills", "team-skill.md")

	localTierEdits(t, repo)
	out, _ := sessionStart(t)
	if got, err := os.ReadFile(backup); err != nil || string(got) != tierSkill("local-edit") {
		t.Fatalf("backup = %q (%v), want the local edit", got, err)
	}
	for _, want := range []string{".satelle/skills/team-skill.md", backup} {
		if !strings.Contains(out, want) {
			t.Errorf("session start must name %s:\n%s", want, out)
		}
	}

	writeRepoFile(t, repo, ".satelle/satelle.toml", boundProjectToml)
	writeRepoFile(t, repo, ".satelle/skills/team-skill.md", tierSkill("second-edit"))
	stdout, stderr, err := runRootSplit(t, "", "sync")
	if err != nil {
		t.Fatalf("sync: %v\n%s%s", err, stdout, stderr)
	}
	if got, _ := os.ReadFile(backup); string(got) != tierSkill("second-edit") {
		t.Errorf("backup after sync = %q, want the second edit", got)
	}
	for _, want := range []string{".satelle/skills/team-skill.md", backup} {
		if !strings.Contains(stderr, want) {
			t.Errorf("sync stderr must name %s:\n%s", want, stderr)
		}
	}
}

// AC3: a copy satelle wrote is replaced when only the tier moved on, with no
// backup and no warning.
func TestWriteDownStaleCopyNoBackup(t *testing.T) {
	repo, _ := tierRepo(t)
	rt := runtimeDirOf(t, repo)
	sessionStart(t)
	assertTreeIsTier(t, "first", repo, "v1")

	st, _ := config.ReadProjectTierStamp(config.ProjectTierDir(rt))
	if err := config.WriteProjectTier(rt, st, map[string][]byte{
		config.AgentsRel:       []byte(tierAgents("team-v2")),
		"skills/team-skill.md": []byte(tierSkill("v2")),
	}); err != nil {
		t.Fatal(err)
	}
	out, _ := sessionStart(t)
	assertTreeIsTier(t, "after the tier moved", repo, "v2")
	if _, err := os.Stat(filepath.Join(rt, "backups", string(BackupKindTeamReplaced))); !os.IsNotExist(err) {
		t.Errorf("no backup may be taken of a copy satelle wrote: %v", err)
	}
	if strings.Contains(out, "edited locally") {
		t.Errorf("no warning for a stale satelle-written copy:\n%s", out)
	}
}

// AC3: a file whose backup fails is left as it is, and the warning reports it.
func TestWriteDownBackupFailureKeepsFile(t *testing.T) {
	repo, _ := tierRepo(t)
	rt := runtimeDirOf(t, repo)
	if err := os.WriteFile(filepath.Join(rt, "backups"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	localTierEdits(t, repo)
	out, _ := sessionStart(t)
	if got := readRepoFile(t, repo, ".satelle/skills/team-skill.md"); got != tierSkill("local-edit") {
		t.Errorf("a file with no backup must not be replaced, got %q", got)
	}
	if !strings.Contains(out, "backup failed") || !strings.Contains(out, ".satelle/skills/team-skill.md") {
		t.Errorf("the warning must report the failed backup:\n%s", out)
	}
}

// AC4: the local override files are never written, even by a tier that holds
// them under a set that owns everything, and they still take precedence.
func TestWriteDownNeverWritesLocalOverrides(t *testing.T) {
	repo := agentsPushRepo(t, boundProjectToml, tierAgents("team-v1"))
	rt := runtimeDirOf(t, repo)
	if err := config.WriteProjectTier(rt, config.ProjectTierStamp{Project: "probe", TeamPaths: []string{"**"}}, map[string][]byte{
		config.AgentsRel:       []byte(tierAgents("team-v1")),
		config.LocalAgentsRel:  []byte("[reviewer]\nmodel = \"team-local\"\n"),
		config.LocalConfigName: []byte("[backup]\nlocal_only = false\n"),
	}); err != nil {
		t.Fatal(err)
	}
	localAgents := "[reviewer]\nmodel = \"mine\"\n"
	localCfg := "[team]\npaths = [\"**\"]\n\n[backup]\nlocal_only = true\n"
	writeRepoFile(t, repo, ".satelle/"+config.LocalAgentsRel, localAgents)
	writeRepoFile(t, repo, ".satelle/"+config.LocalConfigName, localCfg)
	paths := []string{filepath.Join(repo, ".satelle", filepath.FromSlash(config.LocalAgentsRel)), filepath.Join(repo, ".satelle", config.LocalConfigName)}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, p := range paths {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	out, _ := sessionStart(t)
	syncOut, err := runRoot(t, "sync")
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, syncOut)
	}
	for i, want := range []string{localAgents, localCfg} {
		b, err := os.ReadFile(paths[i])
		info, serr := os.Stat(paths[i])
		if err != nil || serr != nil || string(b) != want || !info.ModTime().Equal(old) {
			t.Errorf("%s was written: %q (%v)", paths[i], b, err)
		}
	}
	for _, s := range []string{out, syncOut} {
		if strings.Contains(s, config.LocalAgentsName) || strings.Contains(s, config.LocalConfigName) {
			t.Errorf("a local override file appears in the output:\n%s", s)
		}
	}
	if _, err := os.Stat(filepath.Join(rt, "backups", string(BackupKindTeamReplaced))); !os.IsNotExist(err) {
		t.Errorf("no local override may be backed up: %v", err)
	}
	eff, err := config.LoadEffectiveAgents(filepath.Join(repo, ".satelle"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if eff.Agents.Reviewer.Model != "mine" {
		t.Errorf("reviewer model = %q, want the local override", eff.Agents.Reviewer.Model)
	}
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Backup.LocalOnly {
		t.Error("satelle.local.toml's setting must still take precedence")
	}
}

// AC5: an unbound repo is untouched by session start and sync, even with a
// leftover tier; a tier naming another project writes nothing either.
func TestWriteDownUnboundRepoUntouched(t *testing.T) {
	clearHarnessEnv(t)
	repo := agentsPushRepo(t, "", tierAgents("team-v1"))
	rt := runtimeDirOf(t, repo)
	if err := config.WriteProjectTier(rt, config.ProjectTierStamp{Project: "other"}, map[string][]byte{
		config.AgentsRel:       []byte(tierAgents("team-v2")),
		"skills/team-skill.md": []byte(tierSkill("v2")),
	}); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, repo, ".satelle/skills/team-skill.md", tierSkill("local-edit"))
	before := snapshotTree(t, filepath.Join(repo, ".satelle"))

	out, _ := sessionStart(t)
	syncOut, err := runRoot(t, "sync")
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, syncOut)
	}
	if after := snapshotTree(t, filepath.Join(repo, ".satelle")); !equalSnapshots(before, after) {
		t.Errorf("an unbound repo's .satelle changed:\nbefore %v\nafter  %v", before, after)
	}
	if _, err := os.Stat(filepath.Join(rt, "backups", string(BackupKindTeamReplaced))); !os.IsNotExist(err) {
		t.Errorf("no backup may be taken: %v", err)
	}
	for _, s := range []string{out, syncOut} {
		if strings.Contains(s, "team substrate:") {
			t.Errorf("no team line in an unbound repo:\n%s", s)
		}
	}

	var process config.Config
	process.Hosted.Project = "probe"
	res, err := writeDownProjectTier(process, repo, rt, repo, process, false)
	if err != nil || len(res.Written) != 0 || len(res.Backups) != 0 || len(res.Errors) != 0 {
		t.Errorf("another project's tier must write nothing: %+v %v", res, err)
	}
}

func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		info, serr := d.Info()
		if rerr != nil || serr != nil {
			return nil
		}
		out[p] = teamFileSum(b) + "@" + info.ModTime().String()
		return nil
	})
	return out
}

func equalSnapshots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
