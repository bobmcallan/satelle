package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

// `satelle sync` is the read-only foundation for epic:scoped-sync
// (sty_2ff2232d): a config-only [sync] scope resolver (internal/config/sync.go)
// with no network and no git side-effects. `sync scopes` is the resolver's
// production caller — it prints each area's resolved local|personal|shared
// scope and, for a personal area, which files are frontmatter-flagged shared —
// so an operator can verify resolution before any sync engine lands.
func init() {
	syncCmd := &cobra.Command{Use: "sync", Short: "Inspect the [sync] scope config (foundation only — no network, no git side-effects)"}
	syncCmd.AddCommand(&cobra.Command{
		Use:         "scopes",
		Short:       "Print each .satelle area's resolved scope, and shared files within a personal area",
		Annotations: needsStore(),
		RunE:        runSyncScopes,
	})
	register(syncCmd)
}

func runSyncScopes(cmd *cobra.Command, args []string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, area := range config.SyncAreas {
		scope, err := config.ScopeFor(a.Config, area)
		if err != nil {
			return fmt.Errorf("sync area %q: %w", area, err)
		}
		fmt.Fprintf(w, "%s\t%s\n", area, scope)
		if scope != config.PersonalScope {
			continue
		}
		path, isDir := syncAreaPath(a, area)
		if path == "" {
			continue
		}
		shared, err := sharedFilesIn(path, isDir)
		if err != nil {
			return fmt.Errorf("sync area %q: %w", area, err)
		}
		for _, f := range shared {
			fmt.Fprintf(w, "\tshared: %s\n", f)
		}
	}
	return w.Flush()
}

// syncAreaPath resolves the on-disk location a sync area's files live under.
// isDir reports whether path is a directory to scan (vs. a single file).
// "ledger" is DB-only and "executions" nest under their parent task's
// directory — neither has an area-level location of its own, so both resolve
// to "" (no file scan, scope still printed).
func syncAreaPath(a *app.App, area string) (path string, isDir bool) {
	dataDir := filepath.Dir(a.DBPath)
	switch area {
	case "constitution":
		return a.Config.ResolveConstitution(a.RepoRoot), false
	case "agents":
		return filepath.Join(dataDir, config.AgentsConfigName), false
	case "tasks":
		return filepath.Join(dataDir, "tasks"), true
	case "stories":
		return filepath.Join(dataDir, "stories"), true
	case "ledger", "executions":
		return "", false
	default:
		if dir := a.Config.ResolveAuthoredDirs(a.RepoRoot)[area]; dir != "" {
			return dir, true
		}
		return "", false
	}
}

// sharedFilesIn returns the repo-relative paths of every markdown file under
// path (or path itself, if it is a single file) whose frontmatter marks it
// shared. A non-existent path is benign (empty result, not an error) — an
// area with nothing on disk yet has nothing to report.
func sharedFilesIn(path string, isDir bool) ([]string, error) {
	if !isDir {
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if config.FileShared(config.PersonalScope, string(body)) {
			return []string{path}, nil
		}
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == path {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		if name == "index" || name == "log" || strings.EqualFold(name, "README") {
			return nil
		}
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if config.FileShared(config.PersonalScope, string(body)) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
