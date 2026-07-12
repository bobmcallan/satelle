// `satelle init` — scaffold a repo for satelle, idempotently. It ensures the
// .satelle/ directory, a documented satelle.toml (created if missing, never
// clobbered), the authored-markdown dirs the directory monitor watches, the
// per-repo SQLite database (created + migrated), and a managed .gitignore block
// that keeps the local database out of git while committing the toml and the
// authored markdown. Re-running is safe: it reports what it added versus what
// was already present and never overwrites existing files. It ends by
// VALIDATING the deployed system (sty_d0d6bb67) and exits non-zero when the
// deployment does not validate green.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// lookPath is exec.LookPath, swappable in tests so harness detection is pure.
var lookPath = exec.LookPath

func init() {
	var configArg string
	var noWorkspace bool
	cmd := &cobra.Command{
		Use: "init",
		// `satelle install` reads naturally at first contact — an alias, same
		// implementation and flags (sty_77367228, sty_0e268c9a). (No `verify` alias:
		// the generic `satelle validate` it would have aliased was removed for
		// per-noun validators.)
		Aliases: []string{"install"},
		Short:   "Scaffold this repo for satelle (.satelle/, config, database, authored dirs)",
		Long: `init (alias: install) makes a repo ready for satelle, idempotently. It ensures:

  - the .satelle/ directory,
  - a satelle.toml (created if missing, left intact if present) — every setting
    has a default, so the file ships fully commented and the repo runs zero-config,
  - the authored-markdown dirs (documents, workflows, principles, skills) the
    directory monitor watches and indexes,
  - the per-repo SQLite database at .satelle/satelle.db (created and migrated),
  - a managed .gitignore block keeping the local database out of git while
    committing the config and the authored markdown,
  - process hooks for the detected coding harness(es): Claude
    (.claude/settings.json) and/or Grok (.grok/hooks/satelle.json),
  - registration of this repo in the local workspace registry (opt out with
    --no-workspace) so 'satelle serve' and the /workspace view see it.

init/install end by VALIDATING the deployed system — the agents layer must load
and every substrate artifact must pass its deterministic structure check — and
exit non-zero when validation fails (broken configuration refuses to run).

Re-running is safe: existing files are preserved and the report shows what was
added versus already present. Both names share one implementation.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.OutOrStdout(), initRepoRoot(configArg), noWorkspace)
		},
	}
	cmd.Flags().StringVar(&configArg, "config", "", "path to satelle.toml (resolves the repo root; default: walk up from CWD)")
	cmd.Flags().BoolVar(&noWorkspace, "no-workspace", false, "skip registering this repo in the local workspace registry")
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
// noWorkspace skips registration in the machine-local workspace registry
// (sty_3bdbdc38); default is false (register).
func runInit(out io.Writer, repoRoot string, noWorkspace bool) error {
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

	// 3b. Seed the COMPLETE default solution into a FRESH repo: materialise the
	//     embedded generic project/parent/task-execution workflows and every gate
	//     skill they (or the baseline fallback) reference into .satelle, so a fresh
	//     repo works end-to-end and validates green immediately after init. Only
	//     when the workflows dir has no authored workflow yet — never clobbering or
	//     competing with an existing set (sty_a7cbd6dd).
	for _, line := range materializeDefaultSolution(dataDir) {
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

	// 3c-bis. Advisory skills — embedded executor rubrics NOT referenced by any
	//     workflow (so the default-solution seeding never carries them), seeded
	//     unconditionally when absent, even beside an authored workflow set
	//     (sty_f4c1bd90): they guide the in-loop agent, they don't gate anything.
	for _, line := range materializeAdvisorySkills(dataDir) {
		fmt.Fprintln(out, line)
	}

	// 3d. Tasks are AUTHORED substrate but ingested into the workitem store (not the
	//     OKF doc index), so .satelle/tasks is scaffolded here — NOT via AuthoredKinds
	//     (that would route it through the OKF normalizer). Create the dir + README
	//     keep-file (sty_c1b3b4e3), then seed the embedded substrate-audit task — the
	//     one repo-agnostic default task that ships with the binary so a fresh repo
	//     has a re-runnable quality audit resolving via the task workflow immediately
	//     (sty_d4360e90). No generic example task (sty_04ec1fe6): one named default,
	//     not example noise.
	for _, line := range seedTasks(dataDir) {
		fmt.Fprintln(out, line)
	}
	for _, line := range materializeTasks(dataDir) {
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

	// 6. Process hooks for the coding harness(es) on this machine/repo — Claude
	//    (.claude/settings.json) and/or Grok (.grok/hooks/satelle.json). Detection
	//    prefers PATH + existing harness dirs; both scaffolds apply when both are
	//    present. Same satelle hook gate/commitgate/context/reindex policy either way.
	if err := ensureProcessHooks(out, repoRoot); err != nil {
		return err
	}

	// 6b. Local workspace registry (sty_3bdbdc38) — register this repo so the
	//     aggregate /workspace view and a running 'satelle serve' see it without
	//     a separate 'satelle workspace add'. LOCAL gc.Workspace only (no hosted
	//     network). Non-fatal on global-config errors; --no-workspace opts out.
	ensureWorkspaceRegistration(out, repoRoot, noWorkspace)

	// 7. Agent guidance — init is usually run BY a coding agent, and this report is
	//    its cue: when the repo carries an agent instruction file (CLAUDE.md /
	//    AGENTS.md), tell the reading agent to fold satelle into it. init never
	//    edits those files itself (they are user/agent-owned); it emits the
	//    instruction, the agent applies it (sty_4c406061).
	for _, line := range agentGuidance(repoRoot) {
		fmt.Fprintln(out, line)
	}

	// 8. Validate the deployed system (sty_d0d6bb67): init ends by PROVING the
	//    deployment green — agents layer loadable, every substrate artifact
	//    passing its deterministic structure check, workflow set consistent. A
	//    failing validation exits non-zero: the runtime refuses to run broken
	//    configuration, so init must not report success over it.
	if verr := validateDeployment(out, dataDir); verr != nil {
		return verr
	}

	fmt.Fprintln(out, "\nReady. Try: satelle status · satelle story create --title \"…\" · satelle serve")
	return nil
}

// agentInstructionFiles returns the agent instruction files present at the repo
// root — CLAUDE.md / AGENTS.md, matched case-insensitively — as their on-disk names.
func agentInstructionFiles(repoRoot string) []string {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(e.Name()) {
		case "claude.md", "agents.md":
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// agentGuidance renders the agent-facing note init appends to its report when
// the repo carries an agent instruction file: add/update a satelle section
// there with the basic loop, pointing agents at `satelle help` as the preferred
// reference — a short pointer, never a doc dump. Empty when no such file exists.
func agentGuidance(repoRoot string) []string {
	files := agentInstructionFiles(repoRoot)
	if len(files) == 0 {
		return nil
	}
	return []string{
		"",
		"Agent note: this repo carries " + strings.Join(files, " and ") + " — add a \"## satelle\" section there (or update the existing one) with the basics:",
		"  - satelle runs this repo's workflow: create a story (`satelle story create`), drive it through its gates (`satelle story set <id> --status …`); status is the sole proof of done.",
		"  - keep the section a short pointer: agents should consult `satelle help` (and `satelle help <topic>`) for the process — prefer that over duplicating satelle docs into " + strings.Join(files, "/") + ".",
		"  - define process agents and workflow steps in `.satelle/agents.toml` (a `[<name>]` binding: command/tools/model) and allocate a workflow node `agent=<name>` — NOT in a harness-specific agent dir (e.g. `.claude/agents`), which satelle cannot see, validate, dispatch, or carry repo-agnostically. See `satelle help agent-dispatch`.",
	}
}

// claudeHookSettings is the .claude/settings.json satelle init scaffolds: the
// SessionStart context injector plus the BLOCKING PreToolUse gates that enforce
// the authored workflow — edits require an engaged story, and so do commits/
// pushes. The agent must create stories and drive them through the gates.
// gate/commitgate commands prepend $HOME/.local/bin to PATH so a missing
// satelle-on-PATH cannot `|| exit 2` brick every Edit/Bash tool (sty_24b32127).
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
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook gate || exit 2" }
        ]
      },
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook commitgate || exit 2" }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook prompt" }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook stopcheck" }
        ]
      }
    ]
  }
}
`

// grokHookSettings is the satelle-owned Grok project hook file
// (.grok/hooks/satelle.json). Same policy commands as Claude; matchers cover
// Grok-native tool ids and Claude aliases Grok maps (sty_2fad11b0).
const grokHookSettings = `{
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
        "matcher": "Edit|Write|MultiEdit|NotebookEdit|search_replace|write",
        "hooks": [
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook gate || exit 2" }
        ]
      },
      {
        "matcher": "Bash|run_terminal_command",
        "hooks": [
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook commitgate || exit 2" }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook prompt" }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook stopcheck" }
        ]
      }
    ]
  }
}
`

// grokHooksRel is the repo-relative path of the satelle-owned Grok hooks file.
const grokHooksRel = ".grok/hooks/satelle.json"

// retiredHookCommands maps RETIRED satelle CLI commands to their replacements —
// the reconciliation seam for hook commands in an existing harness hook file
// (sty_6a919dff): a repo initialised before a rename otherwise invokes a removed
// command forever (observed: a SessionStart hook still running `satelle index`).
// Extend this map on every future rename/removal.
var retiredHookCommands = map[string]string{
	"satelle index": "satelle reindex",
}

// detectProcessHarnesses decides which process-hook scaffolds to apply.
// Signals: CLI on PATH (hasCLI), and/or an existing harness dir in the repo.
// When neither is detected, Claude is the default (preserves prior init behaviour).
// When both are present, both scaffolds apply — no silent single-vendor pick.
func detectProcessHarnesses(repoRoot string, hasCLI func(string) bool) (claude, grok bool) {
	if hasCLI == nil {
		hasCLI = func(name string) bool {
			_, err := lookPath(name)
			return err == nil
		}
	}
	claude = hasCLI("claude") || dirExists(filepath.Join(repoRoot, ".claude"))
	grok = hasCLI("grok") || dirExists(filepath.Join(repoRoot, ".grok"))
	if !claude && !grok {
		claude = true
	}
	return claude, grok
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// ensureProcessHooks scaffolds Claude and/or Grok process hooks per detection
// and reports each outcome on out (same initLine / "~ updated" style as before).
// When Grok is detected it also disables Claude-compat hooks in ~/.grok/config.toml
// so Grok does not double-fire the same satelle gate/commitgate (sty_24b32127).
func ensureProcessHooks(out io.Writer, repoRoot string) error {
	wantClaude, wantGrok := detectProcessHarnesses(repoRoot, nil)
	if wantClaude {
		added, updated, err := ensureClaudeHooks(repoRoot)
		if err != nil {
			return err
		}
		if len(updated) > 0 {
			fmt.Fprintf(out, "  ~ .claude/settings.json (hook updated: %s)\n", strings.Join(updated, "; "))
		} else {
			fmt.Fprintln(out, initLine(added, ".claude/settings.json (process hooks)"))
		}
	}
	if wantGrok {
		added, updated, err := ensureGrokHooks(repoRoot)
		if err != nil {
			return err
		}
		if len(updated) > 0 {
			fmt.Fprintf(out, "  ~ %s (hook updated: %s)\n", grokHooksRel, strings.Join(updated, "; "))
		} else {
			fmt.Fprintln(out, initLine(added, grokHooksRel+" (process hooks)"))
		}
		// Project hooks load only when Grok trusts the folder; grant trust for
		// this repo root so .grok/hooks/satelle.json is not silently skipped
		// (sty_edb01f49 — same effect as /hooks-trust, automatic on init).
		if err := ensureGrokFolderTrust(out, repoRoot); err != nil {
			return err
		}
		// Grok-native .grok/hooks/satelle.json is the sole process-hook path under
		// Grok; leave skills/rules/agents/mcps alone.
		if err := ensureGrokCompatConfig(out); err != nil {
			return err
		}
	}
	return nil
}

// ensureGrokFolderTrust records repoRoot in ~/.grok/trusted_folders.toml so
// Grok loads project process hooks. Reports a line only when the store changes
// (first trust or promote from trusted=false).
func ensureGrokFolderTrust(out io.Writer, repoRoot string) error {
	changed, abs, err := config.EnsureGrokFolderTrusted(repoRoot)
	if err != nil {
		return fmt.Errorf("init: grok folder trust: %w", err)
	}
	if !changed {
		return nil
	}
	fmt.Fprintf(out, "  + ~/.grok/trusted_folders.toml (Grok project hooks trusted for %s)\n", abs)
	return nil
}

// ensureGrokCompatConfig surgically upserts [compat.claude] hooks = false into
// ~/.grok/config.toml so Grok does not also load .claude/settings.json hooks
// (double-fire with .grok/hooks/satelle.json). Creates the file when absent;
// preserves every other key/table. Idempotent: already-false is a silent no-op
// (no write, no report line).
func ensureGrokCompatConfig(out io.Writer) error {
	path, err := config.GrokConfigPath()
	if err != nil {
		return fmt.Errorf("init: grok config path: %w", err)
	}
	var before string
	raw, rerr := os.ReadFile(path)
	switch {
	case rerr == nil:
		before = string(raw)
	case os.IsNotExist(rerr):
		// empty before
	default:
		return fmt.Errorf("init: read %s: %w", path, rerr)
	}
	after := config.UpsertKey(before, "compat.claude", "hooks", "false")
	if after == before {
		return nil // already hooks = false (or surgically identical)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("init: mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		return fmt.Errorf("init: write %s: %w", path, err)
	}
	label := "~/.grok/config.toml ([compat.claude] hooks=false)"
	if before == "" {
		fmt.Fprintln(out, initLine(true, label))
	} else {
		fmt.Fprintf(out, "  ~ %s\n", label)
	}
	return nil
}

// reconcileHookFile surgically rewrites known-retired satelle commands inside
// an existing hook JSON — exact-command string swap (word-boundary guarded), so
// every other byte of the user-owned file is preserved. Returns the applied
// renames ("old -> new"), empty when nothing was stale. Idempotent.
func reconcileHookFile(path string) ([]string, error) {
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

// reconcileClaudeHooks is the Claude settings path of reconcileHookFile
// (kept for existing tests and call sites).
func reconcileClaudeHooks(path string) ([]string, error) { return reconcileHookFile(path) }

// The reinforcement hook commands (the PATH-prefixed form the scaffolds embed).
// Kept as named constants so the HEAL that adds them to an already-initialized
// repo appends exactly what a fresh init writes; the scaffold constants embed the
// same literals and a test asserts both files carry them (drift-caught).
const (
	promptHookCommand    = "PATH=$HOME/.local/bin:$PATH satelle hook prompt"
	stopcheckHookCommand = "PATH=$HOME/.local/bin:$PATH satelle hook stopcheck"
)

// reinforcementHooks are the event→command entries the heal ensures exist. Marker
// is the stable substring used to detect an already-present entry regardless of a
// repo's PATH prefix.
var reinforcementHooks = []struct{ event, marker, command string }{
	{"UserPromptSubmit", "satelle hook prompt", promptHookCommand},
	{"Stop", "satelle hook stopcheck", stopcheckHookCommand},
}

// ensureReinforcementHooks HEALS an already-initialized repo's hook file: when the
// UserPromptSubmit ('satelle hook prompt') or Stop ('satelle hook stopcheck')
// reinforcement hook is absent, it APPENDS it, preserving every other key. This is
// load-bearing, not polish: without it a repo initialized before the reinforcement
// hooks shipped would NEVER gain them, leaving the single PreToolUse gate as the
// only line of defence — the exact bypass this closes (sty_949e8739). Idempotent:
// when nothing is missing it writes nothing (preserving the file byte-for-byte);
// an unparseable file is left untouched rather than clobbered. Returns the events
// it added.
func ensureReinforcementHooks(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, nil // not JSON we can safely mutate — leave it untouched
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	var added []string
	for _, rh := range reinforcementHooks {
		if hookEventHasMarker(hooks[rh.event], rh.marker) {
			continue
		}
		group := map[string]any{
			"hooks": []any{
				map[string]any{"type": "command", "command": rh.command},
			},
		}
		arr, _ := hooks[rh.event].([]any)
		hooks[rh.event] = append(arr, group)
		added = append(added, rh.event)
	}
	if len(added) == 0 {
		return nil, nil
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return nil, err
	}
	sort.Strings(added)
	return added, nil
}

// hookEventHasMarker reports whether an event's hook groups already contain a
// command carrying the marker substring. Tolerant of the arbitrary nested shape a
// user-owned hook file may hold — any non-conforming node is simply skipped.
func hookEventHasMarker(event any, marker string) bool {
	groups, ok := event.([]any)
	if !ok {
		return false
	}
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hs, ok := gm["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hs {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, marker) {
				return true
			}
		}
	}
	return false
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
		updated, rerr := reconcileHookFile(path)
		if rerr != nil {
			return false, nil, fmt.Errorf("init: reconcile %s: %w", path, rerr)
		}
		healed, herr := ensureReinforcementHooks(path)
		if herr != nil {
			return false, nil, fmt.Errorf("init: reinforce %s: %w", path, herr)
		}
		for _, e := range healed {
			updated = append(updated, "added "+e+" hook")
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

// ensureGrokHooks writes .grok/hooks/satelle.json when absent, and reconciles
// known-retired satelle commands in an existing satelle-owned file. Other files
// under .grok/hooks/ are never touched. Returns created + applied renames.
func ensureGrokHooks(repoRoot string) (bool, []string, error) {
	dir := filepath.Join(repoRoot, ".grok", "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, nil, fmt.Errorf("init: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(grokHooksRel))
	if _, err := os.Stat(path); err == nil {
		updated, rerr := reconcileHookFile(path)
		if rerr != nil {
			return false, nil, fmt.Errorf("init: reconcile %s: %w", path, rerr)
		}
		healed, herr := ensureReinforcementHooks(path)
		if herr != nil {
			return false, nil, fmt.Errorf("init: reinforce %s: %w", path, herr)
		}
		for _, e := range healed {
			updated = append(updated, "added "+e+" hook")
		}
		return false, updated, nil
	} else if !os.IsNotExist(err) {
		return false, nil, fmt.Errorf("init: stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(grokHookSettings), 0o644); err != nil {
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

# Archive retention for CLOSED-story attachment dirs under .satelle/stories.
# Unset (default) = keep everything. The two compose — either triggers pruning.
# A NON-terminal (open/engaged) story's dir is ALWAYS kept regardless. Pruned
# dirs are MOVED to .satelle/backups/stories/ (never deleted in place).
# stories_keep_closed = 50       # keep the N most-recently-updated closed-story dirs
# stories_keep_days = 90         # drop a closed story's dir older than N days

# [review] — opt into reviewer-gated work (off by default). The reviewer rubrics
# ship embedded; enabling enforcement is your choice (needs an agent CLI — see
# 'satelle agent'). gate_create runs the required-structure reviewer on
# 'story/task create', pushing non-conforming drafts back instead of persisting.
# [review]
# gate_create = true

# [gate] — edit-gate exemptions and the single-story process rule. This is the
# ONE table seeded ACTIVE (not commented) below, because it is the source of
# truth for which paths escape the engaged-story edit gate — configuration, not
# a Go rule (the constitution: configuration over code).
# edit_exempt_paths lists repo-root-relative (or absolute) path prefixes whose
# edits are exempt from the engaged-story edit gate. It is the SOLE exemption
# source — the binary does NOT special-case the data dir. ".satelle/" is seeded
# so this repo's authored substrate (workflows/skills/principles/documents/tasks
# /config) stays editable without a release; add a harness authoring dir that
# holds authored markdown rather than product code (e.g. ".claude/"), or drop
# ".satelle/" to require an engaged story even for substrate edits.
# allow_parallel (default false) opts OUT of one-performing-story enforcement: by
# default satelle refuses engaging a second story while another is already in a
# non-terminal engaging state (plan/in_progress/…). Parked (blocked) and terminal
# do not count. Setting allow_parallel = true only turns that blocker OFF — it
# does NOT implement parallel worktrees/merge; a repo that wants true parallel
# must design that into its workflow and then flip this switch.
[gate]
edit_exempt_paths = [".satelle/"]
# edit_exempt_paths = [".satelle/", ".claude/"]
# allow_parallel = false

# substrate_roots — per-kind parent dir for authored markdown. Unset means
# <data_dir>/<kind> (e.g. .satelle/documents). Point a kind elsewhere — even
# outside .satelle/ — to author it at the repo root or another path:
# [substrate_roots]
# documents = "."                # → ./documents
# skills = "."                   # → ./skills

# [sync] — each .satelle area on the local|personal|shared ladder (scoped-sync).
# Unset = local: nothing leaves the machine until an area is opted in.
# personal = this repo's BOUND hosted project (not a dump across every project).
# shared = team catalog eligibility; use 'satelle publish' for the team catalog
# (sync itself does not write to a team workspace). Inspect with 'satelle sync scopes'.
# git remains the source of truth; an area opted in here is MIRRORED to hosted,
# not moved off git — keep authored markdown and satelle.toml committed.
# Areas: documents, workflows, principles, skills, constitution, agents, tasks,
# stories, ledger, executions. Reserved key 'all' blanket-defaults every area
# not set explicitly; a per-area key still overrides it.
# >>> satelle-example: enable sync/hosted (uncomment to opt in)
# [sync]
# all = "personal"               # every area personal unless overridden below
# documents = "personal"         # per-area override wins over 'all'
# [hosted]
# server = "https://hosted.satelle.dev"
# project = "my-project-slug"    # set via: satelle project bind <slug>
# workspace = "team-name"        # per-developer; usually satelle.local.toml
# [vars]
# MODEL_BASE_URL = "https://example.invalid"  # non-secret; secrets → satelle.local.toml
# <<< satelle-example
#
# [hosted] — secret-free hosted-server binding (committed). Tokens live in the
# user credential store, never here. 'satelle login' sets the server URL;
# 'satelle project bind <slug>' writes project. workspace is a per-developer
# choice (prefer satelle.local.toml / 'satelle login --workspace'); a value
# committed here is only a team default the overlay can override.
#
# [vars] — operator KV substituted into agents.toml binding env values via
# ${NAME}. NON-secret vars may live here; SECRETS go in gitignored
# satelle.local.toml (per-key overlay wins). Never pushed with substrate sync.
`

// scaffoldAgentsToml is the documented agents layer a fresh init writes. The
// file is REQUIRED once a repo is initialized (sty_d0d6bb67): a missing or
// unparseable agents.toml refuses to run rather than silently falling back to
// compiled defaults — the configuration executes as defined. A repo may widen
// or rebind transparently — the override is a committed file, the operator's
// choice.
var scaffoldAgentsToml = strings.ReplaceAll(`# agents.toml — the agents layer: how each agent runs (backend + tool grant).
# FULLY DEFINED by init (no hidden coded configuration, sty_892517e7): every
# value below is the ACTIVE default, written out so the operator sees exactly
# what runs. Edit freely. This file is REQUIRED in an initialized repo
# (sty_d0d6bb67): a missing or unparseable agents.toml refuses to run — delete
# it and re-run "satelle init" to reseed the default.
#
# The agent operating model (see the satelle-agent-model principle):
#   - executor  — runs IN-LOOP as the driving session (context, principles,
#     skills via the substrate). Not an isolated process.
#   - reviewer  — an ISOLATED, READ-ONLY sub-process: the rubric rides as its
#     system prompt; it judges, never mutates (the claude preset denylists
#     Write/Edit/NotebookEdit/Bash on top of the read-only grant).
#   - any OTHER top-level [<name>] is an optional named agent, always isolated;
#     a workflow node allocates a step to it via agent=<name>, and entering that
#     state DISPATCHES the step to this binding's command (item on stdin, the
#     node's @skill rubric as the system prompt, tools/model from the binding).
#     A node naming an agent with NO binding here REFUSES the transition. A
#     named agent that MUTATES declares its own full command template + wide
#     grant; its model key pins the step's model ({model} in the template), so
#     per-step model selection is pure configuration.
#   - Per-GATE model without a second binding (sty_19456622): a workflow edge or
#     node may set model="…" (e.g. release->done [agent=reviewer, prompt="@skill:…",
#     model="opus"]). That overrides ONLY the model for that gate/step; the
#     allocated binding stays the source of command template + tools. Absent
#     model= inherits the binding's model. See satelle help agent-dispatch and
#     the satelle-dot-standard principle.
# role= is the binding's declared contract (reviewer | agent); inference from
# the section name is a fallback, not the norm — declare it.
#
# Define process/step agents HERE (a [<name>] binding + an agent=<name> node),
# NEVER in a harness-specific agent dir (e.g. .claude/agents): those are invisible
# to satelle — it cannot see, validate, dispatch, or carry them repo-agnostically —
# and they silently pin the repo to one CLI vendor. See "satelle help agent-dispatch".
#
# THE COMMAND TEMPLATE: a SINGLE token is a built-in PRESET — claude | grok |
# codex | in-loop. "claude" and "grok" expand to a correct, read-only reviewer
# command for that CLI (no need to hand-author its argv); "codex" is not yet
# mapped (write a full command instead); "in-loop" means the driving session
# performs the step (no subprocess). A MULTI-token value is a full command taken
# verbatim. Placeholders — each one argv token: {system} {tools} {model}
# {settings} {payload}. The work-item body is ALWAYS also written on stdin (dual
# delivery). Empty {model}/{settings} drop that flag; empty {payload} does not.
# Claude's default uses stdin only (no -p {payload}) so the prompt is not
# double-fed; argv-first CLIs (grok) opt in with -p {payload}. Prefer a preset;
# write a full command only to customize flags/tools, e.g.
#   command = "grok -p {payload} --system-prompt-override {system} --tools read_file,grep,list_dir --always-approve --output-format plain --max-turns 8 --no-subagents"
#
# PER-BINDING ENV — point a step at an alternate model backend WITHOUT a wrapper
# binary. A binding may set env = { KEY = "value" }; each value may reference the
# [vars] KV via ${NAME}. The resolved env is layered onto the dispatched agent's
# process (binding keys win). Put SECRETS (API keys) under [vars] in the gitignored
# satelle.local.toml — NEVER here (this file is committed). Example — GLM via its
# Anthropic-compatible endpoint, same claude CLI, model reads naturally:
#   [planner]
#   command = "claude -p --append-system-prompt {system} --allowedTools {tools} --model {model}"
#   model   = "glm-4.6"
#   env     = { ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic", ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}" }
# and in satelle.local.toml (gitignored):
#   [vars]
#   GLM_API_KEY = "sk-…"

[executor]
role    = "agent"              # declared contract (agent | reviewer); do not leave inferred
command = "in-loop"            # the orchestrator/driving session itself

[reviewer]
# The FULL command template — transparent and swappable (sty_892517e7, user
# feedback): satelle substitutes {system}/{tools}/{model}/{settings}/{payload}
# (each one argv token); payload is always also on stdin. An empty model drops
# the --model pair. Point this at ANY agent CLI by rewriting the command — or
# replace this whole line with a single-token preset: command = "claude" (or
# "grok"), which expands to a correct read-only reviewer command for that CLI.
role    = "reviewer"           # declared contract; inference is a fallback, not the norm
command = "REVIEWER_COMMAND_TEMPLATE"
tools   = "Read,Grep,Glob"     # read-only grant — widen at your own risk (claude preset only; the grok preset bakes its own grok-named read-only grant)
model   = ""                   # empty inherits the CLI's default; each binding may pin its own (e.g. "sonnet"). A workflow gate/node model="…" overrides this for that gate only without a second binding (sty_19456622).

# A named EXECUTOR agent for isolated mutating steps (e.g. a commit/push step),
# with an explicit full command template and a wide grant:
# [commit-agent]
# role    = "agent"
# command = "claude -p --append-system-prompt {system} --allowedTools {tools}"
# tools   = "Read,Edit,Bash(git:*),Bash(gh:*),Bash(make:*),Bash(satelle:*)"
`, "REVIEWER_COMMAND_TEMPLATE", agentcli.DefaultClaudeCommand)

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
# markdown under .satelle/ stay tracked. Scoped sync is a MIRROR of that tracked
# content, not a second home: deploy/pull land files back in the same tracked
# paths, so git stays the source of truth even when an area is [sync] personal.
# Sync credentials and cursors live under ~/.config/satelle (outside the repo).
.satelle/satelle.db
.satelle/satelle.db-wal
.satelle/satelle.db-shm
.satelle/satelle.local.toml
# the repo-local pinned binary (satelle update --local) is local state, never committed
.satelle/satelle
# the flat operation log (a read-only reviewer's read surface) is local evidence
.satelle/logs/
# mandatory backups from rebase and task archive are local disposal evidence
.satelle/backups/
# <<< satelle (managed) <<<
`

// ensureWorkspaceRegistration registers repoRoot in the machine-local workspace
// registry (gc.Workspace — the connected-repo list for /workspace and multi-serve).
// Non-fatal on every failure path: a machine whose global config is unreadable or
// unwritable still gets a fully initialized repo (sty_3bdbdc38). noWorkspace opts out.
func ensureWorkspaceRegistration(out io.Writer, repoRoot string, noWorkspace bool) {
	if noWorkspace {
		fmt.Fprintln(out, "  = workspace registry (skipped: --no-workspace)")
		return
	}
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		fmt.Fprintf(out, "  ! workspace registry (skipped: resolve path: %v)\n", err)
		return
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		fmt.Fprintf(out, "  ! workspace registry (skipped: %v)\n", err)
		return
	}
	if !gc.Workspace.AddRepo(abs) {
		fmt.Fprintln(out, "  = workspace registry (already registered)")
		return
	}
	if err := config.SaveGlobal(gc); err != nil {
		fmt.Fprintf(out, "  ! workspace registry (registration skipped: %v)\n", err)
		return
	}
	fmt.Fprintf(out, "  + workspace registry (registered %s)\n", abs)
}

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
	"tasks":      "# tasks\n\nAuthored task HEADERS (`tsk_*.md`, `type: task`): re-runnable work-definitions\nthat declare an ACTION and how success is VERIFIED. The file is the source of\ntruth; the DB indexes it. Each RUN is an execution under a per-task folder\n`<tsk_id>/exe_*.md`; create one with `satelle execution create --parent <tsk_id>`.\nDispose of a superseded header with `satelle task archive <tsk_id>`: it marks the\nrecord archived (dropped from the default `task list`, still readable via `task\nget`) and MOVES the header + its executions to `.satelle/backups/tasks/<ts>/<id>/`\n— archive is record disposition, distinct from workflow status.\n",
}

// seedTasks scaffolds .satelle/tasks (dir + README keep-file). It seeds NO
// example task (sty_04ec1fe6): a fresh repo starts with an empty tasks dir —
// tasks are authored substrate the operator writes, and an example header only
// adds noise beside a repo's own set. The reported "+"/"=" tracks the DIRECTORY
// (created vs already present), not the README keep-file, so a repo that already
// has an authored tasks dir reports "=". Returns report lines.
func seedTasks(dataDir string) []string {
	var lines []string
	dir := filepath.Join(dataDir, "tasks")
	dirCreated, derr := ensureDir(dir)
	if derr != nil {
		return lines
	}
	ensureReadme(dir, "tasks")
	lines = append(lines, initLine(dirCreated, config.DefaultDataDir+"/tasks/"))
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
		rel := "principles/" + d.Name + ".md"
		verb, _, err := reconcileEmbeddedFile(dataDir, rel, d.Body)
		if err != nil {
			continue
		}
		if verb != reconcileUnchanged {
			lines = append(lines, reconcileReportLine(verb, rel))
		}
	}
	return lines
}

// materializeTasks writes every embedded default TASK into .satelle/tasks when
// absent (sty_d4360e90). Tasks are authored substrate ingested by SyncTasks (not the
// OKF doc index), so this lands the tsk_*.md header the task reconciler picks up — a
// fresh repo gets the re-runnable substrate-audit and reviewer-objective-audit tasks
// resolving via the task workflow immediately. An existing on-disk file (authored
// or a prior seed) is never clobbered; rebase re-runs this to heal a removed default.
func materializeTasks(dataDir string) []string {
	var lines []string
	dir := filepath.Join(dataDir, "tasks")
	if _, err := ensureDir(dir); err != nil {
		return lines
	}
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "tasks" {
			continue
		}
		rel := "tasks/" + d.Name + ".md"
		verb, _, err := reconcileEmbeddedFile(dataDir, rel, d.Body)
		if err != nil {
			continue
		}
		if verb != reconcileUnchanged {
			lines = append(lines, reconcileReportLine(verb, rel))
		}
	}
	return lines
}

// advisorySkills are embedded rubrics that guide the IN-LOOP agent (or re-runnable
// audits) and are referenced by no workflow — so the default-solution seeding
// (which walks workflow references) never carries them. init seeds each when
// absent, regardless of whether the repo authored its own workflows
// (sty_f4c1bd90). Includes satelle-reviewer-objective-audit (reviewer primary-
// objective audit skill, paired with tsk_reviewer-objective-audit).
var advisorySkills = []string{
	"satelle-workflow-advisor",
	"satelle-reviewer-objective-audit",
}

// materializeAdvisorySkills writes each embedded advisory skill into
// .satelle/skills when absent — never clobbering an authored copy. Report lines.
func materializeAdvisorySkills(dataDir string) []string {
	var lines []string
	for _, name := range advisorySkills {
		body, ok := embeddedDefault("skills", name)
		if !ok {
			continue
		}
		rel := "skills/" + name + ".md"
		verb, _, err := reconcileEmbeddedFile(dataDir, rel, body)
		if err != nil {
			continue
		}
		if verb != reconcileUnchanged {
			lines = append(lines, reconcileReportLine(verb, rel))
		}
	}
	return lines
}

// defaultSolutionWorkflows are the embedded workflows init (and rebase) deploy
// into a repo as EDITABLE substrate — the complete default solution: the minimal
// repo-agnostic BASE lifecycle, the parent/epic container close, the
// task-execution run, and the substrate-only lane for markdown-only changes.
// sty_bf153cbf REVERSES sty_3f9a6124's embedded-only stance:
// the base workflow is now seeded too, so a repo edits its lifecycle FROM it
// rather than authoring one from scratch. This is collision-safe — the
// overlappingCategory guard below skips seeding a default (base included) whose
// applies_to overlaps a category an authored workflow already claims — and the
// embedded copy still backstops the order-zero Get fallback when a repo has no
// workflow on disk at all.
var defaultSolutionWorkflows = []string{
	"satelle-baseline-workflow",
	"satelle-parent-workflow",
	"satelle-task-workflow",
	"satelle-substrate-workflow",
}

// materializeDefaultSolution seeds a repo's .satelle with the embedded default
// solution ADDITIVELY, per file: the generic base/parent/task-execution
// workflows plus every gate skill they reference (sty_a7cbd6dd). Each file is
// seeded only when ABSENT; a same-named authored file is never overwritten or
// modified.
//
// There is no all-or-nothing guard (sty_f6bd6f84): a repo that authored one
// workflow but is missing others (observed: satelle-server had a project
// workflow but no satelle-parent-workflow and was missing a referenced gate
// skill) is HEALED by re-running init — the absent defaults land, the present
// ones and any authored files are untouched, and validation passes because the
// skills a default workflow references are seeded even when that workflow's own
// file is skipped. The ONE guard that survives is routing safety: a default
// workflow whose applies_to overlaps a category an AUTHORED workflow already
// claims is NOT seeded (it would create the same-precedence duplicate the
// reindex consistency check rejects) — its gate skills are still collected and
// seeded. rebase remains the reset path (backup+wipe+redeploy); init is the heal
// path. Returns report lines.
func materializeDefaultSolution(dataDir string) []string {
	wfDir := filepath.Join(dataDir, "workflows")
	var lines []string
	skills := map[string]bool{}
	collectSkills := func(body string) {
		if spec, parsed := wfdot.Parse(body); parsed {
			for _, s := range referencedSkills(spec) {
				skills[s] = true
			}
		}
	}
	// Categories already claimed by an authored (on-disk) workflow. A default
	// whose applies_to overlaps one of these is skipped to avoid a
	// same-precedence routing duplicate.
	claimed := authoredWorkflowCategories(wfDir)
	for _, name := range defaultSolutionWorkflows {
		body, ok := embeddedDefault("workflows", name)
		if !ok {
			continue
		}
		collectSkills(body) // collect refs even if the workflow file is skipped
		rel := "workflows/" + name + ".md"
		p := filepath.Join(wfDir, name+".md")
		// The overlappingCategory guard gates SEEDING an absent default only; an
		// on-disk file is reconciled (converge/diverge) below, never routed here.
		if !fileExists(p) {
			if conflict := overlappingCategory(body, claimed); conflict != "" {
				lines = append(lines, "  = "+config.DefaultDataDir+"/workflows/"+name+".md (applies_to "+conflict+" claimed by an authored workflow — not seeded)")
				continue
			}
		}
		verb, _, err := reconcileEmbeddedFile(dataDir, rel, body)
		if err != nil {
			continue
		}
		lines = append(lines, reconcileReportLine(verb, rel))
	}
	names := make([]string, 0, len(skills))
	for s := range skills {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, name := range names {
		sBody, has := embeddedDefault("skills", name)
		if !has {
			continue // a referenced skill without an embedded rubric stays advisory by design
		}
		rel := "skills/" + name + ".md"
		verb, _, err := reconcileEmbeddedFile(dataDir, rel, sBody)
		if err != nil {
			continue
		}
		if verb != reconcileUnchanged {
			lines = append(lines, reconcileReportLine(verb, rel))
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

// authoredWorkflowCategories returns the set of applies_to categories declared
// by the on-disk (authored) workflows in wfDir. Used to skip seeding a default
// workflow that would duplicate a category an authored workflow already claims
// at the same precedence (sty_f6bd6f84).
func authoredWorkflowCategories(wfDir string) map[string]bool {
	claimed := map[string]bool{}
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		return claimed
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") ||
			strings.EqualFold(e.Name(), "README.md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(wfDir, e.Name()))
		if err != nil {
			continue
		}
		for _, c := range appliesToCategories(string(body)) {
			claimed[c] = true
		}
	}
	return claimed
}

// overlappingCategory returns the first applies_to category the workflow body
// declares that is already present in claimed, or "" when none overlaps.
func overlappingCategory(body string, claimed map[string]bool) string {
	for _, c := range appliesToCategories(body) {
		if claimed[c] {
			return c
		}
	}
	return ""
}

// appliesToCategories extracts the applies_to categories from a workflow's
// frontmatter, handling the inline flow form (applies_to: ["*", "web"]) and the
// block list form (applies_to: then subsequent "- web" lines).
func appliesToCategories(body string) []string {
	lines := strings.Split(body, "\n")
	var out []string
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "applies_to:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, "applies_to:"))
		if rest != "" {
			for _, part := range strings.Split(strings.Trim(rest, "[]"), ",") {
				if c := strings.Trim(strings.TrimSpace(part), `"'`); c != "" {
					out = append(out, c)
				}
			}
			break
		}
		// Block list form: consume following "- item" lines.
		for _, bl := range lines[i+1:] {
			bt := strings.TrimSpace(bl)
			if strings.HasPrefix(bt, "- ") {
				if c := strings.Trim(strings.TrimSpace(bt[2:]), `"'`); c != "" {
					out = append(out, c)
				}
				continue
			}
			if bt == "" {
				continue
			}
			break
		}
		break
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
