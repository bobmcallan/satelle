package cli

// The substrate holder gate on the config push (sty_40c41d91). In a project
// whose substrate is hosted, only its one holder publishes team-owned files; a
// member's copy stays on the member's machine. The holder is the server's
// (GET /api/v1/projects/{project}); the team-owned set is the tier's.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// holderRefusal is the team-owned files a config push kept local because the
// caller is not the project's substrate holder, and who may publish them.
type holderRefusal struct {
	Paths  []string
	Holder hosted.SubstrateHolder
	// LookupErr is why the holder could not be read when the gate failed closed.
	LookupErr error
}

// add records a refused path, taking the holder the refusal named.
func (r *holderRefusal) add(path string, holder hosted.SubstrateHolder) {
	r.Paths = append(r.Paths, path)
	if holder != (hosted.SubstrateHolder{}) {
		r.Holder = holder
	}
}

func (r holderRefusal) refused() bool { return len(r.Paths) > 0 }

// message names the withheld files and the holder, once.
func (r holderRefusal) message() string {
	seen := map[string]bool{}
	var paths []string
	for _, p := range r.Paths {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	who := r.Holder.Email
	if who == "" {
		who = r.Holder.UserID
	}
	switch {
	case who == "" && r.LookupErr != nil:
		who = fmt.Sprintf("unknown (project lookup failed: %v)", r.LookupErr)
	case who == "":
		who = "unknown"
	}
	return fmt.Sprintf("team substrate not published: %s — only the project's substrate holder %s can publish it; your copy stays local", strings.Join(paths, ", "), who)
}

// holderGate is the files a config push may send. A caller who is not the
// substrate holder of a hosted-substrate project sends no team-owned file whose
// bytes the project does not already hold: one the project holds goes with the
// project's bytes, so the area snapshot keeps the project's version and records
// no delete, and one it does not hold is dropped. headSHA is the manifest's
// heads, empty when the manifest could not be read. A server that does not
// report a substrate gates nothing; one whose project lookup fails gates as if
// the caller were a member when the repo has a project tier.
func holderGate(ctx context.Context, client *hosted.Client, cfg config.Config, repoRoot, server, project string, files []config.ConfigFile, headSHA map[string]string) ([]config.ConfigFile, holderRefusal, error) {
	var ref holderRefusal
	tier, hasTier := projectTierFor(repoRoot)
	p, err := client.GetProject(ctx, project)
	switch {
	case errors.Is(err, hosted.ErrLoginRequired):
		return nil, ref, err
	case errors.Is(err, hosted.ErrProjectNotFound):
		return files, ref, nil
	case err != nil:
		if !hasTier {
			return files, ref, nil
		}
		ref.LookupErr = err
	case p.Substrate == nil || p.Substrate.Source != hosted.SubstrateHosted:
		return files, ref, nil
	default:
		if p.Substrate.Holder != nil {
			ref.Holder = *p.Substrate.Holder
		}
		if id := resolveUserFor(server, repoRoot).PrincipalID; id != "" && id == ref.Holder.UserID {
			return files, ref, nil
		}
	}

	team := config.NewTeamSet(cfg.Team.Paths)
	if hasTier {
		team = tier.Team
	}
	send := make([]config.ConfigFile, 0, len(files))
	for _, f := range files {
		if !team.Owns(f.Path) || contentMatchesSHA(headSHA[f.Path], f.Content) {
			send = append(send, f)
			continue
		}
		ref.Paths = append(ref.Paths, f.Path)
		if headSHA[f.Path] == "" {
			continue
		}
		body, _, gerr := client.ConfigFileContent(ctx, project, f.Path, 0)
		if gerr != nil {
			return nil, ref, fmt.Errorf("read the project's %s: %w", f.Path, gerr)
		}
		f.Content = body
		send = append(send, f)
	}
	return send, ref, nil
}

// projectTierFor is the project tier of the repo at repoRoot, read through its
// process of record as the write-down reads it.
func projectTierFor(repoRoot string) (config.ProjectTier, bool) {
	proc, root, _, err := config.LoadRepoProcess(repoRoot)
	if err != nil {
		return config.ProjectTier{}, false
	}
	return config.LoadProjectTier(proc, root, "")
}
