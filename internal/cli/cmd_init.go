// `satelle init` — scaffold a repo for satelle, idempotently. It ensures the
// .satelle/ directory, a documented satelle.toml (created if missing, never
// clobbered), the authored-markdown dirs the directory monitor watches, the
// home-keyed runtime database (~/.satelle/<repo-key>/satelle.db), and a managed
// .gitignore block for in-repo local-state only (local.toml, pinned binary).
// Re-running is safe: authored files are preserved; the managed .gitignore
// block is CONVERGED to the current form (sty_87c8a69c). It ends by VALIDATING
// the deployed system (sty_d0d6bb67) and exits non-zero when the deployment
// does not validate green.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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

func init() {
	var configArg string
	var noWorkspace bool
	var harnessFlag string
	cmd := &cobra.Command{
		Use: "init",
		// No `install` alias — it collided with `satelle service install`.
		// Invoking the removed spelling fails closed naming this command.
		Short: "Scaffold this repo for satelle (.satelle/, config, database, authored dirs)",
		Long: `init makes a repo ready for satelle, idempotently. It ensures:

  - the .satelle/ directory,
  - a satelle.toml (created if missing, left intact if present) — every setting
    has a default, so the file ships fully commented and the repo runs zero-config,
  - the authored-markdown dirs (documents, workflows, principles, skills) the
    directory monitor watches and indexes,
  - the per-repo SQLite database on the home-keyed runtime plane
    (~/.satelle/<repo-key>/satelle.db; created and migrated),
  - a managed .gitignore block of RECOMMENDED local-state ignores (local.toml,
    pinned binary); the operator owns .gitignore and whether process is tracked,
    Runtime state is home-keyed and not listed in .gitignore.
    Repos still on the pre-relocation layout: run 'satelle migrate'.
  - process hooks ON DEMAND for coding harnesses in use (epic:minimal-harness-footprint):
    scaffolds only when the repo already has .claude/ or .grok/, or when
    --harness names them (claude,grok). Never from PATH and never a silent
    claude default. An empty repo with no flag gets zero harness scaffolds.
    Use-time lazy install: store-backed verbs detect CLAUDE_CODE_* / GROK_AGENT
    session markers and install the matching scaffold if missing (first session
    of a new harness may run without hooks; they take effect next session).
  - registration of this repo in the local workspace registry (opt out with
    --no-workspace) so 'satelle serve' and the /workspace view see it.

init/install end by VALIDATING the deployed system — the agents layer must load
and every substrate artifact must pass its deterministic structure check — and
exit non-zero when validation fails (broken configuration refuses to run).

Re-running is safe: authored files are preserved; the managed .gitignore block
is rewritten between its markers to the current form (sty_87c8a69c). The report
shows what was added versus already present.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			forced, err := parseHarnessFlag(harnessFlag)
			if err != nil {
				return err
			}
			return runInit(cmd.OutOrStdout(), initRepoRoot(configArg), noWorkspace, forced)
		},
	}
	cmd.Flags().StringVar(&configArg, "config", "", "path to satelle.toml (resolves the repo root; default: walk up from CWD)")
	cmd.Flags().BoolVar(&noWorkspace, "no-workspace", false, "skip registering this repo in the local workspace registry")
	cmd.Flags().StringVar(&harnessFlag, "harness", "", "comma-separated harness scaffolds to install (claude,grok); when empty, only existing .claude/.grok dirs are scaffolded — never PATH")
	register(cmd)
}

// parseHarnessFlag parses --harness claude,grok into a unique ordered list.
func parseHarnessFlag(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		switch p {
		case "claude", "grok":
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		default:
			return nil, fmt.Errorf("init: unknown --harness %q (want claude and/or grok)", p)
		}
	}
	return out, nil
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
// forcedHarness is from --harness (nil/empty = dirs only, no PATH default).
func runInit(out io.Writer, repoRoot string, noWorkspace bool, forcedHarness []string) error {
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
	agentsRel := config.DefaultDataDir + "/" + config.AgentsConfigName
	switch _, statErr := os.Stat(agentsPath); {
	case statErr == nil:
		// Format-migrate an existing agents.toml (harness→command, add role=)
		// instead of reporting "= already present" — mirrors hook heal.
		if raw, rerr := os.ReadFile(agentsPath); rerr != nil {
			fmt.Fprintln(out, initLine(false, agentsRel))
		} else if migrated, notes, merr := config.MigrateAgents(string(raw)); merr != nil {
			fmt.Fprintf(out, "= %s (left intact: %v)\n", agentsRel, merr)
		} else if len(notes) > 0 {
			if werr := os.WriteFile(agentsPath, []byte(migrated), 0o644); werr != nil {
				return fmt.Errorf("init: write migrated %s: %w", agentsPath, werr)
			}
			fmt.Fprintf(out, "~ %s (migrated: %s)\n", agentsRel, strings.Join(notes, "; "))
		} else {
			fmt.Fprintln(out, initLine(false, agentsRel))
		}
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

	// Virtual sparse defaults (sty_29e5a9a5): do NOT materialise unedited embedded
	// workflows/skills/principles/tasks onto disk. List/Count overlay defaults at
	// read time; day-one edit uses `satelle substrate edit`. Authored dirs stay
	// empty scaffolds so an operator has somewhere to put an override.
	cfg, _, _ := config.Load(filepath.Join(repoRoot, config.DefaultDataDir, config.ConfigName))
	bopts := ResolveBackupOpts(cfg)
	rtEarly := cfg.ResolveRuntimeDir(repoRoot)
	if err := os.MkdirAll(rtEarly.Dir, 0o755); err != nil {
		return fmt.Errorf("init: mkdir runtime dir %s: %w", rtEarly.Dir, err)
	}
	bopts.BackupsDir = rtEarly.Dir

	// Heal operator-authored principle frontmatter already on disk (inert scope:,
	// principles:always → session). Does NOT seed missing defaults.
	for _, line := range healPrincipleFrontmatter(dataDir, bopts) {
		fmt.Fprintln(out, line)
	}

	// Converge ON-DISK embedded-owned copies only (restamp/update). Never create
	// a missing default file — virtual defaults cover absence (sty_29e5a9a5).
	for _, line := range convergeOnDiskDefaults(dataDir, bopts) {
		fmt.Fprintln(out, line)
	}

	// 3d. Tasks: dir + README, then seed embedded default task HEADERS onto disk.
	// Unlike workflows/skills/principles (virtual List/Get overlay), tasks are
	// workitem substrate whose coded gates check for an on-disk header file — so
	// the default tasks stay seeded (scoped AC1 carve-out for the tasks plane;
	// sty_29e5a9a5 plan Step 5 fallback). No generic example task.
	for _, line := range seedTasks(dataDir) {
		fmt.Fprintln(out, line)
	}
	for _, line := range materializeTasks(dataDir, bopts) {
		fmt.Fprintln(out, line)
	}

	// 4. The per-repo database on the home-keyed runtime plane (sty_4660bbe1) —
	//    open (creating + migrating) then close, so a fresh repo lands a ready
	//    satelle.db with no first-command surprise. Authored substrate stays under
	//    dataDir; runtime (db/logs/backups/stories) never lands in the repo.
	rt := rtEarly
	dbPath := filepath.Join(rt.Dir, config.DefaultDBName)
	dbExisted := fileExists(dbPath)
	db, derr := store.Open(dbPath)
	if derr != nil {
		return fmt.Errorf("init: open database: %w", derr)
	}
	_ = db.Close()
	_ = config.WriteRepoPathMarker(rt.Dir, repoRoot)
	fmt.Fprintln(out, initLine(!dbExisted, dbPath))

	// 5. .gitignore managed block — ignore in-repo local-state only (local.toml,
	//    pinned binary). Runtime files no longer live under the repo.
	if added, gerr := ensureGitignore(repoRoot); gerr != nil {
		return gerr
	} else {
		fmt.Fprintln(out, initLine(added, ".gitignore (satelle local-state block)"))
	}

	// 6. Process hooks ON DEMAND — Claude (.claude/settings.json) and/or Grok
	//    (.grok/hooks/satelle.json) when forced via --harness or when those dirs
	//    already exist. Never PATH, never a silent claude default.
	if err := ensureProcessHooks(out, repoRoot, forcedHarness); err != nil {
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

	// Stamp the binary version this repo is aligned to (breaking-surface baseline).
	stamped, err := writeDeployedVersion(dataDir)
	if err != nil {
		return fmt.Errorf("init: write deployed.version: %w", err)
	}
	fmt.Fprintln(out, initLine(stamped, config.DefaultDataDir+"/"+deployedVersionName))

	// Point half-upgraded repos at the compose verb (sty_a3915840).
	if note := cfg.LegacyRuntimeNote(repoRoot); note != "" {
		fmt.Fprintf(out, "\nlegacy structure detected — run `satelle migrate` to converge (or `satelle migrate --yes` to apply)\n")
	} else if hasLegacyResidue(dataDir) || gitignoreNeedsConverge(repoRoot) {
		fmt.Fprintf(out, "\nlegacy structure residue remains — run `satelle migrate` to converge (or `satelle migrate --yes` to apply)\n")
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

// failVisibleMarker is the stable substring that identifies a fail-visible
// PreToolUse wrapper (sty_c75c73ed). Lives in the script body under
// .satelle/hooks/ (sty_adfb9862); heal paths also match legacy inline forms.
const failVisibleMarker = "#satelle-failvisible"

// satelleHookScriptRel is the single parameterized fail-visible PreToolUse
// wrapper (epic:minimal-harness-footprint): gate|commitgate × claude|grok.
const satelleHookScriptRel = ".satelle/hooks/satelle-hook.sh"

// legacyHookScriptRel is the pre-consolidation per-harness path (retired).
func legacyHookScriptRel(harness, sub string) string {
	return ".satelle/hooks/pretooluse-" + sub + "-" + harness + ".sh"
}

// hookScriptRel is kept as an alias for callers/tests that still pass harness+sub
// for path expectations of the *parameterized* form (settings commands embed sub+harness).
func hookScriptRel(harness, sub string) string {
	_ = harness
	_ = sub
	return satelleHookScriptRel
}

// renderHookCommand returns a $-free harness command that runs the parameterized
// wrapper with static sub and harness tokens (sty_adfb9862).
func renderHookCommand(harness, sub string) string {
	return "sh " + satelleHookScriptRel + " " + sub + " " + harness
}

// parameterizedHookScriptBody is the single script body for satelle-hook.sh.
// Usage: sh .satelle/hooks/satelle-hook.sh <gate|commitgate> <claude|grok>
//
// The wrapper:
//  1. Resolves satelle from $HOME/.local/bin/satelle → .satelle/satelle → PATH
//  2. Runs `satelle hook <sub>`, captures stdout
//  3. Re-emits satelle's deny JSON when present
//  4. On infra failure: harness-correct static deny JSON (never bare exit-2)
//  5. commitgate: non-mutating bash fails OPEN on infra failure; commit/push closed
func parameterizedHookScriptBody() string {
	claudeInfra := strings.ReplaceAll(infraDenyJSON("claude"), `'`, `'\''`)
	grokInfra := strings.ReplaceAll(infraDenyJSON("grok"), `'`, `'\''`)
	return fmt.Sprintf(`#!/bin/sh
%s
# args: $1=gate|commitgate  $2=claude|grok
sub="$1"
harness="$2"
case "$harness" in
  grok) infra='%s' ;;
  *)    infra='%s' ;;
