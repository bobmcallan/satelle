// `satelle hook` carries the Claude Code hook handlers. Currently one:
// `satelle hook context` — the SessionStart always-context injector
// (sty_e3922598).
//
// At session start it fetches every `principles:session`-flagged authored doc
// and injects their bodies as session context, followed by the standing "pull
// the rest on demand" instruction. This keeps the minimal SESSION set (the
// operating principle) in front of the agent without auto-injecting an unbounded
// list — the bodies are bounded by a byte ceiling, and an overflow is reported on
// stderr (never silently dropped). It FAILS OPEN: an unconfigured repo or any read
// error injects nothing and never blocks the session. This is the mechanism that
// makes the `principles:session` residency marker live: residency is TWO tiers —
// session (carries the marker → injected every session) and on-demand (the
// default, no marker → pulled only when a skill or workflow references it).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sessionTag is the residency marker: a doc carrying it in its frontmatter tags
// is part of the SESSION set injected every session start. A doc WITHOUT it is
// on-demand (the default) — resolvable substrate pulled only when a skill or
// workflow references it, never auto-injected.
const sessionTag = "principles:session"

// alwaysContextCeiling bounds the total injected always-content. The resident
// set is meant to be small (a handful of principle-sized docs); this is the
// backstop that stops a mis-tagged large doc from blowing the context budget the
// whole model is meant to protect. Sized to hold satelle's order-zero principles
// (constitution, repo-agnostic, agent-goals, done-is-last) with headroom.
const alwaysContextCeiling = 16384

// alwaysIndexInstruction is the standing "pull, don't preload" directive
// appended to every injection — the pivot of the session-context model: the
// session set is pushed, everything else is on-demand. On-demand substrate is
// pulled when a skill or workflow REFERENCES it (recall on reference), not by
// browsing — `satelle doc list` is the quality-management / authoring browse
// surface, not the session-context path.
const alwaysIndexInstruction = "The session set above is everything auto-loaded. Other principles and documents are on-demand: pull one with `satelle doc get <kind> <name>` when a skill or workflow references it — do not preload. (`satelle doc list` is the quality-management browse surface for authoring/curating substrate, not a step in the work loop.)"

