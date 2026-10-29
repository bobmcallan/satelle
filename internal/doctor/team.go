package doctor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

// CheckProjectTier reports, for a repo with a project tier (sty_8dedc0a4), what
// the tier changes about the tree: each team-owned path whose tree copy differs
// from the tier (the tier governs), each init-owned file a [team] glob names (it
// stays with init), and each team-owned script substrate still names by its
// tree path instead of `satelle team path`. A repo with no tier reports nothing.
func CheckProjectTier(process config.Config, processRoot string) health.Findings {
	tier, ok := config.LoadProjectTier(process, processRoot, "")
	if !ok {
		return nil
	}
	dataDir := process.ResolveDataDir(processRoot)
	var out health.Findings
	var scripts []string
	for _, rel := range tier.Stamp.Files {
		if !tier.Team.Owns(rel) {
			continue
		}
		if !strings.HasSuffix(rel, ".md") && !strings.HasSuffix(rel, ".toml") {
			scripts = append(scripts, rel)
		}
		local, lerr := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
		if lerr != nil {
			continue
		}
		if deployed, derr := os.ReadFile(tier.Path(rel)); derr == nil && !bytes.Equal(local, deployed) {
			out = append(out, health.Warn(health.IDTeamTreeDiffers, "Tree copy of a team-owned path does not govern",
				fmt.Sprintf("%s/%s differs from the project tier's copy (%s), which governs", config.DefaultDataDir, rel, tier.Path(rel))).
				About(config.DefaultDataDir+"/"+rel).
				WithRemediation("to make the tree's edit the team version: `satelle sync config push && satelle sync config deploy`; to keep a local agents or settings edit, move it into agents.local.toml or satelle.local.toml"))
		}
	}
	for _, rel := range tier.Team.InitOwnedMatched() {
		out = append(out, health.Info(health.IDTeamInitOwned, "Init-owned file excluded from the team set",
			fmt.Sprintf("a [team] paths glob names %s/%s, which `satelle init` writes; it resolves from the tree", config.DefaultDataDir, rel)).
			About(config.DefaultDataDir+"/"+rel))
	}
	out = append(out, literalScriptRefs(config.ResolveAuthoredRoots(process, processRoot, ""), scripts)...)
	return out
}

// literalScriptRefs finds substrate (skills, workflows, principles, in the tree
// and the tier) that names a team-owned script by its .satelle/ tree path.
func literalScriptRefs(roots config.AuthoredRoots, scripts []string) health.Findings {
	if len(scripts) == 0 {
		return nil
	}
	idx := roots.Index()
	var out health.Findings
	for _, kind := range []string{"skills", "workflows", "principles"} {
		for _, dir := range []string{roots.Dirs[kind], idx.TierDir(kind)} {
			if dir == "" {
				continue
			}
			_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				body, rerr := os.ReadFile(p)
				if rerr != nil {
					return nil
				}
				for _, rel := range scripts {
					if bytes.Contains(body, []byte(config.DefaultDataDir+"/"+rel)) {
						out = append(out, health.Warn(health.IDTeamLiteralScript, "Team script named by its tree path",
							fmt.Sprintf("%s names %s/%s, which resolves from the project tier", p, config.DefaultDataDir, rel)).
							About(p).
							WithRemediation(fmt.Sprintf("reference it as \"$(satelle team path %s)\"", rel)))
					}
				}
				return nil
			})
		}
	}
	return out
}
