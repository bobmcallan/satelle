package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

func tierAgents(model string) string {
	return "[executor]\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\nmodel = \"" + model + "\"\n"
}

func tierSkill(v string) string {
	return "---\ntype: skill\nname: team-skill\ndescription: team skill\n---\n# team skill " + v + "\n"
}

func tierPush(t *testing.T, server string) {
	t.Helper()
	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, server, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}
}

func tierDeploy(t *testing.T, server string) string {
	t.Helper()
	cmd, buf := testCmd()
	if err := runSyncConfigDeploy(cmd, server, "", 0); err != nil {
		t.Fatalf("deploy: %v\n%s", err, buf.String())
	}
	return buf.String()
}

// localTierEdits drifts the tree's copies of the team-owned files, adds a
// repo-only file and seat under the team set, and leaves a stale workspace
// agents layer: none of it may govern.
func localTierEdits(t *testing.T, repo string) {
	t.Helper()
	writeRepoFile(t, repo, ".satelle/skills/team-skill.md", tierSkill("local-edit"))
	writeRepoFile(t, repo, ".satelle/skills/repo-only.md", "---\ntype: skill\nname: repo-only\n---\n# repo only\n")
	writeRepoFile(t, repo, ".satelle/"+config.AgentsRel, tierAgents("local-edit")+"\n[repoonly]\nrole = \"agent\"\ncommand = \"in-loop\"\n")
	writeRepoFile(t, repo, ".satelle/"+config.WorkspaceAgentsRel, "[ghost]\nrole = \"agent\"\ncommand = \"in-loop\"\n")
}

// assertTierGoverns proves the deployed version v governs every surface that
// reads the team-owned set: the agents ladder, the document index, validate.
func assertTierGoverns(t *testing.T, stage, repo, v string) {
	t.Helper()
	dataDir := filepath.Join(repo, ".satelle")
	eff, err := config.LoadEffectiveAgents(dataDir, nil)
	if err != nil {
		t.Fatalf("%s: agents: %v", stage, err)
	}
	if eff.Agents.Reviewer.Model != "team-"+v {
		t.Errorf("%s: reviewer model = %q, want team-%s", stage, eff.Agents.Reviewer.Model, v)
	}
	for _, seat := range []string{"repoonly", "ghost"} {
		if _, ok := eff.Agents.Agents[seat]; ok {
			t.Errorf("%s: seat %q must not govern", stage, seat)
		}
	}

	a, err := app.Open()
	if err != nil {
		t.Fatalf("%s: open: %v", stage, err)
	}
	defer a.Close()
	ctx := context.Background()
	if _, err := a.Store.DocIndex.SyncRoots(ctx, a.AuthoredDirs().Index(), time.Now()); err != nil {
		t.Fatalf("%s: sync: %v", stage, err)
	}
	d, err := a.Store.DocIndex.Get(ctx, "skills", "team-skill")
	if err != nil {
		t.Fatalf("%s: team-skill: %v", stage, err)
	}
	if !strings.Contains(d.Body, "team skill "+v) || strings.HasPrefix(d.Path, repo) {
		t.Errorf("%s: team-skill indexed from %s:\n%s", stage, d.Path, d.Body)
	}
	if d, err := a.Store.DocIndex.Get(ctx, "skills", "repo-only"); err == nil {
		t.Errorf("%s: repo-only skill under the team set was indexed from %s", stage, d.Path)
	}

	cmd, buf := testCmd()
	_ = validateKind(cmd, a, "skills", "")
	if out := buf.String(); strings.Count(out, "skills/team-skill") != 1 || strings.Contains(out, "repo-only") {
		t.Errorf("%s: validate must check the tier copy once and never the repo's:\n%s", stage, out)
	}
}

// AC1: push v1, deploy, then drift the tree: the deployed v1 governs after
// deploy, a reindex, a re-sync and a validate; deploying v2 moves it to v2.
func TestProjectTierDeployedVersionGovernsTeamOwned(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)
	repo := agentsPushRepo(t, "[sync]\n"+boundProjectToml, tierAgents("team-v1"))
	writeRepoFile(t, repo, ".satelle/skills/team-skill.md", tierSkill("v1"))
	tierPush(t, ts.URL)
	if out := tierDeploy(t, ts.URL); !strings.Contains(out, "project tier:") {
		t.Fatalf("deploy must report the tier:\n%s", out)
	}
	skillPath := config.ResolveTeamPath(repo, "skills/team-skill.md")
	if strings.HasPrefix(skillPath, repo) {
		t.Fatalf("team skill must resolve outside the tree, got %s", skillPath)
	}

	localTierEdits(t, repo)
	assertTierGoverns(t, "after deploy", repo, "v1")

	if out, err := runRoot(t, "reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}
	assertTierGoverns(t, "after reindex", repo, "v1")

	tierDeploy(t, ts.URL)
	localTierEdits(t, repo)
	assertTierGoverns(t, "after re-sync", repo, "v1")

	writeRepoFile(t, repo, ".satelle/"+config.AgentsRel, tierAgents("team-v2"))
	writeRepoFile(t, repo, ".satelle/skills/team-skill.md", tierSkill("v2"))
	if err := os.Remove(filepath.Join(repo, ".satelle", "skills", "repo-only.md")); err != nil {
		t.Fatal(err)
	}
	tierPush(t, ts.URL)
	tierDeploy(t, ts.URL)
	localTierEdits(t, repo)
	assertTierGoverns(t, "after deploying v2", repo, "v2")
}

