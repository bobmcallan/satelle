// `satelle hook` carries the Claude Code hook handlers. Currently one:
// `satelle hook context` — the SessionStart always-context injector
// (sty_e3922598).
//
// At session start it fetches every `principles:always`-flagged authored doc
// and injects their bodies as session context, followed by the standing "pull
// the rest on demand" instruction. This keeps the small resident set (the
// constitution + repo-agnostic principle) in front of the agent without
// auto-injecting an unbounded list — the bodies are bounded by a byte ceiling,
// and an overflow is reported on stderr (never silently dropped). It FAILS OPEN:
// an unconfigured repo or any read error injects nothing and never blocks the
// session. This is the mechanism that makes the `principles:always` residency
// marker live (see the satelle-constitution / satelle-repo-agnostic principles).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/reviewer"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// alwaysTag is the residency marker: a doc carrying it in its frontmatter tags
// is part of the resident set injected every session.
const alwaysTag = "principles:always"

// alwaysContextCeiling bounds the total injected always-content. The resident
// set is meant to be small (a handful of principle-sized docs); this is the
// backstop that stops a mis-tagged large doc from blowing the context budget the
// whole model is meant to protect. Sized to hold satelle's order-zero principles
// (constitution, repo-agnostic, agent-goals, done-is-last) with headroom.
const alwaysContextCeiling = 16384

// alwaysIndexInstruction is the standing "pull, don't preload" directive
// appended to every injection — the pivot of the always-context model: the
// resident set is pushed, everything else is discovered on demand.
const alwaysIndexInstruction = "To discover other documents and principles beyond the resident set above, run `satelle doc list` and load only the ones the task needs — do not preload everything."

func init() {
	hook := &cobra.Command{
		Use:   "hook",
		Short: "Claude Code hook handlers (SessionStart context injection, …)",
	}
	context := &cobra.Command{
		Use:   "context",
		Short: "SessionStart always-context injector — inject principles:always docs + the index pointer",
		Long: `context is the SessionStart handler. It injects every principles:always
authored doc (the resident set — e.g. the project constitution) as session
context, then the standing instruction to discover the rest via ` + "`satelle doc list`" + `.
Bounded by a byte ceiling (overflow noted on stderr); fails open so it never
blocks a session.`,
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
story is ENGAGED — in one of the active workflow's executor states (e.g.
in_progress) — so the agent works under a tracked story. Fails open: an
unconfigured repo or any internal error allows the edit. The "engaged" policy is
authored substrate — it reads the workflow's executor-actor states, not a Go rule.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			// An edit whose target is OUTSIDE the repo (e.g. the session
			// scratchpad under /tmp) is untracked scratch, not project code —
			// never gated. Only in-repo edits require an engaged story.
			if p := filePathFromEvent(raw); p != "" && !withinRepoTarget(p) {
				return nil
			}
			if storyEngaged() {
				return nil
			}
			return fmt.Errorf("satelle: no engaged story — create or engage one before editing code " +
				"(satelle story create …, then satelle story set <id> --status in_progress). " +
				"The workflow requires work to proceed under a tracked story.")
		},
	}

	commitgate := &cobra.Command{
		Use:   "commitgate",
		Short: "PreToolUse Bash gate — block git commit/push unless a story is engaged",
		Long: `commitgate is the PreToolUse handler for Bash. It allows any command that is
not a git commit/push; for a commit/push it exits non-zero (blocked via
'|| exit 2') unless a story is engaged, so changes are committed under a tracked
story. Fails open on any internal error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			if !isGitCommitOrPush(bashCommandFromEvent(raw)) {
				return nil // not a commit/push — allow
			}
			if storyEngaged() {
				return nil
			}
			return fmt.Errorf("satelle: refusing to commit/push with no engaged story — " +
				"engage a story (satelle story set <id> --status in_progress) so the change is tracked through the workflow.")
		},
	}

	hook.AddCommand(context, gate, commitgate)
	register(hook)
}

// storyEngaged reports whether any work item — story OR task — is in one of the
// active workflow's executor states (the authored definition of "engaged work").
// Fails OPEN: an unopenable store or list error returns true (allow), so the
// hooks never wedge a session on an internal fault.
func storyEngaged() bool {
	a, err := app.Open()
	if err != nil {
		return true
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	engaged := map[string]bool{"in_progress": true} // fallback if no workflow resolves
	if wfs, e := a.Store.DocIndex.List(ctx, "workflows"); e == nil {
		if ordered := reviewer.OrderedWorkflows(wfs, ""); len(ordered) > 0 {
			if es := executorStates(ordered[0].Body); len(es) > 0 {
				engaged = map[string]bool{}
				for _, s := range es {
					engaged[s] = true
				}
			}
		}
	}
	// All kinds — a task engaged in an executor state counts exactly like a story,
	// so the commit/edit gates treat engaged tasks the same (sty_3ed91a58).
	items, e := a.Store.Stories.List(ctx, workitem.ListFilter{})
	if e != nil {
		return true // fail open
	}
	return anyEngaged(items, engaged)
}

// anyEngaged reports whether any work item (story or task) sits in one of the
// engaged executor states — the pure core of storyEngaged, split out for testing.
func anyEngaged(items []workitem.Item, engaged map[string]bool) bool {
	for _, it := range items {
		if engaged[it.Status] {
			return true
		}
	}
	return false
}

// executorStates parses the active workflow body for states marked
// `agent: executor` (the legacy `actor:` still parses) — the states that
// represent engaged work. The "engaged" policy is thus authored in the workflow,
// not hardcoded.
func executorStates(body string) []string {
	// DOT workflow: the executor states are the nodes marked agent=executor in
	// the shared wfdot spec.
	if spec, ok := wfdot.Parse(body); ok {
		var out []string
		for _, s := range spec.States {
			if s.Agent == "executor" {
				out = append(out, s.Name)
			}
		}
		return out
	}
	lines := strings.Split(body, "\n")
	in := false
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "states:" {
			in = true
			continue
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(t, "- ") {
			break // end of the states block
		}
		item := strings.TrimSpace(t[2:])
		if strings.HasPrefix(item, "{") && strings.Contains(item, "actor:") && strings.Contains(item, "executor") {
			if name := hookInlineField(item, "name"); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// hookInlineField extracts key's value from a YAML inline-map line, to the next
// comma or brace, quotes trimmed.
func hookInlineField(line, key string) string {
	i := strings.Index(line, key+":")
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(line[i+len(key)+1:], " ")
	if end := strings.IndexAny(rest, ",}"); end >= 0 {
		rest = rest[:end]
	}
	return strings.Trim(strings.TrimSpace(rest), `"'`)
}

