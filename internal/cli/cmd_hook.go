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

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
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
		Long: `gate is the PreToolUse handler for Edit|Write|MultiEdit|NotebookEdit|
search_replace|write. It exits non-zero (the wiring turns that into a block
with '|| exit 2') unless a story is ENGAGED — in one of the active workflow's
non-terminal engaging states (e.g. plan, in_progress, integration, release) —
so the agent works under a tracked story. On deny it emits dual-format JSON on
stdout so BOTH Claude Code and Grok Build surface the reason to the agent
(Claude: hookSpecificOutput.permissionDecision=deny + permissionDecisionReason;
Grok: decision=deny + reason), not a bare "hook denied (exit 2)". The "engaged"
policy is authored substrate — it reads the workflow's DOT shape markers
(Mdiamond=start, Msquare=terminal) rather than hardcoding state names, so
configuration drives the decision (sty_f3d5d4b8, sty_e4902c51).

The edit target is resolved to an ABSOLUTE path against the repo root before any
containment test (sty_8c3d345c). Harnesses differ: Claude Code sends an absolute
file_path, Grok sends a repo-relative one — resolving up front means a relative
target is never nested under a narrower tested root (e.g. the data dir) and
wrongly classed as inside it, which previously let Grok edits bypass the gate.

Edits OUTSIDE the repo root are REFUSED (sty_3026d890): a session in
satelle-server must not rewrite files under a sibling tree such as ../satelle.
If the change belongs in another satelle-enabled project, create and engage the
story in THAT repo. Session scratch under /tmp is no longer a free pass.

Exemption is CONFIGURATION, not code (the constitution: configuration over
code). An edit is exempt from the engaged-story check ONLY when its target falls
under a [gate] edit_exempt_paths prefix (repo-root-relative or absolute). The
binary does NOT special-case the data dir: 'satelle init' SEEDS .satelle/ into
edit_exempt_paths so authored substrate (workflows, skills, principles,
documents, tasks, config) stays editable without a release OOTB — but the
operator sees and owns that list and may add a harness authoring dir like
.claude/ or remove .satelle/ entirely. With an empty edit_exempt_paths, even a
.satelle/ edit needs an engaged story: config decides, never a Go rule. Generated
views under the data dir stay protected by their 0o444 file mode regardless.

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
				// Cross-repo lock (sty_3026d890): refuse any edit outside this repo.
				// Observed failure: agent in satelle-server wrote CLI code under
				// ../satelle with no story in the correct repo — process break.
				if !withinRepoTarget(p) {
					return denyPreToolUse(cmd, outsideRepoEditReason(p))
				}
				// Exemption is CONFIGURATION, not code (the constitution:
				// configuration over code). Only a path under a [gate]
				// edit_exempt_paths prefix is exempt from the engaged-story gate.
				// The data dir is NOT special-cased in the binary — `satelle init`
				// seeds .satelle/ into edit_exempt_paths so authored substrate stays
				// editable without a release OOTB, but the operator owns that list
				// (sty_8c3d345c). With an empty list, even a .satelle/ edit needs an
				// engaged story: config decides, never a Go rule.
				if exemptTarget(p) {
					return nil
				}
			}
			// Engagement-error surface: a broken deployment blocks the edit with a
			// clear message rather than silently allowing it (sty_f3d5d4b8).
			if engErr != nil {
				return denyPreToolUse(cmd, "satelle: "+engErr.Error())
			}
			if engaged {
				return nil
			}
			return denyPreToolUse(cmd, noEngagedStoryEditReason)
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
				return denyPreToolUse(cmd, "satelle: "+err.Error())
			}
			if engaged {
				return nil
			}
			return denyPreToolUse(cmd, noEngagedStoryCommitReason)
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
// one (wfgovern.GoverningWorkflow). A "non-terminal engaging state" is one that
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
		wf, ok := wfgovern.GoverningWorkflow(wfs, it)
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
// resolved, it returns true (stay conservative — treat as in-repo so other gate
// rules still apply rather than free-passing an unresolvable path).
func withinRepoTarget(target string) bool {
	_, cfgPath, err := config.Load("")
	if err != nil {
		return true
	}
	root := config.RepoRootFromConfigPath(cfgPath)
	return withinRoot(root, resolveAbsTarget(root, target))
}

// noEngagedStoryEditReason is the canonical agent-facing deny for product-code
// edits without a performing story (sty_e4902c51). Both Claude and Grok must
// surface this text — not a bare "hook denied (exit 2)".
const noEngagedStoryEditReason = "satelle: you're mutating the tree without a performing story, or you have used the wrong tool for reading. " +
	"Open a story session before editing code: satelle story create …, then satelle story set <id> --status plan. " +
	"That session stays open through your edits until the story reaches a terminal or parked state (done, cancelled, or blocked) — finishing an edit does NOT close it. " +
	"For research, use read tools (Read/read_file/grep/Glob) — not Edit/Write/search_replace."

// noEngagedStoryCommitReason is the agent-facing deny for git commit/push without
// an engaged story. Same dual-format emission as the edit gate.
const noEngagedStoryCommitReason = "satelle: refusing to commit/push with no engaged story — " +
	"engage a story (satelle story set <id> --status plan) so the change is tracked through the workflow."

// outsideRepoEditReason is the agent-facing refusal when a PreToolUse edit targets
// a path outside the current repo root (sty_3026d890). Kept as a pure string so
// dual-format deny emission and unit tests share one stable message.
func outsideRepoEditReason(path string) string {
	return fmt.Sprintf(
		"satelle: refusing edit outside this repo (%s) — only paths under the repo root may be modified here. If this change belongs in another project (e.g. CLI work for an epic tracked on satelle-server), open a session in THAT repo and create/engage the story there: satelle story create … then satelle story set <id> --status plan",
		path)
}

// outsideRepoEditErr wraps outsideRepoEditReason as an error for tests that
// still assert .Error() on the refusal helper.
func outsideRepoEditErr(path string) error {
	return fmt.Errorf("%s", outsideRepoEditReason(path))
}

// denyPreToolUse emits a dual-harness deny payload on stdout (Claude + Grok) and
// returns an error so the process exits non-zero (hook wiring: `|| exit 2`).
// The reason is also on the error (cobra → stderr) for humans/transcripts.
func denyPreToolUse(cmd *cobra.Command, reason string) error {
	_ = emitPreToolUseDeny(cmd.OutOrStdout(), reason)
	return fmt.Errorf("%s", reason)
}

// preToolUseDenyOut is the common PreToolUse deny payload for Claude Code and
// Grok Build (sty_e4902c51):
//
//   - Grok: top-level {"decision":"deny","reason":"…"} (docs: decision + reason)
//   - Claude: hookSpecificOutput.permissionDecision=deny +
//     permissionDecisionReason (shown to the model on deny)
//
// One JSON object carries both shapes so harness-specific hook scripts stay
// identical: `satelle hook gate || exit 2`.
type preToolUseDenyOut struct {
	Decision           string `json:"decision"`
	Reason             string `json:"reason"`
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
		AdditionalContext        string `json:"additionalContext,omitempty"`
	} `json:"hookSpecificOutput"`
}

// emitPreToolUseDeny writes the dual-format deny JSON to out (one line).
func emitPreToolUseDeny(out io.Writer, reason string) error {
	var doc preToolUseDenyOut
	doc.Decision = "deny"
	doc.Reason = reason
	doc.HookSpecificOutput.HookEventName = "PreToolUse"
	doc.HookSpecificOutput.PermissionDecision = "deny"
	doc.HookSpecificOutput.PermissionDecisionReason = reason
	doc.HookSpecificOutput.AdditionalContext = reason
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(b))
	return nil
}

// exemptTarget reports whether an edit to target is exempt from the engaged-story
// gate: it resolves under a configured [gate] edit_exempt_paths prefix. Exemption
// is CONFIGURATION, not code (the constitution: configuration over code) — the
// binary no longer special-cases the data dir; `satelle init` seeds .satelle/ into
// edit_exempt_paths so authored substrate stays editable OOTB, but the operator
// owns that list (sty_8c3d345c). The target is resolved to absolute against the
// repo root FIRST, so a repo-relative path (as Grok sends) is classified correctly.
// Returns false if the config/root cannot be resolved, so the gate stays
// conservative (still applies) on any resolution failure.
func exemptTarget(target string) bool {
	cfg, cfgPath, err := config.Load("")
	if err != nil {
		return false
	}
	root := config.RepoRootFromConfigPath(cfgPath)
	return editExempt(cfg.ResolveEditExemptPaths(root), resolveAbsTarget(root, target))
}

// editExempt is the pure classification the edit-gate exemption rests on: target
// is exempt when it resolves under any configured exempt prefix. Kept pure (no
// config/filesystem) so the path classification is unit-tested directly. Callers
// pass an already-absolute target (see resolveAbsTarget) and absolute prefixes
// (ResolveEditExemptPaths); blank prefixes are skipped — a blank prefix would make
// withinRoot fail open TOWARD inside and exempt everything, so this is the guard.
func editExempt(exemptRoots []string, target string) bool {
	for _, r := range exemptRoots {
		if strings.TrimSpace(r) != "" && withinRoot(r, target) {
			return true
		}
	}
	return false
}

// resolveAbsTarget makes target absolute against root (the repo root the hook runs
// in). A blank target passes through; an already-absolute target is cleaned and
// returned; a relative target is joined under root. This is the single point that
// pins a repo-relative edit path (as Grok sends) to the repo root BEFORE any
// containment test, so a relative target is never nested under a narrower tested
// root (e.g. the data dir) and mis-classed as inside it (sty_8c3d345c). On a
// resolution error the raw target is returned unchanged (withinRoot stays
// conservative from there).
func resolveAbsTarget(root, target string) string {
	if strings.TrimSpace(target) == "" {
		return target
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return target
	}
	return filepath.Clean(filepath.Join(absRoot, target))
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