esac
b=""; for c in "$HOME/.local/bin/satelle" ".satelle/satelle" satelle; do
  if [ -x "$c" ]; then b="$c"; break; fi
  if command -v "$c" >/dev/null 2>&1; then b=$(command -v "$c"); break; fi
done
p=$(cat)
if [ "$sub" = "commitgate" ]; then
  docase(){ case "$p" in *git\ commit*|*git\ push*) printf '%%s\n' "$infra"; exit 2;; *) exit 0;; esac; }
  if [ -z "$b" ]; then docase; fi
  o=$(printf '%%s' "$p" | "$b" hook commitgate 2>/dev/null); code=$?
  if [ -n "$o" ]; then printf '%%s\n' "$o"; fi
  if [ "$code" -eq 0 ]; then exit 0; fi
  if [ -z "$o" ]; then docase; fi
  exit 2
fi
if [ -z "$b" ]; then printf '%%s\n' "$infra"; exit 2; fi
o=$(printf '%%s' "$p" | "$b" hook gate 2>/dev/null); code=$?
if [ -n "$o" ]; then printf '%%s\n' "$o"; fi
if [ "$code" -eq 0 ]; then exit 0; fi
if [ -z "$o" ]; then printf '%%s\n' "$infra"; fi
exit 2
`, failVisibleMarker, grokInfra, claudeInfra)
}

// failVisibleScriptBody returns the canonical wrapper body. The single script
// is harness/sub parameterized; harness+sub args are accepted for call-site
// compatibility (body is identical for all pairs).
func failVisibleScriptBody(harness, sub string) string {
	_ = harness
	_ = sub
	return parameterizedHookScriptBody()
}

// writeHookScripts materialises the single parameterized fail-visible PreToolUse
// wrapper and retires legacy per-harness / kimi scripts (epic:minimal-harness-footprint).
func writeHookScripts(repoRoot string) error {
	dir := filepath.Join(repoRoot, ".satelle", "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("init: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(satelleHookScriptRel))
	body := parameterizedHookScriptBody()
	if prev, err := os.ReadFile(path); err != nil || string(prev) != body {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			return fmt.Errorf("init: write %s: %w", path, err)
		}
	}
	// Retire per-harness and kimi residue (no replacement for kimi).
	for _, harness := range []string{"claude", "grok", "kimi"} {
		for _, sub := range []string{"gate", "commitgate"} {
			_ = os.Remove(filepath.Join(repoRoot, filepath.FromSlash(legacyHookScriptRel(harness, sub))))
		}
	}
	for _, rel := range []string{
		".satelle/hooks/stop-kimi.sh",
		".satelle/bin/kimi-argv.sh",
		".satelle/kimi/config.toml",
	} {
		_ = os.Remove(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	}
	// Drop empty kimi dir if empty.
	_ = os.Remove(filepath.Join(repoRoot, ".satelle", "kimi"))
	return nil
}

// isScriptFormHookCommand reports whether cmd is already the $-free script form
// for this harness/sub (sty_adfb9862).
func isScriptFormHookCommand(cmd, harness, sub string) bool {
	want := renderHookCommand(harness, sub)
	return strings.TrimSpace(cmd) == want
}

// buildClaudeHookSettings returns the .claude/settings.json scaffold bytes.
// PreToolUse gates use the fail-visible wrapper (sty_c75c73ed); SessionStart /
// prompt / stopcheck stay simple (fail open by design).
func buildClaudeHookSettings() []byte {
	doc := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"hooks": []any{
					map[string]any{"type": "command", "command": "satelle reindex"},
					map[string]any{"type": "command", "command": "satelle hook context"},
				}},
			},
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Edit|Write|MultiEdit|NotebookEdit",
					"hooks":   []any{map[string]any{"type": "command", "command": renderHookCommand("claude", "gate")}},
				},
				map[string]any{
					"matcher": "Bash",
					"hooks":   []any{map[string]any{"type": "command", "command": renderHookCommand("claude", "commitgate")}},
				},
			},
			"UserPromptSubmit": []any{
				map[string]any{"hooks": []any{
					map[string]any{"type": "command", "command": promptHookCommand},
				}},
			},
			"Stop": []any{
				map[string]any{"hooks": []any{
					map[string]any{"type": "command", "command": stopcheckHookCommand},
				}},
			},
		},
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return append(b, '\n')
}

// buildGrokHookSettings returns the .grok/hooks/satelle.json scaffold bytes.
// Matchers cover Grok-native tool ids and Claude aliases Grok maps (sty_2fad11b0).
func buildGrokHookSettings() []byte {
	doc := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"hooks": []any{
					map[string]any{"type": "command", "command": "satelle reindex"},
					map[string]any{"type": "command", "command": "satelle hook context"},
				}},
			},
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Edit|Write|MultiEdit|NotebookEdit|search_replace|write",
					"hooks":   []any{map[string]any{"type": "command", "command": renderHookCommand("grok", "gate")}},
				},
				map[string]any{
					"matcher": "Bash|run_terminal_command",
					"hooks":   []any{map[string]any{"type": "command", "command": renderHookCommand("grok", "commitgate")}},
				},
			},
			"UserPromptSubmit": []any{
				map[string]any{"hooks": []any{
					map[string]any{"type": "command", "command": promptHookCommand},
				}},
			},
			"Stop": []any{
				map[string]any{"hooks": []any{
					map[string]any{"type": "command", "command": stopcheckHookCommand},
				}},
			},
		},
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return append(b, '\n')
}

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

// detectProcessHarnesses decides which process-hook scaffolds to apply
// (epic:minimal-harness-footprint). Signals:
//   - forced: explicit --harness list (wins when non-empty)
//   - existing harness dirs in the repo (.claude / .grok)
//
// Never PATH. Never a silent claude default (empty → install nothing).
func detectProcessHarnesses(repoRoot string, forced []string) (claude, grok bool) {
	if len(forced) > 0 {
		for _, h := range forced {
			switch strings.ToLower(strings.TrimSpace(h)) {
			case "claude":
				claude = true
			case "grok":
				grok = true
			}
		}
		return claude, grok
	}
	claude = dirExists(filepath.Join(repoRoot, ".claude"))
	grok = dirExists(filepath.Join(repoRoot, ".grok"))
	return claude, grok
}

// detectSessionHarnesses probes the process environment for harness session
// markers (never PATH). Claude: any CLAUDE_CODE_* env key. Grok: GROK_AGENT
// non-empty (Grok Build sets GROK_AGENT=1 in agent sessions).
func detectSessionHarnesses() (claude, grok bool) {
	return detectSessionHarnessesFrom(os.Environ())
}

// detectSessionHarnessesFrom is the testable core (environ as KEY=VALUE entries).
func detectSessionHarnessesFrom(environ []string) (claude, grok bool) {
	for _, e := range environ {
		key, val, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "CLAUDE_CODE_") {
			claude = true
		}
		if key == "GROK_AGENT" {
			v := strings.TrimSpace(val)
			if v != "" && v != "0" {
				grok = true
			}
		}
	}
	return claude, grok
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// ensureLazySessionHarness installs missing harness scaffolds when a store-backed
// verb runs inside a harness session (session env markers). No-op outside an
// initialized satelle repo, when markers are absent, or when scaffolds exist.
// Best-effort: errors are ignored so a store verb still proceeds (hooks appear next session).
func ensureLazySessionHarness(repoRoot string) {
	dataDir := filepath.Join(repoRoot, config.DefaultDataDir)
	if st, err := os.Stat(dataDir); err != nil || !st.IsDir() {
		return
	}
	wantClaude, wantGrok := detectSessionHarnesses()
	if wantClaude {
		settings := filepath.Join(repoRoot, ".claude", "settings.json")
		if _, err := os.Stat(settings); os.IsNotExist(err) {
			_, _, _, _ = ensureClaudeHooks(repoRoot)
		}
	}
	if wantGrok {
		path := filepath.Join(repoRoot, filepath.FromSlash(grokHooksRel))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			_, _, _, _ = ensureGrokHooks(repoRoot)
			_ = ensureGrokFolderTrust(io.Discard, repoRoot)
			_ = ensureGrokCompatConfig(io.Discard)
		}
	}
}

// ensureProcessHooks scaffolds Claude and/or Grok process hooks per detection
// and reports each outcome on out (same initLine / "~ updated" style as before).
// When Grok is detected it also disables Claude-compat hooks in ~/.grok/config.toml
// so Grok does not double-fire the same satelle gate/commitgate (sty_24b32127).
// forced is from init --harness (nil = dirs only).
func ensureProcessHooks(out io.Writer, repoRoot string, forced []string) error {
	// Always materialise/retire the shared hook script surface (even when no
	// harness settings are installed) so migrate heals legacy scripts.
	if err := writeHookScripts(repoRoot); err != nil {
		return err
	}
	wantClaude, wantGrok := detectProcessHarnesses(repoRoot, forced)
	if !wantClaude && !wantGrok {
		fmt.Fprintln(out, "  · process hooks: none (no .claude/.grok dirs and no --harness; use --harness claude,grok or open a harness session for lazy install)")
		return nil
	}
	if wantClaude {
		added, updated, incomplete, err := ensureClaudeHooks(repoRoot)
		if err != nil {
			return err
		}
		if len(updated) > 0 {
			fmt.Fprintf(out, "  ~ .claude/settings.json (hook updated: %s)\n", strings.Join(updated, "; "))
		} else {
			fmt.Fprintln(out, initLine(added, ".claude/settings.json (process hooks)"))
		}
		if len(incomplete) > 0 {
			fmt.Fprintf(out, "WARN  .claude/settings.json — incomplete satelle hooks after heal: missing %s\n",
				strings.Join(incomplete, ", "))
		}
	}
	if wantGrok {
		added, updated, incomplete, err := ensureGrokHooks(repoRoot)
		if err != nil {
			return err
		}
		if len(updated) > 0 {
			fmt.Fprintf(out, "  ~ %s (hook updated: %s)\n", grokHooksRel, strings.Join(updated, "; "))
		} else {
			fmt.Fprintln(out, initLine(added, grokHooksRel+" (process hooks)"))
		}
		if len(incomplete) > 0 {
			fmt.Fprintf(out, "WARN  %s — incomplete satelle hooks after heal: missing %s\n",
				grokHooksRel, strings.Join(incomplete, ", "))
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

// reinforcementSimpleHooks are event→command entries with a single command and
// no matcher (UserPromptSubmit, Stop). SessionStart and PreToolUse are handled
// separately because they need multi-command groups / harness matchers
// (sty_0699637c).
var reinforcementSimpleHooks = []struct{ event, marker, command string }{
	{"UserPromptSubmit", "satelle hook prompt", promptHookCommand},
	{"Stop", "satelle hook stopcheck", stopcheckHookCommand},
}

// ensureReinforcementHooks HEALS an already-initialized repo's hook file: when a
// satelle hook event is absent, it APPENDS it, preserving every other key.
// Covers UserPromptSubmit + Stop (sty_949e8739) and SessionStart + PreToolUse
// (sty_0699637c). harness is "claude" or "grok" so PreToolUse matchers/scripts
// match the scaffold for that harness. Idempotent; unparseable files are left
// untouched. Returns the events it added.
func ensureReinforcementHooks(path, harness string) ([]string, error) {
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

	// SessionStart: need context (and reindex alongside on a full scaffold add).
	if !hookEventHasMarker(hooks["SessionStart"], "satelle hook context") &&
		!hookEventHasMarker(hooks["SessionStart"], "satelle reindex") {
		group := map[string]any{
			"hooks": []any{
				map[string]any{"type": "command", "command": "satelle reindex"},
				map[string]any{"type": "command", "command": "satelle hook context"},
			},
		}
		arr, _ := hooks["SessionStart"].([]any)
		hooks["SessionStart"] = append(arr, group)
		added = append(added, "SessionStart")
	} else if !hookEventHasMarker(hooks["SessionStart"], "satelle hook context") {
		// reindex present but context missing — append context only.
		group := map[string]any{
			"hooks": []any{
				map[string]any{"type": "command", "command": "satelle hook context"},
			},
		}
		arr, _ := hooks["SessionStart"].([]any)
		hooks["SessionStart"] = append(arr, group)
		added = append(added, "SessionStart")
	}

	// PreToolUse: gate + commitgate with harness matchers / script-file commands.
	if harness == "" {
		harness = "claude"
	}
	gateCmd := renderHookCommand(harness, "gate")
	commitCmd := renderHookCommand(harness, "commitgate")
	gateMatcher := "Edit|Write|MultiEdit|NotebookEdit"
	commitMatcher := "Bash"
	if harness == "grok" {
		gateMatcher = "Edit|Write|MultiEdit|NotebookEdit|search_replace|write"
		commitMatcher = "Bash|run_terminal_command"
	}
	if !hookEventHasMarker(hooks["PreToolUse"], "satelle hook gate") &&
		!hookEventHasMarker(hooks["PreToolUse"], "pretooluse-gate-") &&
		!hookEventHasMarker(hooks["PreToolUse"], "satelle-hook.sh") {
		group := map[string]any{
			"matcher": gateMatcher,
			"hooks":   []any{map[string]any{"type": "command", "command": gateCmd}},
		}
		arr, _ := hooks["PreToolUse"].([]any)
		hooks["PreToolUse"] = append(arr, group)
		added = append(added, "PreToolUse")
	}
	if !hookEventHasMarker(hooks["PreToolUse"], "satelle hook commitgate") &&
		!hookEventHasMarker(hooks["PreToolUse"], "pretooluse-commitgate-") &&
		!hookEventHasMarker(hooks["PreToolUse"], "satelle-hook.sh commitgate") &&
		!hookEventHasMarker(hooks["PreToolUse"], "commitgate "+harness) {
		group := map[string]any{
			"matcher": commitMatcher,
			"hooks":   []any{map[string]any{"type": "command", "command": commitCmd}},
		}
		arr, _ := hooks["PreToolUse"].([]any)
		hooks["PreToolUse"] = append(arr, group)
		// Only report PreToolUse once if both gate+commit were added.
		if len(added) == 0 || added[len(added)-1] != "PreToolUse" {
			added = append(added, "PreToolUse")
		}
	}

	for _, rh := range reinforcementSimpleHooks {
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

// incompleteHookEvents returns event names still missing a satelle marker after
// heal. Empty when the full set is present or the file is unparseable (caller
// may still WARN on unparseable separately).
func incompleteHookEvents(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []string{"(unreadable)"}
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return []string{"(unparseable)"}
	}
	hooks, _ := root["hooks"].(map[string]any)
	need := map[string][]string{
		"SessionStart":     {"satelle hook context", "satelle reindex"},
		"PreToolUse":       {"satelle hook gate", "pretooluse-gate-", "satelle-hook.sh"},
		"UserPromptSubmit": {"satelle hook prompt"},
		"Stop":             {"satelle hook stopcheck"},
	}
	var missing []string
	for _, event := range []string{"SessionStart", "PreToolUse", "UserPromptSubmit", "Stop"} {
		markers := need[event]
		ok := false
		for _, m := range markers {
			if hookEventHasMarker(hooks[event], m) {
				ok = true
				break
			}
		}
		if !ok {
			missing = append(missing, event)
		}
	}
	return missing
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
// Also upgrades legacy inline / bare-exit-2 PreToolUse wrappers to the $-free
// script-file form (sty_c75c73ed, sty_adfb9862). Returns whether it created the
// file and any applied updates.
func ensureClaudeHooks(repoRoot string) (created bool, updated []string, incomplete []string, err error) {
	if err := writeHookScripts(repoRoot); err != nil {
		return false, nil, nil, err
	}
	dir := filepath.Join(repoRoot, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, nil, nil, fmt.Errorf("init: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "settings.json")
	if _, err := os.Stat(path); err == nil {
		updated, incomplete, herr := healExistingHookFile(path, "claude", repoRoot)
		if herr != nil {
			return false, nil, nil, herr
		}
		return false, updated, incomplete, nil
	} else if !os.IsNotExist(err) {
		return false, nil, nil, fmt.Errorf("init: stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, buildClaudeHookSettings(), 0o644); err != nil {
		return false, nil, nil, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil, nil, nil
}

// ensureGrokHooks writes .grok/hooks/satelle.json when absent, and reconciles
// known-retired satelle commands in an existing satelle-owned file. Other files
// under .grok/hooks/ are never touched. Returns created + applied updates.
func ensureGrokHooks(repoRoot string) (created bool, updated []string, incomplete []string, err error) {
	if err := writeHookScripts(repoRoot); err != nil {
		return false, nil, nil, err
	}
	dir := filepath.Join(repoRoot, ".grok", "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, nil, nil, fmt.Errorf("init: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(grokHooksRel))
	if _, err := os.Stat(path); err == nil {
		updated, incomplete, herr := healExistingHookFile(path, "grok", repoRoot)
		if herr != nil {
			return false, nil, nil, herr
		}
		return false, updated, incomplete, nil
	} else if !os.IsNotExist(err) {
		return false, nil, nil, fmt.Errorf("init: stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, buildGrokHookSettings(), 0o644); err != nil {
		return false, nil, nil, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil, nil, nil
}

// healExistingHookFile applies reconcile + reinforcement + fail-visible upgrade
// to an existing hook settings file. Returns the list of human-readable updates
// (empty when nothing changed). incomplete is a WARN note when satelle hooks
// remain incomplete after heal (sty_0699637c).
func healExistingHookFile(path, harness, repoRoot string) (updated []string, incomplete []string, err error) {
	if err := writeHookScripts(repoRoot); err != nil {
		return nil, nil, err
	}
	renames, err := reconcileHookFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("init: reconcile %s: %w", path, err)
	}
	updated = append(updated, renames...)
	healed, err := ensureReinforcementHooks(path, harness)
	if err != nil {
		return nil, nil, fmt.Errorf("init: reinforce %s: %w", path, err)
	}
	for _, e := range healed {
		updated = append(updated, "added "+e+" hook")
	}
	n, err := upgradeFailVisibleHooks(path, harness)
	if err != nil {
		return nil, nil, fmt.Errorf("init: fail-visible upgrade %s: %w", path, err)
	}
	if n > 0 {
		updated = append(updated, fmt.Sprintf("upgraded %d PreToolUse hook(s) to script-file form", n))
	}
	incomplete = incompleteHookEvents(path)
	return updated, incomplete, nil
}

// upgradeFailVisibleHooks rewrites legacy PreToolUse gate/commitgate commands
// to the $-free script-file form (sty_adfb9862). Upgrades:
//   - bare `… hook gate||commitgate || exit 2`
//   - inline `sh -c '…#satelle-failvisible…$c…'` wrappers
//
// Idempotent when already `sh .satelle/hooks/pretooluse-<sub>-<harness>.sh`.
func upgradeFailVisibleHooks(path, harness string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return 0, nil // unparseable — leave untouched
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return 0, nil
	}
	pre, ok := hooks["PreToolUse"].([]any)
	if !ok {
		return 0, nil
	}
	n := 0
	for _, g := range pre {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hs, ok := gm["hooks"].([]any)
		if !ok {
			continue
		}
		for i, h := range hs {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hm["command"].(string)
			sub := legacyHookSub(cmd)
			if sub == "" {
				continue
			}
			if isScriptFormHookCommand(cmd, harness, sub) {
				continue
			}
			hm["command"] = renderHookCommand(harness, sub)
			hs[i] = hm
			n++
		}
		gm["hooks"] = hs
	}
	if n == 0 {
		return 0, nil
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return 0, err
	}
	return n, nil
}

// legacyHookSub returns "gate" or "commitgate" when cmd is a satelle PreToolUse
// gate command that is not yet (or not only) an unrelated shell snippet.
func legacyHookSub(cmd string) string {
	switch {
	case strings.Contains(cmd, "hook commitgate") || strings.Contains(cmd, "pretooluse-commitgate-") ||
		(strings.Contains(cmd, "satelle-hook.sh") && strings.Contains(cmd, "commitgate")):
		return "commitgate"
	case strings.Contains(cmd, "hook gate") || strings.Contains(cmd, "pretooluse-gate-") ||
		(strings.Contains(cmd, "satelle-hook.sh") && strings.Contains(cmd, " gate ")):
		return "gate"
	default:
		return ""
	}
}

// scaffoldToml is the documented config a fresh init writes. Every key is
// commented because each has a default — the repo runs zero-config until a knob
// is uncommented.
const scaffoldToml = `# satelle.toml — per-repo config (committed, secret-free). Every setting has a
# default, so this file may stay fully commented; uncomment a key to override.
# Per-user overrides go in satelle.local.toml beside this file (gitignored).