// AC4: a bound project whose deployed version holds no team-owned file gets no
// tier, and every path resolves from the tree as before.
func TestProjectTierNoneForStoreWithoutTeamFiles(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)
	// Every area syncs under the resolved scope, so the team set names only paths
	// this repo has no file under.
	repo := syncConfigRepo(t, "[sync]\n[team]\npaths = [\"hooks/*\"]\n"+boundProjectToml)
	writeRepoFile(t, repo, ".satelle/principles/rule.md", "team rule\n")
	writeRepoFile(t, repo, ".satelle/skills/s.md", "tree skill\n")
	pointAt(t, repo)
	tierPush(t, ts.URL)
	out := tierDeploy(t, ts.URL)
	if !strings.Contains(out, "holds no team-owned file") {
		t.Errorf("deploy output:\n%s", out)
	}
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.ProjectTierDir(cfg.ResolveRuntimeDir(repo).Dir)); !os.IsNotExist(err) {
		t.Fatalf("no tier dir may be created: %v", err)
	}
	if got, want := config.ResolveTeamPath(repo, "skills/s.md"), filepath.Join(repo, ".satelle", "skills", "s.md"); got != want {
		t.Errorf("ResolveTeamPath = %s, want %s", got, want)
	}
}

// bindingsTierRepo is a repo bound to both a project with a deployed tier that
// owns agents.toml and a team workspace.
func bindingsTierRepo(t *testing.T) string {
	t.Helper()
	repo := agentsPushRepo(t, "[hosted]\nproject = \"probe\"\nworkspace = \"Acme\"\n", pushableAgents)
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProjectTier(cfg.ResolveRuntimeDir(repo).Dir, config.ProjectTierStamp{Project: "probe"},
		map[string][]byte{config.AgentsRel: []byte(tierAgents("team-v1"))}); err != nil {
		t.Fatal(err)
	}
	return repo
}

// AC5: with agents.toml team-owned, no team-workspace bindings sync touches it:
// push refuses and pull skips, each with no hosted call.
func TestProjectTierBindingsSyncStandsDown(t *testing.T) {
	ts, f := newFakeBindingsServer(t)
	seedCred(t, ts.URL)
	f.stored = []byte("[ghost]\nrole = \"agent\"\ncommand = \"in-loop\"\n")
	f.version = 1
	repo := bindingsTierRepo(t)

	cmd, buf := testCmd()
	if err := runSyncBindingsPush(cmd, ts.URL, "", false); err == nil || !strings.Contains(err.Error(), "team-owned") {
		t.Fatalf("push must refuse: %v\n%s", err, buf.String())
	}
	cmd2, buf2 := testCmd()
	if err := runSyncBindingsPull(cmd2, ts.URL, ""); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if !strings.Contains(buf2.String(), "skipped") {
		t.Errorf("pull output = %q", buf2.String())
	}
	if _, err := os.Stat(config.WorkspaceAgentsPath(filepath.Join(repo, ".satelle"))); !os.IsNotExist(err) {
		t.Errorf("pull must write no workspace layer: %v", err)
	}
	if n := f.count(); n != 0 {
		t.Errorf("bindings sync made %d hosted call(s): %v", n, f.requests)
	}
}

// AC6: scripts and hooks named by [team] paths ride the project push, land in
// the tier executable, and `satelle team path` reaches the deployed bytes from
// a shell; the init-owned hook stays with the tree.
func TestProjectTierTeamScriptsAndHooks(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)
	repo := agentsPushRepo(t, "[sync]\n[team]\npaths = [\"agents.toml\", \"skills/**\", \"scripts/*.sh\", \"hooks/*\"]\n"+boundProjectToml, tierAgents("team-v1"))
	writeRepoFile(t, repo, ".satelle/scripts/reviewer-token.sh", "#!/bin/sh\necho team-token\n")
	writeRepoFile(t, repo, ".satelle/hooks/pr-review-post.sh", "#!/bin/sh\necho team-post\n")
	writeRepoFile(t, repo, ".satelle/hooks/satelle-hook.sh", "#!/bin/sh\necho init-hook\n")
	tierPush(t, ts.URL)
	tierDeploy(t, ts.URL)

	writeRepoFile(t, repo, ".satelle/scripts/reviewer-token.sh", "#!/bin/sh\necho drifted\n")
	writeRepoFile(t, repo, ".satelle/hooks/pr-review-post.sh", "#!/bin/sh\necho drifted\n")
	for rel, want := range map[string]string{"scripts/reviewer-token.sh": "team-token", "hooks/pr-review-post.sh": "team-post"} {
		out, err := runRoot(t, "team", "path", rel)
		if err != nil {
			t.Fatalf("team path %s: %v\n%s", rel, err, out)
		}
		p := strings.TrimSpace(out)
		if p != config.ResolveTeamPath(repo, rel) || strings.HasPrefix(p, repo) {
			t.Fatalf("team path %s = %q, want the tier copy", rel, p)
		}
		got, err := exec.Command("sh", "-c", fmt.Sprintf("%q", p)).CombinedOutput()
		if err != nil || strings.TrimSpace(string(got)) != want {
			t.Errorf("running %s = %q (%v), want the deployed %q", p, got, err, want)
		}
	}
	if got, want := config.ResolveTeamPath(repo, "hooks/satelle-hook.sh"), filepath.Join(repo, ".satelle", "hooks", "satelle-hook.sh"); got != want {
		t.Errorf("init-owned hook = %s, want the tree's %s", got, want)
	}
}
