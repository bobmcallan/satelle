package cli

// Seeding a project's substrate at `satelle project bind` (sty_6d43eb74). A
// project the server reports with no substrate is seeded by its holder — the
// holder's first publish, through the config push's own upload tail — from the
// repo's team-owned substrate, or from the embedded defaults the team-owned set
// owns when the repo has none. A project that already has substrate is not
// reseeded: the repo takes its project tier.

import (
	"errors"
	"fmt"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/spf13/cobra"
)

// seedProjectSubstrate is bind's step after the binding is recorded. The
// binding stands whatever happens here: only what the user must act on — a
// configuration the push checks refuse, a credential that no longer refreshes
// — fails the bind, and every other reason nothing was seeded is printed.
func seedProjectSubstrate(cmd *cobra.Command, slug string) error {
	out := cmd.OutOrStdout()
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	server := resolveServer("")
	if server == "" {
		fmt.Fprintln(out, "substrate: not seeded — no hosted server configured")
		return nil
	}
	if !resolveUserFor(server, repoRoot).SignedIn() {
		fmt.Fprintf(out, "substrate: not seeded — not signed in to %s (run \"satelle login\", then bind again)\n", server)
		return nil
	}
	p, err := newHostedClient(cmd.Context(), server, repoRoot).GetProject(cmd.Context(), slug)
	switch {
	case errors.Is(err, hosted.ErrProjectNotFound):
		fmt.Fprintln(out, "substrate: not seeded — the server does not report project substrate (404)")
		return nil
	case errors.Is(err, hosted.ErrLoginRequired):
		return err
	case err != nil:
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: substrate: not seeded — project lookup failed: %v\n", err)
		return nil
	}
	// A project the server reports without a substrate has none.
	source := hosted.SubstrateNone
	var holder *hosted.SubstrateHolder
	if p.Substrate != nil {
		source, holder = p.Substrate.Source, p.Substrate.Holder
	}
	switch source {
	case hosted.SubstrateHosted, hosted.SubstrateRepo:
		fmt.Fprintf(out, "substrate: project already has substrate (source %s); not seeded\n", source)
		return takeProjectTier(cmd)
	case hosted.SubstrateNone:
	default:
		fmt.Fprintf(out, "substrate: not seeded — unknown source %s\n", source)
		return nil
	}
	if !isSubstrateHolder(server, repoRoot, holder) {
		var who holderRefusal
		if holder != nil {
			who.Holder = *holder
		}
		fmt.Fprintf(out, "substrate: not seeded — only the project's substrate holder %s can seed it\n", who.holderName())
		return nil
	}

	ign, err := config.LoadIgnore(dataDir)
	if err != nil {
		return err
	}
	files, from, err := seedSet(cfg, repoRoot, ign)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(out, "substrate: not seeded — nothing team-owned to seed")
		return nil
	}
	ref, err := pushConfigFileSet(cmd, cfg, repoRoot, dataDir, ign, server, files, pushSetOpts{Verb: "project bind seed", IgnoreScope: true})
	if err != nil {
		var refused pushRefusal
		if errors.As(err, &refused) || errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: substrate: not seeded — %v\n", err)
		return nil
	}
	if ref.refused() {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+ref.message())
		fmt.Fprintln(out, "substrate: not seeded — the server refused the publish")
		return nil
	}
	fmt.Fprintf(out, "substrate: seeded project %s from %s: %d file(s)\n", slug, from, len(files))
	return nil
}

// seedSet is what a seed publishes, and where it came from: the repo's
// team-owned files, read whatever each area's [sync] scope, else the embedded
// defaults the team-owned set owns.
func seedSet(cfg config.Config, repoRoot string, ign config.Ignore) ([]config.ConfigFile, string, error) {
	team := config.NewTeamSet(cfg.Team.Paths)
	seen := map[string]bool{}
	var files []config.ConfigFile
	keep := func(fs []config.ConfigFile) {
		for _, f := range fs {
			if !seen[f.Path] && team.Owns(f.Path) {
				seen[f.Path] = true
				files = append(files, f)
			}
		}
	}
	for _, area := range config.ConfigAreas {
		fs, err := config.AreaFiles(cfg, repoRoot, area, ign)
		if err != nil {
			return nil, "", err
		}
		keep(fs)
	}
	extra, err := config.TeamExtraFiles(cfg, repoRoot, ign)
	if err != nil {
		return nil, "", err
	}
	keep(extra)
	if len(files) > 0 {
		return files, "repo", nil
	}
	keep(config.EmbeddedDefaultFiles())
	return files, "embedded defaults", nil
}

// takeProjectTier brings the repo to a project that already has substrate: the
// deploy of its hosted version, then the write-down of the team-owned files
// into the tree.
func takeProjectTier(cmd *cobra.Command) error {
	if _, err := runSyncConfigDeployOutcome(cmd, "", "", 0); err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: substrate: project tier not taken — %v\n", err)
		return nil
	}
	tree, repoRoot, _, err := loadRepoConfig()
	if err != nil {
		return err
	}
	process, processRoot, _, err := config.LoadRepoProcess(repoRoot)
	if err != nil {
		return err
	}
	return writeDownAndReport(cmd, process, processRoot, "", repoRoot, tree, false)
}