# data_dir = ".satelle"          # authored substrate home under the repo (default)
# db = ""                        # override DB path; default is home-keyed
#                                # ~/.satelle/<repo-key>/satelle.db (sty_4660bbe1).
#                                # An explicit path wins; otherwise runtime state
#                                # (db, logs, backups, stories cache) lives outside
#                                # the repo. Migrate a legacy in-repo DB with
#                                # 'satelle runtime migrate'.
# web_port = 8787                # 'satelle serve' listen port (default)
# log_level = "info"             # debug | info | warn | error (default info)
# logs_max_size_kb = 5120        # roll a runtime logs/ file past this size (default 5 MiB)
# logs_max_files = 7             # keep at most this many rotated log files (default 7)

# Archive retention for CLOSED-story attachment dirs under the runtime stories/
# cache (~/.satelle/<repo-key>/stories). Unset (default) = keep everything.
# The two compose — either triggers pruning. A NON-terminal (open/engaged)
# story's dir is ALWAYS kept. Pruned dirs are MOVED to runtime backups/stories/
# (never deleted in place).
# stories_keep_closed = 50       # keep the N most-recently-updated closed-story dirs
# stories_keep_days = 90         # drop a closed story's dir older than N days

# [review] — create gate. Default ON (sty_83782ffb): creation is where
# misclassification is cheapest to catch. gate_create runs the deterministic
# structure check plus the workflow's create_review skill (embedded default:
# satelle-story-create-review — content, alignment, and classification). Needs
# an agent CLI for the LLM half (see 'satelle agent'). Set false to opt out.
[review]
gate_create = true

