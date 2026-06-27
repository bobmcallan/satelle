// `satelle init` — scaffold a repo for satelle, idempotently. It ensures the
// .satelle/ directory, a documented satelle.toml (created if missing, never
// clobbered), the authored-markdown dirs the directory monitor watches, the
// per-repo SQLite database (created + migrated), and a managed .gitignore block
// that keeps the local database out of git while committing the toml and the
// authored markdown. Re-running is safe: it reports what it added versus what
// was already present and never overwrites existing files.

package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
)

func init() {
	var configArg string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold this repo for satelle (.satelle/, config, database, authored dirs)",
		Long: `init makes a repo ready for satelle, idempotently. It ensures:

  - the .satelle/ directory,
  - a satelle.toml (created if missing, left intact if present) — every setting
    has a default, so the file ships fully commented and the repo runs zero-config,
  - the authored-markdown dirs (documents, workflows, principles, skills) the
    directory monitor watches and indexes,
  - the per-repo SQLite database at .satelle/satelle.db (created and migrated),
  - a managed .gitignore block keeping the local database out of git while
    committing the config and the authored markdown.

Re-running is safe: existing files are preserved and the report shows what was
added versus already present.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.OutOrStdout(), initRepoRoot(configArg))
		},
	}
	cmd.Flags().StringVar(&configArg, "config", "", "path to satelle.toml (resolves the repo root; default: walk up from CWD)")
	register(cmd)
}

// initRepoRoot resolves the repo to scaffold: the directory holding an existing
// .satelle/ (via config resolution), else the current directory for a fresh repo.
func initRepoRoot(configArg string) string {
	if _, path, err := config.Load(configArg); err == nil && path != "" {
		return config.RepoRootFromConfigPath(path)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// runInit performs the idempotent scaffold for repoRoot.
func runInit(out io.Writer, repoRoot string) error {
	// 1. .satelle/ directory.
	dataDir := filepath.Join(repoRoot, config.DefaultDataDir)
	created, err := ensureDir(dataDir)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, initLine(created, config.DefaultDataDir+"/"))

	// 2. satelle.toml — created only if absent; never overwritten.
	tomlPath := filepath.Join(dataDir, config.ConfigName)
	switch _, statErr := os.Stat(tomlPath); {
	case statErr == nil:
		fmt.Fprintln(out, initLine(false, config.DefaultDataDir+"/"+config.ConfigName))
	case os.IsNotExist(statErr):
		if werr := os.WriteFile(tomlPath, []byte(scaffoldToml), 0o644); werr != nil {
			return fmt.Errorf("init: write %s: %w", tomlPath, werr)
		}
		fmt.Fprintln(out, initLine(true, config.DefaultDataDir+"/"+config.ConfigName))
	default:
		return fmt.Errorf("init: stat %s: %w", tomlPath, statErr)
	}

	// 3. Authored-markdown dirs — create with a .gitkeep so they exist (and are
	//    tracked) for the directory monitor and for dropping content into.
	for _, kind := range config.AuthoredKinds {
		dir := filepath.Join(dataDir, kind)
		dirCreated, derr := ensureDir(dir)
		if derr != nil {
			return derr
		}
		if keepCreated, kerr := ensureGitkeep(dir); kerr != nil {
			return kerr
		} else {
			fmt.Fprintln(out, initLine(dirCreated || keepCreated, config.DefaultDataDir+"/"+kind+"/"))
		}
	}

	// 4. The per-repo database — open (creating + migrating) then close, so a
	//    fresh repo lands a ready satelle.db with no first-command surprise.
	dbPath := filepath.Join(dataDir, config.DefaultDBName)
	dbExisted := fileExists(dbPath)
	db, derr := store.Open(dbPath)
	if derr != nil {
		return fmt.Errorf("init: open database: %w", derr)
	}
	_ = db.Close()
	fmt.Fprintln(out, initLine(!dbExisted, config.DefaultDataDir+"/"+config.DefaultDBName))

	// 5. .gitignore managed block — keep the local DB out of git, commit the rest.
	if added, gerr := ensureGitignore(repoRoot); gerr != nil {
		return gerr
	} else {
		fmt.Fprintln(out, initLine(added, ".gitignore (satelle local-state block)"))
	}

	// 6. .claude/settings.json — the blocking process hooks that enforce the
	//    workflow on the coding agent (created only if absent; never overwritten).
	if added, herr := ensureClaudeHooks(repoRoot); herr != nil {
		return herr
	} else {
		fmt.Fprintln(out, initLine(added, ".claude/settings.json (process hooks)"))
	}

	fmt.Fprintln(out, "\nReady. Try: satelle status · satelle story create --title \"…\" · satelle serve")
	return nil
}

// claudeHookSettings is the .claude/settings.json satelle init scaffolds: the
// SessionStart context injector plus the BLOCKING PreToolUse gates that enforce
// the authored workflow — edits require an engaged story, and so do commits/
// pushes. The agent must create stories and drive them through the gates.
const claudeHookSettings = `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          { "type": "command", "command": "satelle index" },
          { "type": "command", "command": "satelle hook context" }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit|NotebookEdit",
        "hooks": [
          { "type": "command", "command": "satelle hook gate || exit 2" }
        ]
      },
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "satelle hook commitgate || exit 2" }
        ]
      }
    ]
  }
}
`

// ensureClaudeHooks writes .claude/settings.json with the process hooks when it
// does not already exist. Returns whether it created the file. It never
// overwrites an existing settings.json (the repo/user owns it).
func ensureClaudeHooks(repoRoot string) (bool, error) {
	dir := filepath.Join(repoRoot, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("init: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "settings.json")
	if _, err := os.Stat(path); err == nil {
		return false, nil // exists — leave it
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("init: stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(claudeHookSettings), 0o644); err != nil {
		return false, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil
}

// scaffoldToml is the documented config a fresh init writes. Every key is
// commented because each has a default — the repo runs zero-config until a knob
// is uncommented.
const scaffoldToml = `# satelle.toml — per-repo config (committed, secret-free). Every setting has a
# default, so this file may stay fully commented; uncomment a key to override.
# Per-user overrides go in satelle.local.toml beside this file (gitignored).

