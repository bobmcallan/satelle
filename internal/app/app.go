// Package app is satelle's local bootstrap (build order step 3). It loads the
// config, resolves the repo root, opens the per-repo database (home-keyed under
// ~/.satelle/<repo-key>/ by default — sty_4660bbe1), and wires the dynamic
// stores + authored-doc index onto it — the in-process path every CLI command
// (and the local web server) reaches data through.
//
// The OSS tier is always local, so there is no remote-dispatch branch: Open is
// the whole "backend". Zero-config works — a repo with no satelle.toml (but
// WITH a .satelle/ directory) falls back to defaults against the current
// directory. A repo with no .satelle/ at all is not governed by satelle and
// Open refuses it with ErrNotInitialised rather than materialising a runtime
// plane for it (sty_20a7824c).
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

// ErrNotInitialised reports that the working directory is not inside a satelle
// repo — no `.satelle/` directory here or in any parent. Callers compare with
// errors.Is and translate it into their own operator-facing message: the CLI
// says "run satelle init", the session hooks treat it as "not governed" and go
// inert. It carries the repo root that was checked.
var ErrNotInitialised = errors.New("not a satelle repo (no .satelle/ here or in any parent) — run `satelle init`")

// NotInitialisedError is the concrete ErrNotInitialised carrying the root that
// was checked, so a caller can name the path it refused.
type NotInitialisedError struct{ RepoRoot string }

func (e *NotInitialisedError) Error() string {
	return fmt.Sprintf("not a satelle repo (no .satelle/ in %s or any parent) — run `satelle init`", e.RepoRoot)
}
func (e *NotInitialisedError) Is(target error) bool { return target == ErrNotInitialised }
func (e *NotInitialisedError) Unwrap() error        { return ErrNotInitialised }

// Open loads config (walking up for .satelle/satelle.toml), opens the database,
// and returns the wired App. A missing config is not an error — the zero-value
// Config runs on defaults against the current directory (zero-config). The
// caller owns Close. A still-unmigrated legacy DB emits a one-line deprecation
// note on stderr (stdout stays clean for JSON).
//
// A repo with NO `.satelle/` directory is refused with ErrNotInitialised BEFORE
// anything is written. This is the single governance guard for every
// store-backed verb and every session hook, because Open is the one seam
// through which they all reach the two writes below — store.Open (which
// MkdirAll's the runtime plane and creates+migrates the database) and
// WriteRepoPathMarker. Guarding it per-verb would leave the rest still
// materialising a plane for a repo satelle does not govern (sty_20a7824c).
// `satelle init` does not come through here — it calls store.Open and
// WriteRepoPathMarker directly, which is how creating stays possible.
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

	// Governance guard — before store.Open and WriteRepoPathMarker, so an
	// ungoverned repo costs zero writes. Keyed on the .satelle DIRECTORY, not on
	// satelle.toml, because zero-config repos legitimately have no toml.
	if _, ok := config.FindDataDir(repoRoot); !ok {
		return nil, &NotInitialisedError{RepoRoot: repoRoot}
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
	// Record reverse map for `satelle runtime list` (sty_c36c211f). Best-effort —
	// a marker write failure must not block open.
	_ = config.WriteRepoPathMarker(rt.Dir, repoRoot)
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