# [tags.vocabulary] — controlled tag namespaces (optional). A tag in a listed
# namespace must use a declared value (case-insensitive match; stored in the
# casing declared here). Namespaces absent from the table stay free-form.
# Unset = every tag free-form (zero-config). Declare per-repo; never compiled
# into the binary (sty_034d843c).
# [tags.vocabulary]
# surface = ["ui", "cli"]

# [gate] — edit-gate exemptions and the single-story process rule. This is the
# ONE table seeded ACTIVE (not commented) below, because it is the source of
# truth for which paths escape the engaged-story edit gate — configuration, not
# a Go rule (the constitution: configuration over code).
# edit_exempt_paths lists repo-root-relative (or absolute) path prefixes whose
# edits are exempt from the engaged-story edit gate. It is the SOLE exemption
# source — the binary does NOT special-case the data dir or any managed path.
# ".satelle/" is seeded so this repo's authored substrate (workflows/skills/
# principles/documents/tasks/config) stays editable without a release.
# ".gitignore" is seeded because init/migrate write its managed block — satelle-
# managed output the operator did not author (sty_f115e6bf). Without that
# exemption, the binary's own convergence trips the stop hook and there is no
# clean lane to commit it. Add a harness authoring dir that holds authored
# markdown rather than product code (e.g. ".claude/"), or drop either default
# to require an engaged story for those paths. An explicitly empty list is a
# deliberate opt-out (everything gated). Repos that predate .gitignore: run
# satelle migrate --yes to append it without clobbering operator additions.
# allow_parallel (default false) opts OUT of one-performing-story enforcement: by
# default satelle refuses engaging a second story while another is already in a
# non-terminal engaging state (plan/in_progress/…). Parked (blocked) and terminal
# do not count. Setting allow_parallel = true only turns that blocker OFF — it
# does NOT implement parallel worktrees/merge; a repo that wants true parallel
# must design that into its workflow and then flip this switch.
# allow_outside_tree_edits (default false) opts INTO Bash/Edit mutations whose
# targets land in another git working tree (sty_a8454d10 / sty_aadd4d6c).
# Non-repo paths (temp, scratchpads) are never fenced. Leave false unless this
# install deliberately spans multiple repos from one session — create stories
# cross-repo stays allowed either way; progressing/mutating another tree does not.
[gate]
edit_exempt_paths = [".satelle/", ".gitignore"]
# edit_exempt_paths = [".satelle/", ".gitignore", ".claude/"]
# allow_parallel = false
# allow_outside_tree_edits = false

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
# Continuity: local = your disk; personal = push backup + sync-down rehydrate for
# the bound project. Whether .satelle process is git-tracked is the operator's
# choice — satelle does not require it. Git is for the application repo.
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
# [backup] — pre-mutation substrate backup policy (sty_873a5380, sty_84f14ace).
# Local copies under .satelle/backups/ always run before init/restore/rebase
# overwrite an existing file. local_only suppresses the advisory about the
# online option. hosted = true opts into pushing pre-images into the bound
# project's personal documents partition (path backups/<rel>) — default off,
# because that partition is listed by documents pull and backups/ is a restore
# exclusion (auto-push permanently wedged sync; sty_84f14ace).
# [backup]
# local_only = true
# hosted = true

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
#     system prompt; it judges, never mutates (the default claude template
#     denylists Write/Edit/NotebookEdit/Bash on top of the read-only grant).
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
# TRANSPORT (epic:agent-dispatch-transport): interface = "command" | "acp".
# Omit interface (or set "command") for a FULL multi-token command template — any
# CLI (Claude Code, grok -p, wrappers). Optional interface = "acp" for an
# ACP-capable spawn only (e.g. command = "grok agent stdio"); system/payload ride
# the protocol, not {placeholders}. Claude has no ACP — keep it on command.
# Defaults in this scaffold stay command; do not flip [reviewer] to acp here.
#
# THE COMMAND TEMPLATE (interface=command): an isolated binding requires a FULL
# multi-token command (binary + argv). The only bare single-token value is
# "in-loop" (the driving session performs the step — no subprocess). An
# empty/omitted command on [reviewer] inherits the full default claude template.
# Bare tokens like "claude" / "grok" / "codex" are rejected by satelle agent
# validate — run satelle init to expand a legacy bare preset, or write the full
# argv yourself. Placeholders — each one argv token: {system} {tools} {model}
# {settings} {payload}. The work-item body is ALWAYS also written on stdin (dual
# delivery). Empty {model}/{settings} drop that flag; empty {payload} does not.
# Claude's default uses stdin only (no -p {payload}) so the prompt is not
# double-fed; argv-first CLIs (grok) opt in with -p {payload}. Example:
#   command = "grok -p {payload} --system-prompt-override {system} --tools read_file,grep,list_dir --always-approve --output-format plain --max-turns 8 --no-subagents"
# Optional ACP example (not enabled by default):
#   # interface = "acp"
#   # command   = "grok agent stdio"
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
# The FULL command template — transparent and swappable (sty_892517e7): satelle
# substitutes {system}/{tools}/{model}/{settings}/{payload} (each one argv token);
# payload is always also on stdin. An empty model drops the --model pair. Point
# this at ANY agent CLI by rewriting the multi-token command. Bare single-token
# presets (claude/grok/codex) are not accepted — the real argv must be literal.
role    = "reviewer"           # declared contract; inference is a fallback, not the norm
command = "REVIEWER_COMMAND_TEMPLATE"
tools   = "Read,Grep,Glob"     # read-only grant — widen at your own risk (claude template default; a grok full template bakes its own grok-named read-only grant)
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

