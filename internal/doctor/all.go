package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

// CheckAll checks every registered workspace repository INDEPENDENTLY.
//
// The guarantee that matters is isolation: one unreadable, uninitialised, or
// pathological repo must not hide the health of the rest. Each root is checked
// inside its own recover, and a root that cannot be checked at all becomes a
// one-finding report rather than aborting the sweep — a diagnostic that stops at
// the first bad repo is exactly the tool an operator with several repos cannot
// use.
//
// Results are returned in registry order so successive runs are comparable.
func CheckAll(ctx context.Context, o Opts) []Report {
	gc, err := config.LoadGlobal()
	if err != nil {
		return []Report{{
			Repo: config.GlobalConfigPath(),
			OK:   false,
			Findings: health.Findings{health.Error(health.IDRepoUnreadable, "Unreadable workspace registry",
				fmt.Sprintf("could not read %s: %v", config.GlobalConfigPath(), err)).
				WithRemediation("fix the global config, or re-run `satelle workspace add <repo>`")},
		}}
	}
	roots := append([]string(nil), gc.Workspace.Repos...)
	if len(roots) == 0 {
		return nil
	}
	sort.SliceStable(roots, func(i, j int) bool { return i < j }) // preserve registry order

	reports := make([]Report, 0, len(roots))
	for _, root := range roots {
		reports = append(reports, checkOne(ctx, o, root))
	}
	return reports
}

// checkOne runs Check for one root with failure containment.
func checkOne(ctx context.Context, o Opts, root string) (rep Report) {
	defer func() {
		if r := recover(); r != nil {
			rep = Report{
				Repo: root,
				OK:   false,
				Findings: health.Findings{health.Error(health.IDRepoUnreadable, "Repository could not be checked",
					fmt.Sprintf("checking %s panicked: %v", root, r)).
					WithRemediation("inspect the repo by hand, or remove it with `satelle workspace remove`")},
			}
		}
	}()
	dataDir := filepath.Join(root, config.DefaultDataDir)
	if st, err := os.Stat(dataDir); err != nil || !st.IsDir() {
		return Report{
			Repo: root,
			OK:   false,
			Findings: health.Findings{health.Error(health.IDRepoUnreadable, "Not a satelle repository",
				fmt.Sprintf("%s has no %s directory", root, config.DefaultDataDir)).
				WithRemediation("run `satelle init` there, or remove it with `satelle workspace remove`")},
		}
	}
	sub := o
	sub.RepoRoot = root
	sub.DataDir = dataDir
	return Check(ctx, sub)
}

// Summarise counts healthy and unhealthy reports.
func Summarise(reports []Report) (healthy, unhealthy int) {
	for _, r := range reports {
		if r.OK {
			healthy++
			continue
		}
		unhealthy++
	}
	return healthy, unhealthy
}