func init() {
	hook := &cobra.Command{
		Use:   "hook",
		Short: "Claude Code hook handlers (SessionStart context injection, …)",
	}
	context := &cobra.Command{
		Use:   "context",
		Short: "SessionStart session-context injector — inject principles:session docs + the on-demand pointer",
		Long: `context is the SessionStart handler. It injects every principles:session
authored doc (the SESSION set — the minimal operating principle) as session
context, then the standing instruction that the rest is on-demand: pulled via
` + "`satelle doc get`" + ` only when a skill or workflow references it. Bounded by a
byte ceiling (overflow noted on stderr); fails open so it never blocks a session.`,
		Args: cobra.NoArgs,
		// No store annotation: this command opens the store itself, defensively,
		// so any bootstrap failure fails OPEN (exit 0, inject nothing) rather than
		// blocking the session.
		RunE: func(cmd *cobra.Command, args []string) error {
			// Drain stdin (the hook event JSON) — tolerated and ignored; the repo
			// is resolved from the working directory like every other command.
			_, _ = io.ReadAll(cmd.InOrStdin())
			return runHookContext(cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	gate := &cobra.Command{
		Use:   "gate",
		Short: "PreToolUse edit gate — block code edits unless a story is engaged",
		Long: `gate is the PreToolUse handler for Edit|Write|MultiEdit|NotebookEdit. It
exits non-zero (the wiring turns that into a block with '|| exit 2') unless a
story is ENGAGED — in one of the active workflow's non-terminal engaging states
(e.g. plan, in_progress, integration, release) — so the agent works under a
tracked story. The "engaged" policy is authored substrate — it reads the
workflow's DOT shape markers (Mdiamond=start, Msquare=terminal) rather than
hardcoding state names, so configuration drives the decision (sty_f3d5d4b8).

Edits are NEVER gated when: the target is OUTSIDE the repo (session scratch); it
is authored SUBSTRATE under the data dir (.satelle/ by default — workflows,
skills, principles, documents, tasks, and config); or it falls under a configured
[gate] edit_exempt_paths prefix. Authored substrate is the source of truth,
edited freely without a binary release (the constitution and
satelle-generated-readonly); generated views under it stay protected by their
0o444 file mode. A repo may add its own authored dirs to [gate] edit_exempt_paths
(repo-root-relative prefixes, default empty) — e.g. a harness authoring dir like
.claude/ that holds authored skills, not product code. Only in-repo CODE requires
an engaged story. A data-dir substrate edit with NO engaged story is still
ALLOWED but emits a nudge toward the substrate workflow (sty_f5f351d1): the only
model-visible channel on an allowed edit is the PreToolUse additionalContext, so
the nudge rides that (a bare stderr line is transcript-only on exit 0);
edit_exempt_paths opt-in dirs stay silent.

Fails closed: a store open error, listing error, unresolvable workflow, or
non-DOT workflow body blocks the edit with a clear error message rather than
silently allowing it on a broken deployment (sty_f3d5d4b8).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			// storyEngaged is resolved once and reused for the substrate-nudge decision
			// (so only one store open + list runs per invocation). Its error surfaces
			// as a gate rejection (sty_f3d5d4b8).
			engaged, engErr := storyEngaged()
			if p := filePathFromEvent(raw); p != "" {
				// An edit whose target is OUTSIDE the repo (e.g. the session
				// scratchpad under /tmp) is untracked scratch, not project code —
				// never gated.
				if !withinRepoTarget(p) {
					return nil
				}
				// Authored SUBSTRATE under the data dir (.satelle/ by default) is the
				// source of truth, "edited without a binary release" — the
				// constitution and satelle-generated-readonly say to edit it freely.
				// So workflows, skills, principles, documents, tasks, and config
				// (agents.toml/satelle.toml/constitution.md) are NOT gated; generated
				// views under it stay protected by their 0o444 file mode. A repo may
				// exempt additional authored dirs via [gate] edit_exempt_paths — a
				// harness authoring dir like .claude/ holds authored skills, not
				// product code (sty_103af456, sty_41416b76). Only in-repo CODE requires
				// an engaged story. Substrate stays ALLOWED, but a data-dir edit with
				// no engaged story earns a model-visible nudge toward the substrate
				// workflow (sty_f5f351d1) — edit_exempt_paths opt-in dirs (.claude/)
				// stay silent, a deliberate repo choice. A storyEngaged error suppresses
				// the nudge (no false advisory when the store is broken).
				if exemptTarget(p) {
					if engErr == nil && shouldWarnSubstrate(dataDirTarget(p), engaged) {
						emitSubstrateNoStoryNudge(cmd.OutOrStdout(), cmd.ErrOrStderr())
					}
					return nil
				}
			}
			// Engagement-error surface: a broken deployment blocks the edit with a
			// clear message rather than silently allowing it (sty_f3d5d4b8).
			if engErr != nil {
				return fmt.Errorf("satelle: %w", engErr)
			}
			if engaged {
				return nil
			}
			return fmt.Errorf("satelle: no engaged story — create or engage one before editing code " +
				"(satelle story create …, then satelle story set <id> --status plan). " +
				"The workflow requires work to proceed under a tracked story.")
		},
	}

	commitgate := &cobra.Command{
		Use:   "commitgate",
		Short: "PreToolUse Bash gate — block git commit/push unless a story is engaged",
		Long: `commitgate is the PreToolUse handler for Bash. It allows any command that is
not a git commit/push; for a commit/push it exits non-zero (blocked via
'|| exit 2') unless a story is engaged, so changes are committed under a tracked
story. Fails closed: a store/listing/workflow-resolution error blocks the commit
with a clear message rather than silently allowing it (sty_f3d5d4b8).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			if !isGitCommitOrPush(bashCommandFromEvent(raw)) {
				return nil // not a commit/push — allow
			}
			engaged, err := storyEngaged()
			if err != nil {
				return fmt.Errorf("satelle: %w", err)
			}
			if engaged {
				return nil
			}
			return fmt.Errorf("satelle: refusing to commit/push with no engaged story — " +
				"engage a story (satelle story set <id> --status plan) so the change is tracked through the workflow.")
		},
	}

	hook.AddCommand(context, gate, commitgate)
	register(hook)
}

// storyEngaged reports whether any work item — story OR task — sits in a
// non-terminal engaging state of ITS OWN governing workflow (the authored definition
// of "engaged work"). Resolving per story, not against one arbitrary "primary"
// workflow, is what makes engagement correct rather than accidental: a repo that
// runs a category-specific workflow (or opts a step into a dispatched coder) is
// judged by the workflow that actually governs the item (sty_f5bd176f). Fails
// CLOSED: a store open error, listing error, unresolvable workflow, or non-DOT
// workflow body returns (false, error) so the hook blocks rather than silently
// allowing edits on a broken deployment (sty_f3d5d4b8).
func storyEngaged() (bool, error) {
	a, err := app.Open()
	if err != nil {
		return false, fmt.Errorf("cannot determine engagement (store open failed: %w) — fix config and retry", err)
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	// The full workflow set — fail closed on error (not a silent fallback).
	wfs, err := a.Store.DocIndex.List(ctx, "workflows")
	if err != nil {
		return false, fmt.Errorf("cannot determine engagement (workflow list failed: %w) — fix config and retry", err)
	}
	// All kinds — a task engaged in a performing state counts exactly like a story,
	// so the commit/edit gates treat engaged tasks the same (sty_3ed91a58).
	items, err := a.Store.Stories.List(ctx, workitem.ListFilter{})
	if err != nil {
		return false, fmt.Errorf("cannot determine engagement (story list failed: %w) — fix config and retry", err)
	}
	return anyEngaged(items, wfs)
}

// anyEngaged reports whether any work item sits in a non-terminal engaging state
// of the workflow that governs IT — the stamped workflow, else its category-selected
// one (agentstep.GoverningWorkflow). A "non-terminal engaging state" is one that
// is neither start (shape=Mdiamond) nor terminal (shape=Msquare) nor cancel/exception
// (agent=reviewer with no outgoing edges) — read from the authored DOT's shape
// markers, not hardcoded (sty_f3d5d4b8).
//
// Returns (engaged, err): err is non-nil when an item has NO resolving workflow
// or the workflow does not yield a DOT spec — fail-closed, not a silent allow. Pure
// core, split for testing.
func anyEngaged(items []workitem.Item, wfs []docindex.Doc) (bool, error) {
	engagingCache := map[string]map[string]bool{} // workflow name → engaging-state set
	for _, it := range items {
		wf, ok := agentstep.GoverningWorkflow(wfs, it)
		if !ok {
			return false, fmt.Errorf("item %s has no resolving workflow — cannot determine engagement", it.ID)
		}
		engaging, cached := engagingCache[wf.Name]
		if !cached {
			spec, dotOK := wfdot.Parse(wf.Body)
			if !dotOK {
				return false, fmt.Errorf("workflow %s has no DOT spec — cannot determine engagement", wf.Name)
			}
			engaging = map[string]bool{}
			for _, s := range spec.NonTerminalEngagingStates() {
				engaging[s] = true
			}
			engagingCache[wf.Name] = engaging
		}
		if engaging[it.Status] {
			return true, nil
		}
	}
	return false, nil
}

// isGitCommitOrPush reports whether a Bash command is a git commit or push.
func isGitCommitOrPush(command string) bool {
	c := strings.ToLower(command)
	return strings.Contains(c, "git commit") || strings.Contains(c, "git push")
}

// bashCommandFromEvent pulls the bash command out of a PreToolUse event.
// Accepts Claude Code's snake_case envelope (tool_input.command) AND Grok's
// camelCase envelope (toolInput.command) — both harnesses fire the same hook
// (epic:scoped-sync order:9 / sty_0d3665ee). Prefer the first non-empty value.
func bashCommandFromEvent(raw []byte) string {
	var ev struct {
		// Claude Code
		ToolInputSnake struct {
			Command string `json:"command"`
		} `json:"tool_input"`
		// Grok
		ToolInputCamel struct {
			Command string `json:"command"`
		} `json:"toolInput"`
	}
	_ = json.Unmarshal(raw, &ev)
	if c := ev.ToolInputSnake.Command; c != "" {
		return c
	}
	return ev.ToolInputCamel.Command
}

// filePathFromEvent pulls the edit target out of a PreToolUse edit event.
// Claude Code: tool_input.file_path / tool_input.notebook_path.
// Grok: toolInput.file_path | filePath | path | notebook_path | notebookPath.
// Returns "" when none is present. Prefer Claude snake_case, then Grok aliases.
func filePathFromEvent(raw []byte) string {
	var ev struct {
		ToolInputSnake struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
			// Grok may nest camelCase aliases under tool_input too; accept them.
			FilePathCamel     string `json:"filePath"`
			Path              string `json:"path"`
			NotebookPathCamel string `json:"notebookPath"`
		} `json:"tool_input"`
		ToolInputCamel struct {
			FilePath          string `json:"file_path"`
			FilePathCamel     string `json:"filePath"`
			Path              string `json:"path"`
			NotebookPath      string `json:"notebook_path"`
			NotebookPathCamel string `json:"notebookPath"`
		} `json:"toolInput"`
	}
	_ = json.Unmarshal(raw, &ev)
	for _, p := range []string{
		ev.ToolInputSnake.FilePath,
		ev.ToolInputSnake.NotebookPath,
		ev.ToolInputSnake.FilePathCamel,
		ev.ToolInputSnake.Path,
		ev.ToolInputSnake.NotebookPathCamel,
		ev.ToolInputCamel.FilePath,
		ev.ToolInputCamel.FilePathCamel,
		ev.ToolInputCamel.Path,
		ev.ToolInputCamel.NotebookPath,
		ev.ToolInputCamel.NotebookPathCamel,
	} {
		if p != "" {
			return p
		}
	}
	return ""
}

// withinRepoTarget reports whether target resolves to a path inside this repo.
// The repo root is derived from the committed config path; if it cannot be
// resolved, it returns true (stay conservative — the edit gate still applies).
func withinRepoTarget(target string) bool {
	_, cfgPath, err := config.Load("")
	if err != nil {
		return true
	}
	return withinRoot(config.RepoRootFromConfigPath(cfgPath), target)
}

// exemptTarget reports whether an edit to target is exempt from the engaged-story
// gate: it resolves under the authored-substrate data dir (.satelle/ by default,
// honoring a relocated data_dir), OR under a configured [gate] edit_exempt_paths
// prefix — a harness authoring dir a repo opts in (e.g. .claude/), keeping the
// binary CLI-vendor-neutral (sty_103af456, sty_41416b76). Returns false if the
// config/root cannot be resolved, so the gate stays conservative (still applies)
// on any resolution failure.
func exemptTarget(target string) bool {
	cfg, cfgPath, err := config.Load("")
	if err != nil {
		return false
	}
	root := config.RepoRootFromConfigPath(cfgPath)
	return editExempt(cfg.ResolveDataDir(root), cfg.ResolveEditExemptPaths(root), target)
}

// editExempt is the pure classification the edit-gate exemption rests on: target
// is exempt when it resolves under the data dir or any configured exempt prefix.
// Kept pure (no config/filesystem) so the path classification is unit-tested
// directly. Blank roots are skipped — withinRoot fails open TOWARD inside on a
// blank root (returns true), which is the conservative default for the gate but
// would exempt everything here; ResolveEditExemptPaths already drops blanks, and
// this is the second guard.
func editExempt(dataDir string, exemptRoots []string, target string) bool {
	if strings.TrimSpace(dataDir) != "" && withinRoot(dataDir, target) {
		return true
	}
	for _, r := range exemptRoots {
		if strings.TrimSpace(r) != "" && withinRoot(r, target) {
			return true
		}
	}
	return false
}

// substrateNoStoryWarn is the advisory injected when a data-dir substrate edit
// happens with no engaged story (sty_f5f351d1). Fail-open: the edit is ALLOWED;
// this only nudges toward engaging a substrate story so the change is tracked
// through satelle-substrate-workflow. Emitted on the PreToolUse additionalContext
// channel (model-visible) and to stderr (human transcript).
const substrateNoStoryWarn = "satelle: editing .satelle/ substrate with no engaged story — engage a substrate story (category: substrate) to track it through satelle-substrate-workflow. (edit allowed)"

// dataDirTarget reports the narrower half of exemptTarget: whether target resolves
// under the authored-substrate data dir (.satelle/ by default) specifically — NOT a
// configured [gate] edit_exempt_paths prefix. The no-engaged-story nudge fires only
// on this leg (a repo's exempt opt-in dirs like .claude/ are a deliberate choice and
// stay silent). Reuses editExempt with no exempt roots so the path classification
// stays single-sourced. Returns false on any resolution failure (fail-safe: no nudge).
func dataDirTarget(target string) bool {
	cfg, cfgPath, err := config.Load("")
	if err != nil {
		return false
	}
	root := config.RepoRootFromConfigPath(cfgPath)
	return editExempt(cfg.ResolveDataDir(root), nil, target)
}

// shouldWarnSubstrate is the pure substrate-nudge decision: nudge when the edit is
// under the data dir AND no story is engaged. Pure over its inputs so the decision
// is unit-tested directly (sty_f5f351d1 AC4).
func shouldWarnSubstrate(dataDirOnly, engaged bool) bool {
	return dataDirOnly && !engaged
}

// withinRoot reports whether target resolves to a path inside root. A relative
// target is taken relative to root (the hook runs in the repo cwd). Pure, so the
// path classification is unit-tested without touching the filesystem; any
// resolution failure returns true (treat as in-repo) so the gate never opens by
// accident.
func withinRoot(root, target string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(target) == "" {
		return true
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return true
	}
	t := target
	if !filepath.IsAbs(t) {
		t = filepath.Join(absRoot, t)
	}
	rel, err := filepath.Rel(absRoot, filepath.Clean(t))
	if err != nil {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// runHookContext assembles and emits the SessionStart injection. It fails open:
// any error opening the store or listing docs injects nothing and returns nil.
func runHookContext(out, stderr io.Writer) error {
	a, err := app.Open()
	if err != nil {
		return nil // fail open — unconfigured repo / unopenable db blocks nothing
	}
	defer func() { _ = a.Close() }()

	docs, err := a.Store.DocIndex.List(context.Background(), "")
	if err != nil {
		return nil // fail open
	}
	always := selectAlwaysDocs(docs)
	constitution := readConstitution(a.Config.ResolveConstitution(a.RepoRoot))
	content, truncated := renderAlwaysContent(constitution, always, alwaysContextCeiling)
	if truncated {
		fmt.Fprintf(stderr,
			"satelle hook context: always-content exceeded %d bytes and was truncated — trim an always-tagged doc or drop its %s tag\n",
			alwaysContextCeiling, sessionTag)
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return emitAdditionalContext(out, "SessionStart", "", content)
}

// selectAlwaysDocs returns the SESSION set — every principle carrying the
// principles:session residency marker, in the order the index lists them. The
// marker is the single residency authority (the same one the reviewer reads),
// so which principles are resident is authored substrate, not a hardcoded name:
// a principle is session because it is tagged, or on-demand because it is not.
// Kept minimal by keeping the marker on few docs (the operating principle).
func selectAlwaysDocs(docs []docindex.Doc) []docindex.Doc {
	var out []docindex.Doc
	for _, d := range docs {
		if d.Kind == "principles" && docHasTag(d.Body, sessionTag) {
			out = append(out, d)
		}
	}
	return out
}

// renderAlwaysContent assembles the bounded injection body + the standing index
// instruction. The project constitution (when present) rides FIRST as order-zero
// context, then the session-resident principles. Each doc's frontmatter is
// stripped; content is added whole until the next block would breach the ceiling,
// at which point truncated=true and the rest are dropped (reported by the caller
// on stderr). The instruction is always present, even with no session content, so
// the pull-on-reference discipline is taught from day one.
func renderAlwaysContent(constitution string, docs []docindex.Doc, ceiling int) (string, bool) {
	var b strings.Builder
	truncated := false
	used := 0
	// Order-zero: the project constitution — the repo's definition — rides first.
	if constitution != "" {
		part := "# Project constitution\n\n" + constitution
		b.WriteString(part + "\n\n")
		used += len(part)
		if used > ceiling {
			truncated = true // the constitution alone rides, but flag it
		}
	}
	var parts []string
	for _, d := range docs {
		body := strings.TrimSpace(stripFrontmatter(d.Body))
		if body == "" {
			continue
		}
		part := "### " + d.Name + "\n\n" + body
		if used > 0 && used+len(part) > ceiling {
			truncated = true
			break
		}
		parts = append(parts, part)
		used += len(part)
		if used > ceiling {
			truncated = true // a single oversized doc still rides, but flag it
		}
	}
	if len(parts) > 0 {
		b.WriteString("# Always-resident principles (satelle)\n\n")
		b.WriteString(strings.Join(parts, "\n\n"))
		b.WriteString("\n\n")
	}
	b.WriteString(alwaysIndexInstruction)
	return b.String(), truncated
}

// readConstitution returns the project constitution body (frontmatter stripped),
// or "" when absent or unreadable — the order-zero session context injected every
// session (epic:session-context). Fails open: a missing constitution injects
// nothing and never blocks the session.
func readConstitution(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stripFrontmatter(string(b)))
}

// docHasTag reports whether the markdown's frontmatter `tags:` includes tag.
func docHasTag(body, tag string) bool {
	for _, t := range frontmatterTags(body) {
		if t == tag {
			return true
		}
	}
	return false
}

// frontmatterTags parses the `tags:` value from a markdown frontmatter block.
// It handles both the inline flow form (`tags: [a, b]`) and the block list form
// (`tags:` followed by `- a` lines). Returns nil when there is no frontmatter or
// no tags key.
func frontmatterTags(body string) []string {
	fm := frontmatter(body)
	if fm == "" {
		return nil
	}
	lines := strings.Split(fm, "\n")
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "tags:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, "tags:"))
		if strings.HasPrefix(rest, "[") { // inline flow form
			rest = strings.TrimSuffix(strings.TrimPrefix(rest, "["), "]")
			return splitTrimTags(rest)
		}
		// block list form: gather subsequent "- item" lines
		var out []string
		for j := i + 1; j < len(lines); j++ {
			l2 := strings.TrimSpace(lines[j])
			if l2 == "" {
				continue
			}
			if strings.HasPrefix(l2, "- ") {
				out = append(out, strings.Trim(strings.TrimSpace(l2[2:]), `"'`))
				continue
			}
			break // next key — end of the tags list
		}
		return out
	}
	return nil
}

// splitTrimTags splits a comma-separated inline tag list, trimming whitespace
// and surrounding quotes from each item, dropping empties.
func splitTrimTags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		v := strings.Trim(strings.TrimSpace(p), `"'`)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// frontmatter returns the YAML frontmatter block (between the leading `---` and
// the next `---`), or "" when the body has none.
func frontmatter(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			return strings.Join(lines[1:j], "\n")
		}
	}
	return ""
}

// stripFrontmatter returns the body with any leading YAML frontmatter block
// removed, so the injected content is clean markdown.
func stripFrontmatter(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return body
	}
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			return strings.TrimLeft(strings.Join(lines[j+1:], "\n"), "\n")
		}
	}
	return body
}