// gitignoreMarker opens the managed block ensureGitignore maintains.
const gitignoreMarker = "# >>> satelle (managed) >>>"

// gitignoreMarkerEnd closes the managed block. Content between the markers is
// owned by satelle and rewritten on every init/migrate (sty_87c8a69c).
const gitignoreMarkerEnd = "# <<< satelle (managed) <<<"

// gitignoreBlock is the RECOMMENDED ignore set satelle init writes. The
// operator owns .gitignore; satelle does not require process substrate or the
// local DB to be git-tracked. Entries below are local-state defaults only.
// Runtime state (satelle.db, logs, backups, stories cache) lives under
// ~/.satelle/<repo-key>/ — outside the repo — so it is not listed here.
const gitignoreBlock = gitignoreMarker + `
# RECOMMENDED defaults — the operator owns .gitignore. satelle does not require
# satelle.toml or authored markdown under .satelle/ to be committed.
# Continuity is local disk by default; with [sync] <area> = personal the bound
# hosted project backs that area up and can rehydrate it (push = backup;
# documents pull / config deploy = sync down). Git is not the recovery path.
# Runtime state (satelle.db, logs, backups, stories cache) lives under
# ~/.satelle/<repo-key>/ — outside the repo — so it is not listed here.
# Sync credentials and cursors live under ~/.config/satelle.
.satelle/satelle.local.toml
# the repo-local pinned binary (satelle update --local) is local state, never committed
.satelle/satelle
` + gitignoreMarkerEnd + "\n"

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