// isGitCommitOrPush reports whether a Bash command is a git commit or push.
func isGitCommitOrPush(command string) bool {
	c := strings.ToLower(command)
	return strings.Contains(c, "git commit") || strings.Contains(c, "git push")
}

// bashCommandFromEvent pulls tool_input.command out of a PreToolUse Bash event.
func bashCommandFromEvent(raw []byte) string {
	var ev struct {
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	_ = json.Unmarshal(raw, &ev)
	return ev.ToolInput.Command
}

// filePathFromEvent pulls the edit target out of a PreToolUse edit event.
// Write/Edit/MultiEdit carry tool_input.file_path; NotebookEdit carries
// notebook_path. Returns "" when neither is present.
func filePathFromEvent(raw []byte) string {
	var ev struct {
		ToolInput struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		} `json:"tool_input"`
	}
	_ = json.Unmarshal(raw, &ev)
	if ev.ToolInput.FilePath != "" {
		return ev.ToolInput.FilePath
	}
	return ev.ToolInput.NotebookPath
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
	content, truncated := renderAlwaysContent(always, alwaysContextCeiling)
	if truncated {
		fmt.Fprintf(stderr,
			"satelle hook context: always-content exceeded %d bytes and was truncated — trim an always-tagged doc or drop its %s tag\n",
			alwaysContextCeiling, alwaysTag)
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return emitAdditionalContext(out, "SessionStart", content)
}

// selectAlwaysDocs returns the single always-resident principle — the one tight
// operating principle (config.OperatingPrinciple). Every other principle is
// resolvable substrate read on demand, never auto-injected (sty_53a4233c).
func selectAlwaysDocs(docs []docindex.Doc) []docindex.Doc {
	for _, d := range docs {
		if d.Kind == "principles" && d.Name == config.OperatingPrinciple {
			return []docindex.Doc{d}
		}
	}
	return nil
}

// renderAlwaysContent assembles the bounded injection body + the standing index
// instruction. Each doc's frontmatter is stripped; docs are added whole until
// the next would breach the ceiling, at which point truncated=true and the rest
// are dropped (reported by the caller on stderr). The instruction is always
// present, even with no always-docs, so the pull-the-index discipline is taught
// from day one.
func renderAlwaysContent(docs []docindex.Doc, ceiling int) (string, bool) {
	var parts []string
	truncated := false
	used := 0
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
	var b strings.Builder
	if len(parts) > 0 {
		b.WriteString("# Always-resident principles (satelle)\n\n")
		b.WriteString(strings.Join(parts, "\n\n"))
		b.WriteString("\n\n")
	}
	b.WriteString(alwaysIndexInstruction)
	return b.String(), truncated
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

// hookContextOut is the Claude Code hook output that injects advisory context
// without a permission decision, so the session proceeds normally.
type hookContextOut struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// emitAdditionalContext writes the hook JSON that adds context to the session.
func emitAdditionalContext(out io.Writer, event, context string) error {
	var doc hookContextOut
	doc.HookSpecificOutput.HookEventName = event
	doc.HookSpecificOutput.AdditionalContext = context
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(b))
	return nil
}
