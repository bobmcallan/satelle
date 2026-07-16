// `satelle runtime` — inspect and migrate the home-keyed runtime plane
// (sty_4660bbe1 / epic:substrate-planes). Runtime state (satelle.db, logs,
// backups, stories cache) lives under ~/.satelle/<repo-key>/; authored substrate
// stays under <repo>/.satelle. Migration is EXPLICIT — open never moves a DB.
package cli

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	_ "modernc.org/sqlite"
)

func init() {
	root := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect or migrate the home-keyed runtime plane (~/.satelle/<repo-key>/)",
		Long: `Runtime state (satelle.db, logs/, backups/, stories/) lives under
~/.satelle/<repo-key>/, keyed by a stable repo identity so worktrees share one
store. Authored substrate stays under <repo>/.satelle.

  satelle runtime path      print the resolved runtime dir and whether it is legacy
  satelle runtime migrate   copy a legacy in-repo DB + runtime dirs to the home key

Migration is never automatic: an unmigrated repo still opens from the legacy
path and prints a deprecation note. See decision-substrate-planes-local-first.`,
	}

	pathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print the resolved runtime directory for this repo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, cfgPath, err := config.Load("")
			if err != nil && err != config.ErrNotFound {
				return err
			}
			repoRoot := "."
			if cfgPath != "" {
				repoRoot = config.RepoRootFromConfigPath(cfgPath)
			} else if wd, e := os.Getwd(); e == nil {
				repoRoot = wd
			}
			return runRuntimePath(cmd.OutOrStdout(), cfg, repoRoot)
		},
	}

	var force, dryRun bool
	migrateCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Copy legacy in-repo runtime state to ~/.satelle/<repo-key>/",
		Long: `Copy satelle.db (via VACUUM INTO), logs/, backups/, and stories/ from the
legacy <repo>/.satelle/ layout into the home-keyed runtime dir. Leaves the
legacy tree in place and prints an rm suggestion — satelle never deletes the
operator's only ledger copy. Refuses if the target already has a database
unless --force is set.

Does not open the store — so the source DB is free for VACUUM INTO.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, cfgPath, err := config.Load("")
			if err != nil && err != config.ErrNotFound {
				return err
			}
			repoRoot := "."
			if cfgPath != "" {
				repoRoot = config.RepoRootFromConfigPath(cfgPath)
			} else if wd, e := os.Getwd(); e == nil {
				repoRoot = wd
			}
			a := &app.App{
				Config:     cfg,
				RepoRoot:   repoRoot,
				DataDir:    cfg.ResolveDataDir(repoRoot),
				RuntimeDir: cfg.ResolveRuntimeDir(repoRoot).Dir,
				DBPath:     cfg.ResolveDB(repoRoot),
			}
			return runRuntimeMigrate(cmd.OutOrStdout(), a, force, dryRun)
		},
	}
	migrateCmd.Flags().BoolVar(&force, "force", false, "overwrite an existing home-keyed database")
	migrateCmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be copied without writing")

	root.AddCommand(pathCmd, migrateCmd)
	register(root)
}

func runRuntimePath(out io.Writer, cfg config.Config, repoRoot string) error {
	res := cfg.ResolveRuntimeDir(repoRoot)
	key := config.RepoKey(repoRoot)
	fmt.Fprintf(out, "repo_root    %s\n", repoRoot)
	fmt.Fprintf(out, "repo_key     %s\n", key)
	fmt.Fprintf(out, "runtime_dir  %s\n", res.Dir)
	fmt.Fprintf(out, "database     %s\n", filepath.Join(res.Dir, config.DefaultDBName))
	fmt.Fprintf(out, "data_dir     %s\n", cfg.ResolveDataDir(repoRoot))
	if res.Legacy {
		fmt.Fprintf(out, "layout       legacy (run: satelle runtime migrate)\n")
	} else {
		fmt.Fprintf(out, "layout       home-keyed\n")
	}
	return nil
}

func runRuntimeMigrate(out io.Writer, a *app.App, force, dryRun bool) error {
	cfg := a.Config
	repoRoot := a.RepoRoot
	dataDir := a.DataDir
	if dataDir == "" {
		dataDir = cfg.ResolveDataDir(repoRoot)
	}
	legacyDB := filepath.Join(dataDir, config.DefaultDBName)
	if !fileExists(legacyDB) {
		// Already home-keyed, or never had a DB under data_dir.
		home := filepath.Join(config.GlobalDir(), config.RepoKey(repoRoot))
		if fileExists(filepath.Join(home, config.DefaultDBName)) {
			fmt.Fprintf(out, "already migrated: %s\n", home)
			return nil
		}
		return fmt.Errorf("runtime migrate: no legacy database at %s — nothing to migrate", legacyDB)
	}

	targetDir := filepath.Join(config.GlobalDir(), config.RepoKey(repoRoot))
	targetDB := filepath.Join(targetDir, config.DefaultDBName)
	if fileExists(targetDB) && !force {
		return fmt.Errorf("runtime migrate: target already has %s (pass --force to overwrite)", targetDB)
	}

	if dryRun {
		fmt.Fprintf(out, "dry-run: would copy\n")
		fmt.Fprintf(out, "  database: %s → %s (VACUUM INTO)\n", legacyDB, targetDB)
		for _, name := range []string{"logs", "backups", "stories"} {
			src := filepath.Join(dataDir, name)
			if st, err := os.Stat(src); err == nil && st.IsDir() {
				fmt.Fprintf(out, "  dir:      %s → %s\n", src, filepath.Join(targetDir, name))
			}
		}
		fmt.Fprintf(out, "  leave legacy tree in place at %s\n", dataDir)
		return nil
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("runtime migrate: mkdir %s: %w", targetDir, err)
	}
	// Remove a prior target DB when --force so VACUUM INTO can create cleanly.
	if force {
		for _, p := range []string{targetDB, targetDB + "-wal", targetDB + "-shm"} {
			_ = os.Remove(p)
		}
	}

	if err := vacuumInto(legacyDB, targetDB); err != nil {
		return fmt.Errorf("runtime migrate: copy database: %w", err)
	}
	fmt.Fprintf(out, "copied database → %s\n", targetDB)

	for _, name := range []string{"logs", "backups", "stories"} {
		src := filepath.Join(dataDir, name)
		dst := filepath.Join(targetDir, name)
		if st, err := os.Stat(src); err != nil || !st.IsDir() {
			continue
		}
		if err := copyDir(src, dst); err != nil {
			return fmt.Errorf("runtime migrate: copy %s: %w", name, err)
		}
		fmt.Fprintf(out, "copied %s/ → %s\n", name, dst)
	}

	fmt.Fprintf(out, "\nmigration complete. Legacy tree left at %s\n", dataDir)
	fmt.Fprintf(out, "After verifying with `satelle status`, you may remove runtime leftovers:\n")
	fmt.Fprintf(out, "  rm -f %s %s-wal %s-shm\n", legacyDB, legacyDB, legacyDB)
	fmt.Fprintf(out, "  rm -rf %s %s %s\n",
		filepath.Join(dataDir, "logs"),
		filepath.Join(dataDir, "backups"),
		filepath.Join(dataDir, "stories"))
	fmt.Fprintf(out, "(Do not remove authored files: satelle.toml, agents.toml, constitution.md, workflows/, skills/, principles/, documents/, tasks/.)\n")
	return nil
}

// vacuumInto copies srcDB into dstDB using SQLite VACUUM INTO (atomic, consistent
// snapshot; avoids torn wal/shm file copies of a live database).
func vacuumInto(srcDB, dstDB string) error {
	if err := os.MkdirAll(filepath.Dir(dstDB), 0o755); err != nil {
		return err
	}
	// Open source read-only so we never mutate the operator's live ledger.
	dsn := "file:" + filepath.ToSlash(srcDB) + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	// VACUUM INTO wants a path string; escape single quotes for the SQL literal.
	escaped := strings.ReplaceAll(dstDB, "'", "''")
	_, err = db.Exec("VACUUM INTO '" + escaped + "'")
	return err
}

// copyDir recursively copies a directory tree (files + dirs). Destination parents
// are created. Existing files at the destination are overwritten.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil // skip symlinks
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}
