// Package app is satelle's local bootstrap (build order step 3). It loads the
// config, resolves the repo root, opens the per-repo database (home-keyed under
// ~/.satelle/<repo-key>/ by default — sty_4660bbe1), and wires the dynamic
// stores + authored-doc index onto it — the in-process path every CLI command
// (and the local web server) reaches data through.
//
// The OSS tier is always local, so there is no remote-dispatch branch: Open is
// the whole "backend". Zero-config works — a repo with no satelle.toml falls
// back to defaults against the current directory.
package app

import (
	"errors"
	"fmt"
	"os"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
)

// App is the wired local runtime: resolved config + the open per-repo database.
// DataDir holds authored substrate (<repo>/.satelle); RuntimeDir holds the DB,
// logs, backups, and stories cache (~/.satelle/<repo-key>/, or legacy data_dir
// until `satelle runtime migrate`).
type App struct {
	Config     config.Config
	RepoRoot   string
	DataDir    string // authored plane: toml, workflows, skills, principles, tasks, …
	RuntimeDir string // runtime plane: satelle.db, logs/, backups/, stories/
	DBPath     string
	Store      *store.DB
}

// Open loads config (walking up for .satelle/satelle.toml), opens the database,
// and returns the wired App. A missing config is not an error — the zero-value
// Config runs on defaults against the current directory (zero-config). The
// caller owns Close. A still-unmigrated legacy DB emits a one-line deprecation
// note on stderr (stdout stays clean for JSON).
func Open() (*App, error) {
	cfg, cfgPath, err := config.Load("")
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return nil, err
	}

	// Repo root: the dir holding .satelle/ when a config was found, else CWD.
	repoRoot := "."
	if cfgPath != "" {
		repoRoot = config.RepoRootFromConfigPath(cfgPath)
	} else if cwd, e := os.Getwd(); e == nil {
		repoRoot = cwd
	}

	dataDir := cfg.ResolveDataDir(repoRoot)
	rt := cfg.ResolveRuntimeDir(repoRoot)
	dbPath := cfg.ResolveDB(repoRoot)
	if note := cfg.LegacyRuntimeNote(repoRoot); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &App{
		Config:     cfg,
		RepoRoot:   repoRoot,
		DataDir:    dataDir,
		RuntimeDir: rt.Dir,
		DBPath:     dbPath,
		Store:      st,
	}, nil
}

// AuthoredDirs returns the kind→dir map the directory monitor watches/indexes.
func (a *App) AuthoredDirs() map[string]string {
	return a.Config.ResolveAuthoredDirs(a.RepoRoot)
}

// Close releases the database handle.
func (a *App) Close() error {
	if a.Store != nil {
		return a.Store.Close()
	}
	return nil
}