// hookContextOut is the Claude Code hook output that injects advisory context.
// PermissionDecision is optional (omitempty): the SessionStart context injector
// omits it (no decision); a PreToolUse allow-with-nudge sets "allow" plus the
// additionalContext the model reads on its next turn.
type hookContextOut struct {
	HookSpecificOutput struct {
		HookEventName      string `json:"hookEventName"`
		PermissionDecision string `json:"permissionDecision,omitempty"`
		AdditionalContext  string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// emitAdditionalContext writes the hook JSON that adds advisory context. event is
// the hook event name; context is the body the model reads (a system reminder for
// SessionStart; beside the tool result for PreToolUse). permissionDecision is
// optional — "" omits it (SessionStart, which makes no permission decision); a
// PreToolUse allow-with-nudge sets "allow" so the edit proceeds while the
// additionalContext advisory rides alongside (the only model-visible channel on an
// ALLOWED edit — bare stderr is transcript-only on exit 0). One emitter for both
// callers (sty_f5f351d1).
func emitAdditionalContext(out io.Writer, event, permissionDecision, context string) error {
	var doc hookContextOut
	doc.HookSpecificOutput.HookEventName = event
	doc.HookSpecificOutput.PermissionDecision = permissionDecision
	doc.HookSpecificOutput.AdditionalContext = context
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(b))
	return nil
}

// emitSubstrateNoStoryNudge writes the fail-open substrate nudge on both channels:
// the PreToolUse additionalContext JSON (permissionDecision "allow") to out — the
// only model-visible path on an allowed edit — and the same line to stderr for the
// human watching the session. The edit stays allowed (the caller returns nil); this
// only advises.
func emitSubstrateNoStoryNudge(out, stderr io.Writer) {
	_ = emitAdditionalContext(out, "PreToolUse", "allow", substrateNoStoryWarn)
	fmt.Fprintln(stderr, substrateNoStoryWarn)
}