# data_dir = ".satelle"          # home for the per-repo database (default)
# db = ".satelle/satelle.db"     # database path (default: <data_dir>/satelle.db)
# web_port = 8787                # 'satelle serve' listen port (default)
# log_level = "info"             # debug | info | warn | error (default info)

# [review] — opt into reviewer-gated work (off by default). The reviewer rubrics
# ship embedded; enabling enforcement is your choice (needs an agent CLI — see
# 'satelle agent'). gate_create runs the required-structure reviewer on
# 'story/task create', pushing non-conforming drafts back instead of persisting.
# [review]
# gate_create = true

# substrate_roots — per-kind parent dir for authored markdown. Unset means
# <data_dir>/<kind> (e.g. .satelle/documents). Point a kind elsewhere — even
# outside .satelle/ — to author it at the repo root or another path:
# [substrate_roots]
# documents = "."                # → ./documents
# skills = "."                   # → ./skills
`

// gitignoreMarker opens the managed block ensureGitignore maintains. Its
// presence anywhere in the file makes a re-run a no-op.
const gitignoreMarker = "# >>> satelle (managed) >>>"

// gitignoreBlock keeps the local database (+ WAL/SHM sidecars) and the
// per-user overlay out of git, while leaving the committed toml and the
// authored markdown tracked.
const gitignoreBlock = gitignoreMarker + `
# satelle's per-repo database is local state — ignore it and its sidecars, plus
# the per-user config overlay. The committed satelle.toml and the authored
# markdown under .satelle/ stay tracked.
.satelle/satelle.db
.satelle/satelle.db-wal
.satelle/satelle.db-shm
.satelle/satelle.local.toml
# <<< satelle (managed) <<<
`

// ensureGitignore writes the managed block to the repo's .gitignore,
// idempotently and non-destructively: it creates the file with the block when
// absent, appends it when the file exists without the marker, and is a no-op
// when the marker is already present. Returns whether it wrote anything.
func ensureGitignore(repoRoot string) (bool, error) {
	path := filepath.Join(repoRoot, ".gitignore")
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if strings.Contains(string(raw), gitignoreMarker) {
			return false, nil
		}
		body := string(raw)
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if werr := os.WriteFile(path, []byte(body+"\n"+gitignoreBlock), 0o644); werr != nil {
			return false, fmt.Errorf("init: append %s: %w", path, werr)
		}
		return true, nil
	case os.IsNotExist(err):
		if werr := os.WriteFile(path, []byte(gitignoreBlock), 0o644); werr != nil {
			return false, fmt.Errorf("init: write %s: %w", path, werr)
		}
		return true, nil
	default:
		return false, fmt.Errorf("init: read %s: %w", path, err)
	}
}

// ensureGitkeep writes an empty .gitkeep into dir if absent, so an otherwise
// empty authored dir is tracked. Returns whether it created the file.
func ensureGitkeep(dir string) (bool, error) {
	path := filepath.Join(dir, ".gitkeep")
	if fileExists(path) {
		return false, nil
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return false, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil
}

// initLine renders a one-line report: "+ created" or "= present".
func initLine(created bool, what string) string {
	if created {
		return "  + " + what
	}
	return "  = " + what + " (already present)"
}

// ensureDir creates dir (and parents) if absent. Returns whether it created it.
func ensureDir(dir string) (bool, error) {
	switch _, err := os.Stat(dir); {
	case err == nil:
		return false, nil
	case os.IsNotExist(err):
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return false, fmt.Errorf("init: create %s: %w", dir, mkErr)
		}
		return true, nil
	default:
		return false, fmt.Errorf("init: stat %s: %w", dir, err)
	}
}

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