// ensureGitignore writes or converges the managed block in the repo's
// .gitignore (sty_87c8a69c / sty_a3915840):
//   - no file → create with the current block
//   - no markers → append the current block
//   - markers present → rewrite content BETWEEN them to the current form;
//     text outside the markers is preserved
//
// Returns whether the file content changed.
func ensureGitignore(repoRoot string) (bool, error) {
	path := filepath.Join(repoRoot, ".gitignore")
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		next, changed := convergeGitignore(string(raw))
		if !changed {
			return false, nil
		}
		if werr := os.WriteFile(path, []byte(next), 0o644); werr != nil {
			return false, fmt.Errorf("init: write %s: %w", path, werr)
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

// convergeGitignore returns the file body with the managed block current.
// Pure: no IO. changed is false when next equals in (already converged).
func convergeGitignore(in string) (next string, changed bool) {
	// Normalize line endings for comparison only; preserve file ending style lightly.
	if !strings.Contains(in, gitignoreMarker) {
		body := in
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if body != "" {
			body += "\n"
		}
		return body + gitignoreBlock, true
	}
	start := strings.Index(in, gitignoreMarker)
	// Find end marker after start; if missing, replace from start to EOF.
	rest := in[start:]
	endRel := strings.Index(rest, gitignoreMarkerEnd)
	var before, after string
	before = in[:start]
	if endRel < 0 {
		after = ""
	} else {
		afterStart := start + endRel + len(gitignoreMarkerEnd)
		// Consume a single trailing newline on the end marker if present.
		if afterStart < len(in) && in[afterStart] == '\n' {
			afterStart++
		}
		after = in[afterStart:]
	}
	// Keep a single newline before the managed block when there is prior content.
	if before != "" && !strings.HasSuffix(before, "\n") {
		before += "\n"
	}
	out := before + gitignoreBlock
	if after != "" {
		if !strings.HasPrefix(after, "\n") && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += after
	}
	if out == in {
		return in, false
	}
	return out, true
}

// gitignoreNeedsConverge reports whether .gitignore's managed block differs
// from the current form (or is missing). Used by migrate plan reporting.
func gitignoreNeedsConverge(repoRoot string) bool {
	path := filepath.Join(repoRoot, ".gitignore")
	raw, err := os.ReadFile(path)
	if err != nil {
		return true // missing → will create
	}
	_, changed := convergeGitignore(string(raw))
	return changed
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

// convergeOnDiskDefaults restamps/updates embedded-owned files that ALREADY
// exist under dataDir. It never creates a missing default (virtual defaults
// cover absence — sty_29e5a9a5). Used by init so a re-run heals drifted stamps
// without re-seeding 30+ markdown copies.
func convergeOnDiskDefaults(dataDir string, backupOpts ...BackupOpts) []string {
	var bopts BackupOpts
	if len(backupOpts) > 0 {
		bopts = backupOpts[0]
	}
	var backupAdvisoryOnce bool
	var lines []string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "tasks" {
			continue // SyncTasks owns virtual task ingest
		}
		rel := d.Kind + "/" + d.Name + ".md"
		if !fileExists(filepath.Join(dataDir, filepath.FromSlash(rel))) {
			continue
		}
		verb, bres, err := reconcileEmbeddedFile(dataDir, rel, d.Body, bopts)
		if err != nil {
			continue
		}
		switch verb {
		case reconcileUnchanged, reconcileCreated:
			// silence
		default:
			lines = append(lines, reconcileReportLine(verb, rel))
		}
		if bres.Notice != "" && (!backupAdvisoryOnce || !strings.Contains(bres.Notice, "online/personal")) {
			lines = append(lines, "  i "+bres.Notice)
			if strings.Contains(bres.Notice, "online/personal") {
				backupAdvisoryOnce = true
			}
		}
	}
	return lines
}

// materializePrinciples writes every embedded default PRINCIPLE into
// .satelle/principles when absent, so the operating principles — including the
// principles:session session set — live on disk and are LISTED for SessionStart
// injection + doc-list discovery (the runtime index no longer overlays embedded
// docs, sty_94da9ac9). Embedded principles remain the canonical seed; an existing
// on-disk file is never clobbered.
//
// Deprecated for init after sty_29e5a9a5 (virtual defaults); retained for rebase.
func materializePrinciples(dataDir string, backupOpts ...BackupOpts) []string {
	var bopts BackupOpts
	if len(backupOpts) > 0 {
		bopts = backupOpts[0]
	}
	var backupAdvisoryOnce bool
	var lines []string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "principles" {
			continue
		}
		rel := "principles/" + d.Name + ".md"
		verb, bres, err := reconcileEmbeddedFile(dataDir, rel, d.Body, bopts)
		if err != nil {
			continue
		}
		if verb != reconcileUnchanged {
			lines = append(lines, reconcileReportLine(verb, rel))
		}
		if bres.Notice != "" && (!backupAdvisoryOnce || !strings.Contains(bres.Notice, "online/personal")) {
			lines = append(lines, "  i "+bres.Notice)
			if strings.Contains(bres.Notice, "online/personal") {
				backupAdvisoryOnce = true
			}
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
func materializeTasks(dataDir string, backupOpts ...BackupOpts) []string {
	var bopts BackupOpts
	if len(backupOpts) > 0 {
		bopts = backupOpts[0]
	}
	var backupAdvisoryOnce bool
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
		verb, bres, err := reconcileEmbeddedFile(dataDir, rel, d.Body, bopts)
		if err != nil {
			continue
		}
		if verb != reconcileUnchanged {
			lines = append(lines, reconcileReportLine(verb, rel))
		}
		if bres.Notice != "" && (!backupAdvisoryOnce || !strings.Contains(bres.Notice, "online/personal")) {
			lines = append(lines, "  i "+bres.Notice)
			if strings.Contains(bres.Notice, "online/personal") {
				backupAdvisoryOnce = true
			}
		}
	}
	return lines
}

// advisorySkills are embedded rubrics that guide the IN-LOOP agent (or re-runnable
// audits) and are referenced by no workflow — so the default-solution seeding
// (which walks workflow references) never carries them. init seeds each when
// absent, regardless of whether the repo authored its own workflows
// (sty_f4c1bd90). Includes satelle-reviewer-objective-audit (reviewer primary-
// objective audit skill, paired with tsk_reviewer-objective-audit) and
// satelle-context-audit (paired with tsk_context-audit).
var advisorySkills = []string{
	"satelle-workflow-advisor",
	"satelle-reviewer-objective-audit",
	"satelle-context-audit",
}

// materializeAdvisorySkills writes each embedded advisory skill into
// .satelle/skills when absent — never clobbering an authored copy. Report lines.
func materializeAdvisorySkills(dataDir string, backupOpts ...BackupOpts) []string {
	var bopts BackupOpts
	if len(backupOpts) > 0 {
		bopts = backupOpts[0]
	}
	var backupAdvisoryOnce bool
	var lines []string
	for _, name := range advisorySkills {
		body, ok := embeddedDefault("skills", name)
		if !ok {
			continue
		}
		rel := "skills/" + name + ".md"
		verb, bres, err := reconcileEmbeddedFile(dataDir, rel, body, bopts)
		if err != nil {
			continue
		}
		if verb != reconcileUnchanged {
			lines = append(lines, reconcileReportLine(verb, rel))
		}
		if bres.Notice != "" && (!backupAdvisoryOnce || !strings.Contains(bres.Notice, "online/personal")) {
			lines = append(lines, "  i "+bres.Notice)
			if strings.Contains(bres.Notice, "online/personal") {
				backupAdvisoryOnce = true
			}
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
func materializeDefaultSolution(dataDir string, backupOpts ...BackupOpts) []string {
	var bopts BackupOpts
	if len(backupOpts) > 0 {
		bopts = backupOpts[0]
	}
	var backupAdvisoryOnce bool
	wfDir := filepath.Join(dataDir, "workflows")
	var lines []string
	skills := map[string]bool{}
	collectSkills := func(body string) {
		if spec, parsed := wfdot.Parse(body); parsed {
			for _, s := range referencedSkills(spec) {
				skills[s] = true
			}
		}
		// create_review is workflow frontmatter (not a DOT edge); seed it too
		// so the create gate travels with the default solution (sty_83782ffb).
		if cr := workflowFrontmatterScalar(body, "create_review"); cr != "" {
			skills[cr] = true
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
		verb, bres, err := reconcileEmbeddedFile(dataDir, rel, body, bopts)
		if err != nil {
			continue
		}
		lines = append(lines, reconcileReportLine(verb, rel))
		if bres.Notice != "" && (!backupAdvisoryOnce || !strings.Contains(bres.Notice, "online/personal")) {
			lines = append(lines, "  i "+bres.Notice)
			if strings.Contains(bres.Notice, "online/personal") {
				backupAdvisoryOnce = true
			}
		}
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
		verb, bres, err := reconcileEmbeddedFile(dataDir, rel, sBody, bopts)
		if err != nil {
			continue
		}
		if verb != reconcileUnchanged {
			lines = append(lines, reconcileReportLine(verb, rel))
		}
		if bres.Notice != "" && (!backupAdvisoryOnce || !strings.Contains(bres.Notice, "online/personal")) {
			lines = append(lines, "  i "+bres.Notice)
			if strings.Contains(bres.Notice, "online/personal") {
				backupAdvisoryOnce = true
			}
		}
	}
	return lines
}

// workflowFrontmatterScalar returns a single-line YAML scalar for key from the
// leading markdown frontmatter of body, or "" when absent/unparseable. Used for
// create_review (and similar) keys that are not DOT edge attributes.
func workflowFrontmatterScalar(body, key string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	prefix := key + ":"
	for i := 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "---" {
			return ""
		}
		if t == prefix || strings.HasPrefix(t, prefix+" ") {
			return strings.TrimSpace(strings.TrimPrefix(t, prefix))
		}
	}
	return ""
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
