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
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

func init() {
	var configArg string
	cmd := &cobra.Command{
		Use: "init",
		// `satelle install` reads naturally at first contact — an alias, same
		// implementation and flags (sty_77367228). (No `verify` alias: the generic
		// `satelle validate` it would have aliased was removed for per-noun
		// validators.)
		Aliases: []string{"install"},
		Short:   "Scaffold this repo for satelle (.satelle/, config, database, authored dirs)",
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

	// 2b. agents.toml — the agents layer (how each agent runs). Created only if
	//     absent; an absent file is the read-only default, so this documents the
	//     knobs without changing behaviour. A repo still carrying the legacy
	//     actors.toml is treated as present (no re-scaffold) — the loader reads
	//     either (sty_536f9960).
	agentsPath := filepath.Join(dataDir, config.AgentsConfigName)
	legacyPath := filepath.Join(dataDir, config.ActorsConfigName)
	_, legacyErr := os.Stat(legacyPath)
	switch _, statErr := os.Stat(agentsPath); {
	case statErr == nil:
		fmt.Fprintln(out, initLine(false, config.DefaultDataDir+"/"+config.AgentsConfigName))
	case os.IsNotExist(statErr) && legacyErr == nil:
		// Legacy actors.toml present: leave it; report it rather than scaffolding.
		fmt.Fprintln(out, initLine(false, config.DefaultDataDir+"/"+config.ActorsConfigName))
	case os.IsNotExist(statErr):
		if werr := os.WriteFile(agentsPath, []byte(scaffoldAgentsToml), 0o644); werr != nil {
			return fmt.Errorf("init: write %s: %w", agentsPath, werr)
		}
		fmt.Fprintln(out, initLine(true, config.DefaultDataDir+"/"+config.AgentsConfigName))
	default:
		return fmt.Errorf("init: stat %s: %w", agentsPath, statErr)
	}

	// 2c. constitution.md — the project constitution injected every session as
	//     order-zero context (epic:session-context). Created only if absent; a repo
	//     authors its own, and re-init never clobbers it.
	constitutionPath := filepath.Join(dataDir, config.DefaultConstitutionName)
	switch _, statErr := os.Stat(constitutionPath); {
	case statErr == nil:
		fmt.Fprintln(out, initLine(false, config.DefaultDataDir+"/"+config.DefaultConstitutionName))
	case os.IsNotExist(statErr):
		if werr := os.WriteFile(constitutionPath, []byte(scaffoldConstitution), 0o644); werr != nil {
			return fmt.Errorf("init: write %s: %w", constitutionPath, werr)
		}
		fmt.Fprintln(out, initLine(true, config.DefaultDataDir+"/"+config.DefaultConstitutionName))
	default:
		return fmt.Errorf("init: stat %s: %w", constitutionPath, statErr)
	}

	// 3. Authored-markdown dirs — create each with a tiny README.md describing
	//    what it should contain (the README is also the tracked keep-file). The
	//    per-story markdown mirror was removed (sty_fa1e02e1) and story attachments
	//    create their dir on demand, so .satelle/stories is NOT scaffolded here
	//    (sty_746a0c98).
	for _, kind := range config.AuthoredKinds {
		dir := filepath.Join(dataDir, kind)
		dirCreated, derr := ensureDir(dir)
		if derr != nil {
			return derr
		}
		readmeCreated, rerr := ensureReadme(dir, kind)
		if rerr != nil {
			return rerr
		}
		fmt.Fprintln(out, initLine(dirCreated || readmeCreated, config.DefaultDataDir+"/"+kind+"/"))
	}

	// 3b. Seed the default substrate into a FRESH repo: materialise the embedded
	//     baseline workflow and the embedded skills it references into .satelle so
	//     they are visible/editable on disk. Only when the workflows dir has no
	//     authored workflow yet — never clobbering or competing with an existing one.
	for _, line := range materializeBaseline(dataDir) {
		fmt.Fprintln(out, line)
	}

	// 3c. Materialise the embedded operating PRINCIPLES into .satelle/principles when
	//     absent. The runtime index no longer overlays embedded docs (sty_94da9ac9),
	//     so the principles:session session set + the on-demand principles must
	//     live on disk to be LISTED (SessionStart injection) and discoverable. The
	//     baseline WORKFLOW stays embedded-only (Get fallback); only principles seed here.
	for _, line := range materializePrinciples(dataDir) {
		fmt.Fprintln(out, line)
	}

	// 3d. Tasks are AUTHORED substrate but ingested into the workitem store (not the
	//     OKF doc index), so .satelle/tasks is scaffolded here — NOT via AuthoredKinds
	//     (that would route it through the OKF normalizer). Create the dir + README
	//     keep-file and seed one starter task HEADER, idempotently (sty_c1b3b4e3).
	for _, line := range seedTasks(dataDir) {
		fmt.Fprintln(out, line)
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
	if added, updated, herr := ensureClaudeHooks(repoRoot); herr != nil {
		return herr
	} else if len(updated) > 0 {
		fmt.Fprintf(out, "  ~ .claude/settings.json (hook updated: %s)\n", strings.Join(updated, "; "))
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
          { "type": "command", "command": "satelle reindex" },
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
// retiredHookCommands maps RETIRED satelle CLI commands to their replacements —
// the reconciliation seam for hook commands in an existing .claude/settings.json
// (sty_6a919dff): a repo initialised before a rename otherwise invokes a removed
// command forever (observed: a SessionStart hook still running `satelle index`).
// Extend this map on every future rename/removal.
var retiredHookCommands = map[string]string{
	"satelle index": "satelle reindex",
}

// reconcileClaudeHooks surgically rewrites known-retired satelle commands inside
// an existing settings.json — an exact-command string swap (word-boundary
// guarded), so every other byte of the user-owned file is preserved. Returns the
// applied renames ("old -> new"), empty when nothing was stale. Idempotent.
func reconcileClaudeHooks(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := string(data)
	var changed []string
	for old, repl := range retiredHookCommands {
		re := regexp.MustCompile(regexp.QuoteMeta(old) + `\b`)
		if re.MatchString(s) {
			s = re.ReplaceAllString(s, repl)
			changed = append(changed, old+" -> "+repl)
		}
	}
	if len(changed) == 0 {
		return nil, nil
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		return nil, err
	}
	sort.Strings(changed)
	return changed, nil
}

// ensureClaudeHooks writes .claude/settings.json with the process hooks when
// absent, and RECONCILES known-retired satelle hook commands in an existing one
// (sty_6a919dff) — the user-owned file is otherwise preserved byte-for-byte.
// Returns whether it created the file and any applied hook renames.
func ensureClaudeHooks(repoRoot string) (bool, []string, error) {
	dir := filepath.Join(repoRoot, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, nil, fmt.Errorf("init: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "settings.json")
	if _, err := os.Stat(path); err == nil {
		updated, rerr := reconcileClaudeHooks(path)
		if rerr != nil {
			return false, nil, fmt.Errorf("init: reconcile %s: %w", path, rerr)
		}
		return false, updated, nil
	} else if !os.IsNotExist(err) {
		return false, nil, fmt.Errorf("init: stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(claudeHookSettings), 0o644); err != nil {
		return false, nil, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil, nil
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
# logs_max_size_kb = 5120        # roll a .satelle/logs file past this size (default 5 MiB)
# logs_max_files = 7             # keep at most this many rotated log files (default 7)

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

// scaffoldAgentsToml is the documented agents layer a fresh init writes. Every
// key is commented: an absent/blank file is the read-only default (executor
// in-loop, reviewer isolated with Read,Grep,Glob), so this only documents the
// knobs. A repo may widen or rebind transparently — the override is a committed
// file, the operator's choice.
const scaffoldAgentsToml = `# agents.toml — the agents layer: how each agent runs (backend + tool grant).
# FULLY DEFINED by init (no hidden coded configuration, sty_892517e7): every
# value below is the ACTIVE default, written out so the operator sees exactly
# what runs. Edit freely; an absent file falls back to these same defaults.
#
# The agent operating model (see the satelle-agent-model principle):
#   - executor  — runs IN-LOOP as the driving session (context, principles,
#     skills via the substrate). Not an isolated process.
#   - reviewer  — an ISOLATED, READ-ONLY sub-process: the rubric rides as its
#     system prompt; it judges, never mutates (the claude preset denylists
#     Write/Edit/NotebookEdit/Bash on top of the read-only grant).
#   - any OTHER top-level [<name>] is an optional named agent, always isolated;
#     a workflow node allocates a step to it via agent=<name>. A named agent
#     that MUTATES declares its own full-command harness + wide grant.
#
# THE HARNESS TEMPLATE: a SINGLE token (e.g. "claude") is a built-in preset; a
# MULTI-token value is a full command taken verbatim ({system}/{tools}/{model}
# substituted, payload on stdin).

[executor]
harness = "in-loop"            # the orchestrator/driving session itself

[reviewer]
harness = "claude"             # preset: claude -p --disallowedTools Write,Edit,NotebookEdit,Bash --append-system-prompt {system} --allowedTools {tools} --model {model}
tools   = "Read,Grep,Glob"     # read-only grant — widen at your own risk
model   = ""                   # empty inherits the CLI's default; e.g. "sonnet" reviews on a cheaper/faster model

# A named EXECUTOR agent for isolated mutating steps (e.g. a commit/push step),
# with an explicit full-command harness and a wide grant:
# [commit-agent]
# harness = "claude -p --append-system-prompt {system} --allowedTools {tools}"
# tools   = "Read,Edit,Bash(git:*),Bash(gh:*),Bash(make:*),Bash(satelle:*)"
`

// scaffoldConstitution is the project-constitution template a fresh init writes to
// .satelle/constitution.md — the order-zero doc injected into every session
// (epic:session-context). It is a starting point the operator rewrites for THIS
// repo; kept short so the session budget stays lean. Re-init never clobbers it.
const scaffoldConstitution = `---
type: constitution
title: Project constitution
description: The local/repo definition the agent reads as order-zero context, injected every session. Rewrite this for your repo.
---

# Project constitution

<!-- This is your repo's order-zero context — injected into every session. Keep it
short and high-signal: what an agent must know to work in THIS repo. Replace this
placeholder. -->

- **What this repo is:** …
- **Ground rules:** …
- **Where the process lives:** authored substrate under ` + "`.satelle/`" + ` (workflows,
  principles, skills) — edited without a binary release.
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
# the repo-local pinned binary (satelle update --local) is local state, never committed
.satelle/satelle
# the flat operation log (a read-only reviewer's read surface) is local evidence
.satelle/logs/
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

// dirReadme describes what each authored dir should contain — written as the
// dir's README.md so the skeleton is self-documenting (and the README keeps the
// otherwise-empty dir tracked).
var dirReadme = map[string]string{
	"documents":  "# documents\n\nFree-form knowledge documents in the Open Knowledge Format (OKF):\nplain markdown with YAML frontmatter carrying a required `type`. Drop reference\nnotes, designs, and commit summaries here; `index.md`/`log.md` are reserved.\n",
	"workflows":  "# workflows\n\nAuthored lifecycles in the DOT standard (the agent model): each node is a step\nwith an `agent` (executor|reviewer), each edge a transition, the edge into a\nreviewer node its gate. Frontmatter needs `type: workflow`, `scope`, `applies_to`.\nThe lifecycle must start at `backlog`; `done` is terminal.\n",
	"principles": "# principles\n\nAuthored principles (markdown, `type: principle`). They are resolvable on demand;\nthe single always-resident operating principle is injected at session start.\n",
	"skills":     "# skills\n\nAuthored skills (`type: skill`): executor rubrics, reviewer rubrics, or a\nself-contained functional check (a fenced ```check block or a `check:` key).\nEverything a reviewer needs lives inside the skill.\n",
	"stories":    "# stories\n\nPer-story attachments live here under `<id>/…` (typed documents attached to a\nstory). The per-repo database is the sole story store — there is no markdown\nmirror of the backlog.\n",
	"tasks":      "# tasks\n\nAuthored task HEADERS (`tsk_*.md`, `type: task`): re-runnable work-definitions\nthat declare an ACTION and how success is VERIFIED. The file is the source of\ntruth; the DB indexes it. Each RUN is an execution under a per-task folder\n`<tsk_id>/exe_*.md`; create one with `satelle execution create --parent <tsk_id>`.\n",
}

// starterTaskID is the fixed id of the example task header init seeds — fixed so
// re-init is idempotent (it is never duplicated).
const starterTaskID = "tsk_example1"

// scaffoldStarterTask is the example task header seeded into a fresh repo's
// .satelle/tasks — a template demonstrating the ACTION + VERIFICATION contract
// (sty_c1b3b4e3). It passes the deterministic task structure check.
const scaffoldStarterTask = `---
id: tsk_example1
type: task
status: backlog
tags: example
---

# Example task — replace or delete me

A task is a re-runnable work-definition (a HEADER). Declare the ACTION and how
success is VERIFIED, then run it by creating an execution:
` + "`satelle execution create --parent tsk_example1 --title \"run 1\"`" + `.

ACTION: describe the concrete work this task performs.

VERIFICATION: describe the checkable evidence that the ACTION succeeded.
`

// seedTasks scaffolds .satelle/tasks (dir + README keep-file) and seeds the
// starter task header when absent — idempotent (re-init reports it as present and
// never clobbers an authored task). Returns report lines.
func seedTasks(dataDir string) []string {
	var lines []string
	dir := filepath.Join(dataDir, "tasks")
	dirCreated, derr := ensureDir(dir)
	if derr != nil {
		return lines
	}
	readmeCreated, _ := ensureReadme(dir, "tasks")
	lines = append(lines, initLine(dirCreated || readmeCreated, config.DefaultDataDir+"/tasks/"))
	starter := filepath.Join(dir, starterTaskID+".md")
	if !fileExists(starter) {
		if err := os.WriteFile(starter, []byte(scaffoldStarterTask), 0o644); err == nil {
			lines = append(lines, initLine(true, config.DefaultDataDir+"/tasks/"+starterTaskID+".md"))
		}
	} else {
		lines = append(lines, initLine(false, config.DefaultDataDir+"/tasks/"+starterTaskID+".md"))
	}
	return lines
}

// ensureReadme writes a dir's README.md (describing its contents) when absent.
func ensureReadme(dir, kind string) (bool, error) {
	path := filepath.Join(dir, "README.md")
	if fileExists(path) {
		return false, nil
	}
	body := dirReadme[kind]
	if body == "" {
		body = "# " + kind + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return false, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil
}

// materializeBaseline seeds a fresh repo's .satelle with the embedded baseline
// workflow and the embedded skills it references, so the default substrate is
// visible/editable on disk. It is a no-op when the workflows dir already holds an
// authored workflow (never clobber, never create a competing wildcard workflow).
// Returns report lines.
// materializePrinciples writes every embedded default PRINCIPLE into
// .satelle/principles when absent, so the operating principles — including the
// principles:session session set — live on disk and are LISTED for SessionStart
// injection + doc-list discovery (the runtime index no longer overlays embedded
// docs, sty_94da9ac9). Embedded principles remain the canonical seed; an existing
// on-disk file is never clobbered.
func materializePrinciples(dataDir string) []string {
	var lines []string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "principles" {
			continue
		}
		p := filepath.Join(dataDir, "principles", d.Name+".md")
		if fileExists(p) {
			continue
		}
		if err := os.WriteFile(p, []byte(d.Body), 0o644); err == nil {
			lines = append(lines, initLine(true, config.DefaultDataDir+"/principles/"+d.Name+".md"))
		}
	}
	return lines
}

func materializeBaseline(dataDir string) []string {
	wfDir := filepath.Join(dataDir, "workflows")
	if hasMarkdown(wfDir) {
		return nil // an authored workflow exists — respect it
	}
	body, ok := embeddedDefault("workflows", "satelle-baseline-workflow")
	if !ok {
		return nil
	}
	var lines []string
	// The baseline WORKFLOW stays EMBEDDED-ONLY (sty_3f9a6124): it is the canonical
	// default and must never exist as an editable repo file (satelle-repo-agnostic).
	// A fresh repo resolves it from the embedded layer, so a repo's OWN workflow
	// takes precedence (repo-wildcard beats the embedded wildcard) instead of tying
	// with a scaffolded copy. Only the reviewer SKILLS the baseline references are
	// materialised, so their rubrics are visible/editable on disk.
	// Materialise every embedded skill the baseline references that exists in the
	// embedded layer (advisory gates not embedded simply stay absent by design).
	if spec, parsed := wfdot.Parse(body); parsed {
		for _, name := range referencedSkills(spec) {
			sBody, has := embeddedDefault("skills", name)
			if !has {
				continue
			}
			sPath := filepath.Join(dataDir, "skills", name+".md")
			if fileExists(sPath) {
				continue
			}
			if err := os.WriteFile(sPath, []byte(sBody), 0o644); err == nil {
				lines = append(lines, initLine(true, config.DefaultDataDir+"/skills/"+name+".md"))
			}
		}
	}
	return lines
}

// referencedSkills returns every skill a workflow names — node prompts and edge
// gates — deduped.
func referencedSkills(spec wfdot.Spec) []string {
	set := map[string]bool{}
	for _, s := range spec.States {
		if s.Skill != "" {
			set[s.Skill] = true
		}
	}
	for _, tr := range spec.Transitions {
		if tr.Skill != "" {
			set[tr.Skill] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// embeddedDefault returns the body of the embedded canonical artifact for
// (kind, name), if any.
func embeddedDefault(kind, name string) (string, bool) {
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == kind && d.Name == name {
			return d.Body, true
		}
	}
	return "", false
}

// hasMarkdown reports whether dir contains at least one .md file (ignoring
// README.md and .gitkeep).
func hasMarkdown(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		if e.Name() == "README.md" {
			continue
		}
		return true
	}
	return false
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
