// `satelle hook` carries the Claude Code hook handlers. Currently one:
// `satelle hook context` — the SessionStart always-context injector
// (sty_e3922598).
//
// At session start it fetches every `principles:session`-flagged authored doc
// and injects their bodies as session context, followed by the standing "pull
// the rest on demand" instruction. This keeps the minimal SYSTEM residency set
// in front of the agent without auto-injecting an unbounded list — the bodies
// are bounded by a byte ceiling, and an overflow is reported on stderr (never
// silently dropped). It FAILS OPEN: an unconfigured repo or any read error
// injects nothing and never blocks the session.
//
// Residency taxonomy (sty_1278fdd9 / satelle-residency): ONE classifier —
//
//	system   = carries the principles:session tag → injected every session
//	ondemand = no marker (default) → pulled only when a skill/workflow references it
//
// Frontmatter scope: on principles is NOT an injection axis (removed; inert if reintroduced).
// Ownership (embedded_sha) is orthogonal to residency.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/compact"
	"github.com/bobmcallan/satelle/internal/condense"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/docstory"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/wfroute"
	"github.com/bobmcallan/satelle/internal/workitem"
	"github.com/bobmcallan/satelle/internal/worktree"
)

// sessionTag is the sole system-residency marker (taxonomy: system|ondemand).
// A principle carrying it is system-resident — injected every SessionStart.
// A principle WITHOUT it is ondemand — resolvable substrate pulled only when a
// skill or workflow references it, never auto-injected. No other frontmatter
// field (including legacy scope:) participates in injection.
const sessionTag = "principles:session"

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
		Long: `The handlers a harness calls on satelle's behalf: session context injection, the
edit and commit gates, the prompt reminder.

You do not run these by hand — the harness does, on its own events, and they read
their payload from stdin. Read them to understand what satelle enforces in a
session; install or remove the wiring with satelle agents.`,
	}
	context := &cobra.Command{
		Use:   "context",
		Short: "Session-context injector — inject principles:session docs + the on-demand pointer",
		Long: `context injects every principles:session authored doc (the SESSION set —
the minimal operating principle) as session context, then the standing
instruction that the rest is on-demand: pulled via ` + "`satelle doc get`" + ` only
when a skill or workflow references it. The emitting event is the harness's
session_context_event; a payload that names a different event emits nothing,
and a payload with no event field keeps the diagnostic SessionStart contract.
Bounded by that event's budget (overflow noted on stderr); fails open so it
never blocks a session.`,
		Args: cobra.NoArgs,
		// No store annotation: this command opens the store itself, defensively,
		// so any bootstrap failure fails OPEN (exit 0, inject nothing) rather than
		// blocking the session.
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bind identity from the SessionStart payload so a later
			// `satelle story set` in this harness tree can stamp the same id.
			raw, _ := io.ReadAll(cmd.InOrStdin())
			_ = bindSessionID(raw)
			return runHookContext(cmd.OutOrStdout(), cmd.ErrOrStderr(),
				resolveContextHarness(hookHarnessFlag, raw, os.Environ()), raw)
		},
	}
	context.Flags().StringVar(&hookHarnessFlag, "harness", "", "claude|grok|pi|cursor —selects the injection limit and output shape (default: sniff event, then environment)")
	gate := &cobra.Command{
		Use:   "gate",
		Short: "PreToolUse edit gate — block code edits unless a story is engaged",
		Long: `gate is the PreToolUse handler for Edit|Write|MultiEdit|NotebookEdit|
search_replace|write. It returns a deny unless a story is ENGAGED — in one of the
active workflow's non-terminal engaging states. On deny it
emits one harness-correct JSON shape on stdout, named by --harness or detected
from the event envelope: Claude hookSpecificOutput.permissionDecision=deny;
Grok decision=deny + reason; cursor permission=deny + user_message +
agent_message. The encodings live in internal/agentcli. Emitting two shapes in
one blob fails Claude's schema and silently unblocks the tool.
"Engaged" is authored substrate: it reads the route's start/terminal markers
(Mdiamond=start, Msquare=terminal), never hardcoded state names.

The installed satelle-hook.sh wrapper passes a deny it recognises through
unchanged, exit 0; any other result becomes that harness's infrastructure deny.
commitgate fails open for non-mutating shell.

The edit target is resolved to an ABSOLUTE path against the repo root before any
containment test: Claude sends an absolute path, Grok a relative one.

Edits landing in ANOTHER git working tree are REFUSED — open a
session in THAT repo. Temp, scratchpad and non-repo paths are allowed.

Exemption is CONFIGURATION, not code. An edit is exempt when its target falls
under a [gate] edit_exempt_paths prefix (repo-root-relative or absolute) or
matches an edit_exempt_globs filename pattern. 'satelle init' SEEDS .satelle/,
the footprint it deploys (.gitignore, harness scaffolds) and story-dump names;
the operator owns the lists.

Lock precedence over edit_exempt_paths: see 'satelle help substrate'. A path
under a [gate] lock_substrate_paths prefix (default .satelle/ when the key is
absent) is judged before the exemption is consulted, so no edit can rewrite the
substrate that judges the work.
One harness-neutral predicate decides it for Claude, Grok and pi. The deny names
each holder (status, category), the path, the condition and fix, and lands on
that ledger. A substrate-lane story (category "substrate", judged by
satelle-workflow-change-review) may change substrate while siblings perform;
they get substrate_change_note. Never locked:
temp dirs, edit_exempt_globs matches, and the deployed footprint (.gitignore,
.claude/, .grok/, .pi/). lock_substrate_paths = [] opts out; 'satelle doctor' reports it.

The lock covers Bash, here and in commitgate, with the same text and ledger
row: a redirect, tee, rm/mv/cp/sed -i or git -C target in locked substrate, or
an interpreter (python, node, perl, ruby) whose text names a locked path or the
bare root (.satelle) — even if it only READS. A shell -c program is classified
as shell unless not plain ('satelle help substrate'). Unseen, so allowed:
python3 tool.py, '.sat'+'elle'.

Fails closed: a store or listing error, unresolvable workflow, or
workflow declaring no route blocks the edit.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (rerr error) {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			// One git identity lookup and one store for the whole call (as
			// commitgate holds them, sty_a74551d6 AC5): the engagement read and the
			// in-loop run trace below share them. The store opens lazily, so a call
			// that resolves no engagement never opens it.
			defer config.MemoGitIdentity()()
			gs := &gateStore{load: true}
			defer gs.close()
			// The in-loop run trace (sty_23ceeca2): a call this gate let through is
			// recorded on the engaged story, while its step declares evidence a
			// trace could show; a denied one never ran. The engagement is the one
			// the call resolved below, not a second resolution.
			var traced traceEngagement
			defer func() {
				if rerr == nil {
					recordToolTrace(gs, raw, traced)
				}
			}()
			// The engagement is resolved once, by the tree the call lands in, so
			// every check below and the deny reason read one answer.
			sid := bindSessionID(raw)
			resumeWakeFor(raw).activity() // a tool call: the session is mid-turn
			p := filePathFromEvent(raw)
			command := agentcli.CommandFromHookEvent(raw)
			// A harness whose hook file cannot filter by tool name sends this verb
			// EVERY tool call, so classify it here (sty_7d098d50): an edit or write
			// is judged below on its target, a shell is commitgate's, a tool the
			// table cannot place fails closed, and any other class is allowed.
			route, unknownTool := routeUnmatchedTool(raw)
			switch route {
			case routeSkip:
				return nil
			case routeUnknown:
				p, command = "", ""
			case routeJudge:
				command = "" // an edit-class payload: the target alone is judged
			}
			// An edit is attributed by the tree it targets (sty_42231b74,
			// sty_9c782f0e); the fence below decides whether that tree may be
			// edited at all.
			// A shell command has no path; its first mutation target's tree stands in.
			attribution := treeOf(p)
			if p == "" && command != "" {
				attribution = bashAttributionTree(command, sessionAnchor())
			}
			info, engaged, live, engErr := gs.engagementsFor(sid, attribution)
			traced = traceEngagement{info: info, engaged: engaged, resolved: true}
			denyTree := attribution
			if denyTree == "" {
				denyTree = sessionWorktree()
			}
			if p != "" {
				// Foreign-tree fence (sty_a8454d10 / sty_aadd4d6c): refuse edits
				// that land in ANOTHER git working tree unless the operator opts
				// in. Non-repo paths (temp, scratchpads) are not fenced.
				// Observed failure: agent in satelle-server wrote CLI code under
				// ../satelle with no story in the correct repo — process break.
				if !allowOutsideTreeEdits() {
					if foreignRoot, foreign := editTargetForeign(p); foreign {
						return denyPreToolUse(cmd, raw, outsideRepoEditReason(p, foreignRoot))
					}
				}
				if err := denyIfNoImplement(cmd, raw, p); err != nil {
					return err
				}
				// Exemption is configuration plus one mechanism rule (path
				// containment, same class as the foreign-tree fence): a write
				// under the process temp dir is not an in-repo product edit
				// (sty_e33f78fe). A path under a [gate] edit_exempt_paths prefix
				// or a [gate] edit_exempt_globs filename pattern is also exempt.
				// The data dir / managed paths are NOT special-cased in the binary —
				// `satelle init` seeds .satelle/ and the footprint it deploys itself
				// (.gitignore block, harness scaffolds) into edit_exempt_paths, and
				// story-dump names into edit_exempt_globs, so authored substrate
				// and satelle-written output stay editable without a release OOTB,
				// but the operator owns those lists. With empty lists, even a
				// .satelle/ edit needs an engaged story: config decides in-repo
				// paths, never a Go rule.
				//
				// The one thing exemption may not do is let an edit rewrite the
				// substrate that judges the work: a locked prefix ([gate]
				// lock_substrate_paths, default .satelle/) is judged HERE, before
				// the exemption, whether or not any story is performing, by the
				// same harness-neutral predicate for every harness (sty_992cffc6,
				// sty_66abc0e3).
				if err := substrateLockGate(cmd, raw, p, &lockEngagements{info: info, engaged: engaged, live: live}); err != nil {
					return err
				}
				if exemptTarget(p) {
					return nil
				}
			} else if command != "" {
				// A shell command routed through this hook. Preserve read-only
				// preflight: only shell commands with an in-tree mutation target
				// need workflow edit permission. Foreign-tree containment stays
				// active here as well as commitgate.
				if !allowOutsideTreeEdits() {
					_, foreign := bashMutationTargets(command, sessionAnchor())
					if path, foreignRoot, ok := foreignTreeTarget(sessionAnchor(), foreign); ok {
						return denyPreToolUse(cmd, raw, outsideAnchorBashReason(path, foreignRoot))
					}
				}
				// The substrate lock holds for a shell command as it does for an
				// Edit: a classified target, or an interpreter's command text,
				// under a locked prefix is refused before the exemption is
				// consulted (sty_dc77e118).
				anchored := &lockEngagements{info: info, engaged: engaged, live: live}
				if err := bashSubstrateLockGate(cmd, raw, command, loadGateProcess(), func(tree string) (*lockEngagements, error) {
					if tree == "" {
						return anchored, nil
					}
					ti, te, tl, err := resolveEngagementsFor(sid, tree)
					return &lockEngagements{info: ti, engaged: te, live: tl}, err
				}); err != nil {
					return err
				}
				if !bashMutatesTree(command, sessionAnchor()) {
					return nil
				}
			}
			// Engagement-error surface: a broken deployment blocks the edit with a
			// clear message rather than silently allowing it (sty_f3d5d4b8).
			if engErr != nil {
				return denyPreToolUse(cmd, raw, "satelle: "+engErr.Error())
			}
			_ = engaged // broad engagement remains available to other hook surfaces
			dm, rm := currentDispatchMarker(), currentRelayMarker()
			if hookEditPermitted(info, dm, rm) {
				return nil
			}
			if route == routeUnknown {
				// No story is performing: a tool nothing classifies cannot be shown
				// read-only, so it is refused, with the way in appended.
				return denyPreToolUse(cmd, raw, unrecognisedToolReason(raw, unknownTool)+" "+
					hookDenyReason(info, live, dm, rm, denyTree))
			}
			// The scoped in-loop fix lane (sty_4b694872) is a second look at a
			// PATH edit the ordinary rule refused: one recorded, in-bound claim
			// on exactly this path licenses exactly one edit. It never widens
			// what a gate judges, and never applies to a dispatched performer.
			note := ""
			if p != "" {
				var granted bool
				if granted, note = fixLaneEdit(info, dm, rm, raw, p); granted {
					return nil
				}
			}
			return denyPreToolUse(cmd, raw, hookDenyReason(info, live, dm, rm, denyTree)+note)
		},
	}

	commitgate := &cobra.Command{
		Use:   "commitgate",
		Short: "PreToolUse Bash gate — foreign-tree containment + engaged-story commit/push",
		Long: `commitgate is the PreToolUse handler for Bash. It first applies foreign-tree
containment: a command mutating a git working tree other than the session
anchor's is denied unless [gate] allow_outside_tree_edits is true. Then it
denies git commit/push unless a story is engaged, failing closed on
store/listing/workflow-resolution errors.

Step policy: [gate.command_allow] (push = ["release"]) limits a git subcommand
to the listed story statuses.

Command rules: the repo's [[gate.command_rule]] entries deny or warn; satelle
ships none ('help command-rules'). Best-effort: wrappers, eval and scripts
bypass them. satelle manages no identity or rights: the tool's own permissions
and the account it runs as enforce what a command may do.

Substrate lock: a command whose target or interpreter text names locked
substrate is refused, even if it only READS; a shell -c program is classified
as shell. Not seen: python3 tool.py, '.sat'+'elle'.

Attribution: a commit/push goes to the story engaged from its worktree (each cd,
then -C; an unmoved push, its branch's), else the hook's.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (rerr error) {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			resumeWakeFor(raw).activity() // a tool call: the session is mid-turn
			// Only a shell reaches the rest of this verb from a harness whose hook
			// file cannot filter by tool name (sty_7d098d50): its other tools are the
			// edit gate's, and a tool_input.command on one of them is not a shell.
			if !shellOrMatched(raw) {
				return nil
			}
			command := agentcli.CommandFromHookEvent(raw)
			// One `git rev-parse` resolves the repo identity for the whole call:
			// the config load, the store's runtime dir and the repo marker all
			// ask for it, and none of them may start git again (sty_a74551d6 AC5).
			defer config.MemoGitIdentity()()
			defer holdSessionWorktree()()
			// The one store this call opens for command rules, shared by the
			// engagement read and the ledger row. Opened lazily, so a call no rule matched
			// never opens it. Deferred first, so it closes after the record below.
			gs := &gateStore{}
			defer gs.close()
			// A command rule that matched and let the command run (allowed by the
			// story's status, or a warn) is recorded and, for a warn, delivered
			// only once every later check has also let it through: a deny after a
			// warn must leave one answer on stdout, not two.
			var ruled commandRuleDecision
			defer func() {
				if rerr != nil || !ruled.Matched {
					return
				}
				h := commandHarness(raw)
				if ruled.Decision == commandRuleWarned {
					_ = emitAdditionalContext(cmd.OutOrStdout(), h, "PreToolUse", "", ruled.Reason)
				}
				recordCommandRule(gs, ruled, command, h)
			}()
			// Containment BEFORE the engaged-story branch: a foreign-tree mutation
			// is wrong even with a story engaged (sty_aadd4d6c / sty_a8454d10).
			// The process config loads once for this call: containment, command
			// rules and command_allow all read this one load, so the hook starts
			// one `git rev-parse` for its config and no more.
			gp := loadGateProcess()
			gs.gp = gp
			// The in-loop run trace (sty_23ceeca2): a command this hook let run is
			// recorded on the engaged story, on the store the call already holds. A
			// matcher-less harness's gate has already seen this call.
			defer func() {
				if rerr == nil && !invokingNoToolMatcher(raw) {
					recordToolTrace(gs, raw, traceEngagement{})
				}
			}()
			if !allowOutsideTreeEditsFrom(gp) {
				cands := mutationTargets(command, sessionAnchor())
				if path, foreignRoot, ok := foreignTreeTarget(sessionAnchor(), cands); ok {
					return denyPreToolUse(cmd, raw, outsideAnchorBashReason(path, foreignRoot))
				}
			}
			var info engagement
			var engaged bool
			var live []engagement
			var resolved bool
			var sid string
			// The substrate lock before the mutation gate: a command whose target
			// is locked substrate is refused even though .satelle/ is exempt from
			// the engaged-story gate (sty_dc77e118). Engagements resolve only when
			// the command has a lockable candidate.
			// Each candidate is attributed by its own tree; the answer for a
			// candidate with no tree is the anchor-tree one the mutation gate
			// below reuses.
			if err := bashSubstrateLockGate(cmd, raw, command, gp, func(tree string) (*lockEngagements, error) {
				sid = bindSessionID(raw)
				if tree != "" {
					ti, te, tl, err := resolveEngagementsFor(sid, tree)
					return &lockEngagements{info: ti, engaged: te, live: tl}, err
				}
				var err error
				info, engaged, live, err = resolveEngagements(sid)
				resolved = true
				return &lockEngagements{info: info, engaged: engaged, live: live}, err
			}); err != nil {
				return err
			}
			if bashMutatesTree(command, sessionAnchor()) {
				// The mutation is judged against the engagement of the tree it lands
				// in. That answer stays local unless it is the anchor-tree one the
				// commit gate below reuses.
				mInfo, mLive := info, live
				mTree := bashAttributionTree(command, sessionAnchor())
				if tree := mTree; tree != "" {
					var err error
					sid = bindSessionID(raw)
					mInfo, _, mLive, err = resolveEngagementsFor(sid, tree)
					if err != nil {
						return denyPreToolUse(cmd, raw, "satelle: "+err.Error())
					}
				} else if !resolved {
					var err error
					sid = bindSessionID(raw)
					info, engaged, live, err = resolveEngagements(sid)
					resolved = true
					mInfo, mLive = info, live
					if err != nil {
						return denyPreToolUse(cmd, raw, "satelle: "+err.Error())
					}
				}
				dm, rm := currentDispatchMarker(), currentRelayMarker()
				if !hookEditPermitted(mInfo, dm, rm) {
					if mTree == "" {
						mTree = sessionWorktree()
					}
					return denyPreToolUse(cmd, raw, hookDenyReason(mInfo, mLive, dm, rm, mTree))
				}
			}
			// Repo-authored command rules (sty_a74551d6), before command_allow. The
			// engagement is read only for a command a rule matched, so with no rules, or
			// no match, this adds nothing but the config load. Rules that fail to
			// load refuse every shell command: dropping them would turn every
			// authored deny off without a signal.
			rules, refuse := commandRulesFrom(gp)
			if refuse != "" {
				return denyPreToolUse(cmd, raw, refuse)
			}
			if len(rules) > 0 {
				ruled = evalCommandRules(rules, command, func() (engagement, bool, error) {
					if resolved {
						return info, engaged, nil
					}
					return gs.resolve(bindSessionID(raw))
				})
				if ruled.Decision == commandRuleDenied {
					recordCommandRule(gs, ruled, command, commandHarness(raw))
					return denyPreToolUse(cmd, raw, ruled.Reason)
				}
			}
			// Engage gate still applies only to commit/push (today's default).
			// Opt-in [gate.command_allow] may restrict those (or other git
			// subcommands) further by story status (sty_c21490cc).
			subs := gitSubcommands(command)
			needsEngage := false
			for _, sub := range subs {
				if sub == "commit" || sub == "push" {
					needsEngage = true
					break
				}
			}
			allowPolicy := commandAllowFrom(gp)
			if !needsEngage && !commandAllowRestrictsWith(allowPolicy, subs) {
				return nil // not a gated command — allow
			}
			// A commit/push a leading cd or its own -C moved into another tree is
			// attributed to the story engaged from that tree (sty_3a9b06fe); an
			// unmoved one passes no tree, so the hook's own tree decides as it
			// always did. The shell starts where the hook runs, so that is where a
			// relative cd resolves from. An engagement resolved above judged an
			// anchor-tree mutation; a moved commit is decided by its own tree's
			// engagement, so it resolves again.
			commitTree := ""
			base := sessionWorktree()
			if base == "" {
				base = sessionAnchor()
			}
			if d := gitCommandDir(command, base); d != "" {
				commitTree = treeOf(d)
			} else {
				commitTree = pushBranchTree(command, base)
			}
			if !resolved || commitTree != "" {
				var err error
				sid = bindSessionID(raw)
				info, engaged, live, err = resolveEngagementsFor(sid, commitTree)
				if err != nil {
					return denyPreToolUse(cmd, raw, "satelle: "+err.Error())
				}
			}
			if needsEngage && !engaged {
				// Stories performing from other trees make this one unattributed,
				// not unengaged: say so instead of sending it to engage another
				// story (sty_fbbb4aee AC2). The fused form keeps its split-the-call
				// text.
				if len(live) > 0 && !isFusedEngageAndCommit(command) {
					tree := commitTree
					if tree == "" {
						tree = sessionWorktree()
					}
					return denyPreToolUse(cmd, raw, unattributedEditReason(live, tree))
				}
				// Deny only — never allow a fused engage+commit form. PreToolUse cannot
				// know the engage line would succeed; pick the message that teaches the
				// recovery path (sty_577d292f).
				return denyPreToolUse(cmd, raw, commitDenyReason(command))
			}
			// Step-scoped policy (opt-in): engaged story must be at an allowed status.
			if engaged {
				st := info.StoryStatus
				if st == "" {
					st = info.State
				}
				if reason, deny := commandAllowDenyWith(allowPolicy, subs, st); deny {
					// Name the story the command was judged against: with several
					// stories performing, the tree decided which (sty_42231b74).
					return denyPreToolUse(cmd, raw, reason+" — "+formatEngagement(info))
				}
			}
			return nil
		},
	}

	prompt := &cobra.Command{
		Use:   "prompt",
		Short: "UserPromptSubmit reinforcement — re-inject the edits-require-a-story rule + a gate-liveness self-check",
		Long: `prompt is the UserPromptSubmit handler. With no story engaged from this
tree it re-injects a CONCISE reminder that edits require an engaged story
(the full principle rides at SessionStart). With an engaged story with a
forward route advance, that reminder is REPLACED by the engaged form — story id, status, next
gate(s) and the satelle story set command — derived from the governing route
(sty_e16a2cd7). It ALSO runs a gate-liveness SELF-CHECK: it reads the repo's
committed hook settings (.claude/settings.json / .grok/hooks/satelle.json) and,
when it can confidently see that NO PreToolUse Edit-matcher hook invokes
'satelle hook gate', it PREPENDS a LOUD warning that enforcement is not wired
(sty_949e8739). Fails OPEN: any read/resolve error injects only the reminder and
never blocks the prompt. Output is the Claude-shaped hookSpecificOutput/
additionalContext envelope for both harnesses (AC7 finding on sty_e16a2cd7).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			_ = bindSessionID(raw)
			// A user prompt starts a fresh Stop-continuation count, and for a
			// harness that discards this hook's context the verdict is not put
			// here (sty_eac9b28d).
			w := resumeWakeFor(raw)
			w.newTurn()
			return runHookPromptWith(cmd.OutOrStdout(), w.promptCarriesGates())
		},
	}
	stopcheck := &cobra.Command{
		Use:   "stopcheck",
		Short: "Stop post-hoc detector — block finishing when the tree was edited ungated (no engaged story)",
		Long: `stopcheck is the Stop handler. It catches the incident the PreToolUse gate is
meant to prevent even if that gate never fired this session: on Stop, if the tree
has uncommitted, NON-EXEMPT changes while NO story is engaged from this tree, it
emits a Stop block ({"decision":"block","reason":…}) naming the ungated files
(sty_949e8739; top-level decision/reason, sty_5e4bc568 AC6). It honours
stop_hook_active (never re-blocks its own block) and fails OPEN when git is
absent, the tree is clean, only exempt paths changed, or a story is engaged
from this tree.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			// Bind identity (and, unless this process is a dispatch, publish the
			// in-loop model) before the stop question — the Stop event is one of
			// the three the scaffold now names --harness on (sty_719c4a7b AC1/AC2),
			// and stays fail-open: bindSessionID never errors.
			_ = bindSessionID(raw)
			return runHookStopcheck(raw, cmd.OutOrStdout())
		},
	}

	turnend := &cobra.Command{
		Use:    "turnend",
		Hidden: true,
		Short:  "StopFailure / SessionEnd handler — record that the harness ended the turn on its own",
		Long: `turnend is the handler for the events a harness fires when it ends a turn
without a Stop hook: a turn that failed (StopFailure) or the session itself
(SessionEnd). It records the turn as closed and hands every gate of the session
that is still undelivered to the resume watcher, which waits for it to finish
and resumes the same session with the verdict. It prints nothing and never
blocks: neither event can be continued. A harness with no resume path, and a
dispatched process, are left alone.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			_ = bindSessionID(raw)
			resumeWakeFor(raw).closeTurn()
			return nil
		},
	}
	turnend.Flags().StringVar(&hookHarnessFlag, "harness", "", "claude|grok|pi —the harness that fired the event (default: sniff event)")

	// Explicit harness for deny shape (sty_9e86f407): wrapper forwards
	// --harness claude|grok|pi|cursor; empty falls back to harnessFromEvent. pi
	// takes the claude deny envelope (agentcli.PreToolUseDeny gives every harness
	// without its own shape that).
	gate.Flags().StringVar(&hookHarnessFlag, "harness", "", "claude|grok|pi|cursor —deny envelope (default: sniff event)")
	commitgate.Flags().StringVar(&hookHarnessFlag, "harness", "", "claude|grok|pi|cursor —deny envelope (default: sniff event)")
	// Same explicit --harness on prompt/stopcheck (sty_719c4a7b AC2): the
	// installed hook names its own harness rather than relying only on the
	// event sniff, so bindSessionID's in-loop publish stays correct even if a
	// future payload shape changes.
	prompt.Flags().StringVar(&hookHarnessFlag, "harness", "", "claude|grok|pi —in-loop publish (default: sniff event)")
	stopcheck.Flags().StringVar(&hookHarnessFlag, "harness", "", "claude|grok|pi|cursor —in-loop publish and Stop output shape (default: sniff event)")
	stopcheck.Flags().BoolVar(&hookNoWakeFlag, "no-wake", false, "the run ends at settle: wait for no gate and record a delivery limitation for each undelivered one (a harness that cannot hold its stop)")
	explain := &cobra.Command{
		Use:   "explain",
		Short: "Show how the PreToolUse model rule would decide for a payload",
		Long: `explain is read-only: it prints which transcript was chosen, by which key,
the model found, the matched glob, any no-implement carve-out, and the decision.
It changes nothing. A denied agent can run it to see why.`,
		Args: cobra.NoArgs,
		RunE: runHookExplain,
	}
	explain.Flags().String("payload", "", "PreToolUse JSON file (`-` reads stdin)")
	_ = explain.MarkFlagRequired("payload")
	hook.AddCommand(context, gate, commitgate, prompt, stopcheck, turnend, explain, newHookResumeCommand())
	register(hook)
}

func runHookExplain(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("payload")
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}
	caller := resolveCaller(raw, osCallerFS{})
	proc, invoking, _, _, loadErr := config.LoadInvokingProcess()
	if loadErr != nil {
		proc = config.Config{}
		invoking = ""
	}
	target := filePathFromEvent(raw)
	if target != "" && invoking != "" {
		target = resolveAbsTarget(invoking, target)
	}
	d, glob, exempt, reason := evaluateNoImplement(caller, target, invoking, proc)
	_, _ = fmt.Fprint(cmd.OutOrStdout(), formatHookExplain(caller, d, glob, exempt, reason))
	return nil
}

// hookHarnessFlag is set by gate/commitgate --harness (wrapper forwards the
// scaffold harness token). Empty → harnessFromEvent fallback.
var hookHarnessFlag string

// hookNoWakeFlag is set by stopcheck --no-wake: the harness adapter says this
// run ends at settle, where nothing can wake it, so the Stop hook waits for no
// gate and records a limitation for each undelivered one instead.
var hookNoWakeFlag bool

func sessionIDFromHook(raw []byte) string {
	if id := agentcli.HookSessionID(raw); id != "" {
		return id
	}
	var ev struct {
		SessionID      string `json:"session_id"`
		SessionIDCamel string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return ""
	}
	for _, s := range []string{ev.SessionID, ev.SessionIDCamel} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// bindSessionID is the hook identity: prefer SATELLE_SESSION (dispatch and
// operator stamp) so a performer inherits the driver's session; otherwise take
// the harness payload and publish it for a later engage in this process tree.
//
// The identity is always bound — a dispatched performer still needs the
// session — but the in-loop MODEL publish is skipped for a dispatched process
// (sty_719c4a7b AC4): SATELLE_SESSION is inherited from the driver that
// dispatched it, so an unguarded publish here would overwrite the DRIVER's own
// in-loop file with the dispatch's harness/model instead of recording
// anything about the dispatch itself.
func bindSessionID(raw []byte) string {
	dispatched := isDispatchedProcess()
	if id := config.SessionFromEnv(); id != "" {
		config.PublishSession(id)
		if !dispatched {
			publishInLoopModel(raw, id)
			publishServeRoot(id)
		}
		return id
	}
	if id := sessionIDFromHook(raw); id != "" {
		config.PublishSession(id)
		if !dispatched {
			publishInLoopModel(raw, id)
			publishServeRoot(id)
		}
		return id
	}
	return config.ResolveSession()
}

// isDispatchedProcess reports whether this process is a satelle-dispatched
// agent/reviewer/live-session/step-summary rather than the in-loop driving
// session (sty_719c4a7b AC4): agentstep.Invoke and OpenSessionAsWithModel set
// SATELLE_DISPATCH_AGENT/STEP/ITEM on every child they spawn, and every
// request satelle's buildRequest assembles — including a step-summary run,
// which carries no agent/step/item three-tuple of its own — also carries
// SATELLE_DISPATCH_SPAWN (config.SpawnEnv). Either marker sits alongside the
// inherited SATELLE_SESSION stamp bindSessionID otherwise treats as this
// process's own identity.
func isDispatchedProcess() bool {
	for _, k := range []string{config.DispatchAgentEnv, config.DispatchStepEnv, config.DispatchItemEnv, config.SpawnEnv} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

// publishInLoopModel captures this hook invocation's caller model as the
// in-loop session tier config.SelectModel's inherited/creator resolution
// reads (sty_7069bced / epic:model-selection order:3). Runs on every hook
// invocation that (re)binds a session identity — unconditional, not gated
// behind gate.no_implement_models the way resolveCaller's ONLY other caller is.
//
// Harness is derived the same way a PreToolUse deny already picks one
// (hookHarnessFlag, else harnessFromEvent sniffing the event envelope) —
// never hardcoded to "claude", so a wrapper or a non-Claude harness is not
// misreported as Claude's own executable (architecture review advisory,
// sty_7069bced). Best-effort and fail-open: an unresolvable model publishes
// "unknown" (plus an adapter-named reason, sty_719c4a7b AC6) rather than
// skipping the write, so the resolver sees a definite "nothing reported"
// instead of stale/absent data.
func publishInLoopModel(raw []byte, sessionID string) {
	h := hookHarnessFlag
	if h == "" {
		h = harnessFromEvent(raw)
	}
	model, reason := resolveInLoopModel(raw, h)
	config.PublishSessionModel(sessionID, verb.SessionModelRoleInLoop, model, h, reason)
}

// resolveInLoopModel is publishInLoopModel's pure half: the caller's reported
// model when resolveCaller finds one, else the explicit "unknown" marker plus
// the adapter-named reason from agentcli's capability table for harness
// (sty_719c4a7b AC6) — never a silent zero, never a Claude default
// (satelle-agent-agnostic §2).
func resolveInLoopModel(raw []byte, harness string) (model, reason string) {
	if m := strings.TrimSpace(resolveCaller(raw, osCallerFS{}).Model); m != "" {
		return m, ""
	}
	return "unknown", agentcli.ReasonForNoModel(harness)
}

// pushBranchTree is the working tree an unmoved `git push <remote> <branch>`
// publishes from: the worktree of base's repository that has the pushed branch
// checked out, so a session driving stories in several worktrees attributes the
// push to the story engaged from the one it publishes. "" when the push names no branch, several
// that sit in different trees, or one no worktree has checked out, so the tree the
// hook runs in decides as it always did.
func pushBranchTree(command, base string) string {
	branches := gitPushBranches(command)
	if len(branches) == 0 || strings.TrimSpace(base) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tree := ""
	for _, ref := range branches {
		co := worktree.CheckoutOf(ctx, base, ref)
		if co == "" || (tree != "" && !sameTree(tree, co)) {
			return ""
		}
		tree = co
	}
	return treeOf(tree)
}

// sameTree reports whether two git working-tree paths name one directory.
func sameTree(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// sessionWorktreeMemo holds sessionWorktree's answer for one hook call. The
// engagement read asks for it several times (once per tree it attributes),
// and each ask would otherwise start `git rev-parse --show-toplevel`.
// Off unless held, so nothing outside a hook call sees a stale answer.
var sessionWorktreeMemo struct {
	mu      sync.Mutex
	holders int
	set     bool
	tree    string
}

// holdSessionWorktree memoises sessionWorktree until the returned release is
// called. A hook call does not change directory, so the answer cannot move.
func holdSessionWorktree() (release func()) {
	sessionWorktreeMemo.mu.Lock()
	sessionWorktreeMemo.holders++
	sessionWorktreeMemo.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			sessionWorktreeMemo.mu.Lock()
			defer sessionWorktreeMemo.mu.Unlock()
			if sessionWorktreeMemo.holders--; sessionWorktreeMemo.holders == 0 {
				sessionWorktreeMemo.set, sessionWorktreeMemo.tree = false, ""
			}
		})
	}
}

// sessionWorktree resolves the git working tree this hook process runs in.
// Empty when git cannot answer — callers then fall back rather than guess.
var sessionWorktree = func() string {
	sessionWorktreeMemo.mu.Lock()
	held, set, tree := sessionWorktreeMemo.holders > 0, sessionWorktreeMemo.set, sessionWorktreeMemo.tree
	sessionWorktreeMemo.mu.Unlock()
	if set {
		return tree
	}
	tree = sessionWorktreeUncached()
	if held {
		sessionWorktreeMemo.mu.Lock()
		if sessionWorktreeMemo.holders > 0 {
			sessionWorktreeMemo.set, sessionWorktreeMemo.tree = true, tree
		}
		sessionWorktreeMemo.mu.Unlock()
	}
	return tree
}

func sessionWorktreeUncached() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	// Inside a hook call the repo identity is already resolved by the one
	// `git rev-parse --git-common-dir`; the toplevel comes from it and the
	// `.git` entry above dir, so the engagement read starts no process of its own. Only
	// a tree that identity cannot confirm asks git.
	if tree, ok := config.WorktreeOfMemoised(dir); ok {
		return tree
	}
	out, gerr := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if gerr != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// dispatchMarker identifies an isolated performer spawned by the workflow.
// Environment markers are deliberately an honest-posture boundary rather than
// a sandbox: the harness hook is the enforcement point, and bypassing/spoofing
// that hook remains visible operator misconduct.
type dispatchMarker struct {
	Agent string
	Step  string
	Item  string
}

func currentDispatchMarker() dispatchMarker {
	return dispatchMarker{
		Agent: os.Getenv(config.DispatchAgentEnv),
		Step:  os.Getenv(config.DispatchStepEnv),
		Item:  os.Getenv(config.DispatchItemEnv),
	}
}

// relayMarker identifies a rework-relay coder spawn to the PreToolUse hooks
// (sty_7567f047). Distinct from dispatchMarker: that branch requires InFlight,
// and the relay runs at a committed status. Honest-posture boundary — same as
// SATELLE_DISPATCH_*: a process that spoofs the env is outside the contract.
type relayMarker struct {
	Binding string
	Item    string
}

func currentRelayMarker() relayMarker {
	return relayMarker{
		Binding: os.Getenv(config.RelayBindingEnv),
		Item:    os.Getenv(config.RelayItemEnv),
	}
}

// hookEditPermitted is the PreToolUse permission predicate for gate and
// commitgate. A non-empty relay marker takes the allocated-binding rule at a
// committed status (dispatchedPerformerPermitted); otherwise today's
// editPermitted branches apply unchanged for the driving session and for
// in-flight dispatch.
func hookEditPermitted(info engagement, dm dispatchMarker, rm relayMarker) bool {
	if rm.Binding != "" {
		return info.ItemID == rm.Item && dispatchedPerformerPermitted(info, rm.Binding)
	}
	return editPermitted(info, dm)
}

// editPermitted separates engagement from source-edit authorization.
// A dispatched performer is allowed only for the exact in-flight item/target
// and authored agent allocation. The driving session is allowed only in a
// committed route step allocated to the in-loop executor, never mid-transition.
func editPermitted(info engagement, marker dispatchMarker) bool {
	if info.ItemID == "" || !info.Engaged {
		return false
	}
	if marker.Agent != "" || marker.Step != "" || marker.Item != "" {
		agents := info.DispatchAgents[marker.Step]
		if len(agents) == 0 {
			// Compatibility for legacy/derived pure callers that do not carry a
			// parsed-spec dispatch map.
			target := info.TargetState
			if target == "" {
				target = info.State
			}
			if marker.Step == target {
				agents = []string{info.StateAgent}
			}
		}
		if marker.Item != info.ItemID {
			return false
		}
		if info.InFlight {
			return slices.Contains(agents, marker.Agent)
		}
		// Not mid-transition: the dispatch entered its step and the status has
		// committed (or the in-flight mark aged out under a long run). The status is
		// settled, so the performer the route allocates to that committed step may
		// edit — the same rule the rework relay's coder gets (sty_7f3e6fd3).
		status := info.StoryStatus
		if status == "" {
			status = info.State
		}
		return marker.Step == status && dispatchedPerformerPermitted(info, marker.Agent)
	}
	return !info.InFlight && info.EditCapable
}

// dispatchedPerformerPermitted reports whether BINDING may mutate the tree from
// a live session satelle itself opened on the story — the rework relay's coder
// (sty_8e0b29a0). It is a third branch of the same policy, not a fork of it.
//
// editPermitted cannot answer this question. With an empty marker it takes the
// driving-session branch, whose EditCapable is Spec.IsEditCapableState — true
// only for agent="executor" — and OpenSession refuses an in-loop binding, so a
// relay coder is ALWAYS a named live binding and would be denied every mutator
// in exactly the configuration the relay exists for. With a marker it requires
// InFlight, and the relay runs at a committed status, not mid-transition.
//
// So the rule is stated directly: a performing story that is NOT
// mid-transition, whose COMMITTED status the route allocates to that binding.
// The allocation is read from info.DispatchAgents — route-authored, never
// compiled in — and for binding "executor" it agrees with EditCapable by
// construction, so the in-loop policy is unchanged.
func dispatchedPerformerPermitted(info engagement, binding string) bool {
	if binding == "" || info.ItemID == "" || !info.Engaged || info.InFlight {
		return false
	}
	status := info.StoryStatus
	if status == "" {
		status = info.State
	}
	agents := info.DispatchAgents[status]
	if len(agents) == 0 && status == info.State {
		// Same compatibility path editPermitted takes for legacy/derived pure
		// callers that carry no parsed-spec dispatch map.
		agents = []string{info.StateAgent}
	}
	return slices.Contains(agents, binding)
}

// waitsOnOpenChildren reports whether it is a container idling at a step its
// route declares `waits_on_children` while at least one child is still open
// (sty_7f3e6fd3). Such a story holds a status but performs nothing, so it is
// not performing: no edit is allowed under it, it counts against no
// concurrency limit, and it is not named in a deny message.
//
// A child is resolved when its OWN route says terminal, or that it sits in a
// cancel sink; a parked child (blocked) is still open. A child whose workflow
// cannot be resolved does not keep the container waiting — the container then
// counts as performing, exactly as it did before this rule. Which step waits is
// the route's declaration; no status or category name appears here.
func waitsOnOpenChildren(it workitem.Item, status string, spec wfdot.Spec, items []workitem.Item, wfs []docindex.Doc) bool {
	return wfgovern.WaitsOnOpenChildren(it, status, spec, items, wfs)
}

func dispatchAgents(spec wfdot.Spec) map[string][]string {
	out := make(map[string][]string, len(spec.States))
	for _, state := range spec.States {
		// Only the SPINE allocation dispatches (sty_05a5e203): entry no longer
		// fires an agent, so a state allocates at most one dispatch target.
		if state.Agent != "" && !slices.Contains(out[state.Name], state.Agent) {
			out[state.Name] = append(out[state.Name], state.Agent)
		}
	}
	return out
}

// scheduleLine names the child schedule of an epic container and the
// command that answers who may start. It formats a string only: it never reads
// children, never derives a wave and never names a child — which child starts is
// `satelle story wave`'s answer, not this line's. The worktree clause is stated
// only for the parallel schedule, where it applies.
func scheduleLine(id, schedule string) string {
	if schedule == "" {
		return ""
	}
	line := fmt.Sprintf("epic %s schedule: %s — the set that may be engaged is `satelle story wave %s`; a non-zero wave is a stop, not a licence to choose a child.", id, schedule, id)
	if schedule == wfdot.SchedParallel {
		line += " A parallel wave is one worktree per child; an edit is attributed to the story engaged from its tree."
	}
	return line
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

// sessionAnchor returns the pinned session-home repo root for containment and
// edit-gate path checks (sty_aadd4d6c). Precedence: SATELLE_PROJECT_DIR, then
// CLAUDE_PROJECT_DIR (harness pin), then RepoRootFromConfigPath of config.Load.
// Never uses live shell CWD alone — CWD moves with the shell.
func sessionAnchor() string {
	cfgRoot := ""
	if _, cfgPath, err := config.Load(""); err == nil {
		cfgRoot = config.RepoRootFromConfigPath(cfgPath)
	}
	return anchorFrom(os.Getenv, cfgRoot)
}

// anchorFromEnv is the env-pinned part of the anchor, "" when no pin is set. It
// is the one answer to "which repo is this session anchored in", independent of
// the working directory: a gate hand-off uses it to tell the repo whose hooks
// serve the session from the repo the command happens to act on (sty_8f10499d).
func anchorFromEnv(getenv func(string) string) string {
	for _, key := range []string{"SATELLE_PROJECT_DIR", "CLAUDE_PROJECT_DIR"} {
		if p := strings.TrimSpace(getenv(key)); p != "" {
			if abs, err := filepath.Abs(p); err == nil {
				return filepath.Clean(abs)
			}
			return filepath.Clean(p)
		}
	}
	return ""
}

// anchorFrom is the pure resolver for sessionAnchor. getenv is injected for tests.
// An env pin wins over cfgRoot because config.Load walks up from CWD and is not
// trustworthy alone once a persistent shell has cd'd.
func anchorFrom(getenv func(string) string, cfgRoot string) string {
	if p := anchorFromEnv(getenv); p != "" {
		return p
	}
	if strings.TrimSpace(cfgRoot) == "" {
		return ""
	}
	if abs, err := filepath.Abs(cfgRoot); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(cfgRoot)
}

// commandAllowRestrictsWith reports whether any git subcommand is listed in the
// opt-in [gate.command_allow] policy (even if not commit/push).
func commandAllowRestrictsWith(policy map[string][]string, subs []string) bool {
	if len(policy) == 0 {
		return false
	}
	for _, sub := range subs {
		if _, ok := policy[strings.ToLower(sub)]; ok {
			return true
		}
	}
	return false
}

// commandAllowDenyWith returns a deny reason when a restricted subcommand is not
// permitted at the engaged story's current status. deny=false when allowed or
// unconfigured.
func commandAllowDenyWith(policy map[string][]string, subs []string, storyStatus string) (reason string, deny bool) {
	if len(policy) == 0 {
		return "", false
	}
	status := strings.ToLower(strings.TrimSpace(storyStatus))
	for _, sub := range subs {
		key := strings.ToLower(sub)
		allowed, restricted := policy[key]
		if !restricted {
			continue
		}
		if len(allowed) == 0 {
			// Key present with empty list = never allowed while policy is on.
			return fmt.Sprintf(
				"satelle: refusing git %s — [gate.command_allow] lists %q with no allowed states (remove the key or name permitted story statuses, e.g. push = [\"release\"])",
				sub, key), true
		}
		ok := false
		for _, a := range allowed {
			if strings.ToLower(strings.TrimSpace(a)) == status {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Sprintf(
				"satelle: refusing git %s while engaged story is at %q — [gate.command_allow] permits it only at: %s",
				sub, storyStatus, strings.Join(allowed, ", ")), true
		}
	}
	return "", false
}

// loadCommandAllow returns the opt-in [gate.command_allow] map (nil/empty = off).
func loadCommandAllow() map[string][]string {
	return commandAllowFrom(loadGateProcess())
}

// gateProcess is one load of the invoking process config. LoadInvokingProcess
// resolves the canonical repo root through `git rev-parse`, so a hook call loads
// it once and every check in that call reads this value (sty_a74551d6 AC5).
type gateProcess struct {
	proc     config.Config
	invoking string
	path     string
	err      error
	// loc, locPath and procRoot are the other halves of the same load, so the
	// store can open from it (gateStore) instead of loading the config again.
	loc      config.Config
	locPath  string
	procRoot string
}

// loadGateProcess is config.LoadInvokingProcess, keeping the location config
// and the process root it computed on the way.
func loadGateProcess() gateProcess {
	loc, locPath, lerr := config.Load("")
	if lerr != nil && !errors.Is(lerr, config.ErrNotFound) {
		return gateProcess{path: locPath, err: lerr}
	}
	proc, procRoot, path, err := config.LoadProcess(loc, locPath)
	if err != nil {
		return gateProcess{path: path, err: err}
	}
	return gateProcess{
		proc: proc, invoking: config.InvokingRoot(locPath), path: path,
		loc: loc, locPath: locPath, procRoot: procRoot,
	}
}

// gateStore is the one store a commitgate call opens for command rules, from the
// gateProcess already loaded: the engagement read and the ledger row share it, so a
// matched command adds store I/O and no second config load or process.
type gateStore struct {
	gp gateProcess
	// load makes the first open load the process config itself (a call that may
	// never need the store, such as the edit gate's skipped tools, pays nothing).
	load   bool
	a      *app.App
	err    error
	opened bool
}

func (g *gateStore) open() (*app.App, error) {
	if !g.opened {
		g.opened = true
		if g.load {
			g.gp = loadGateProcess()
		}
		if g.gp.err != nil {
			g.err = g.gp.err
		} else {
			g.a, g.err = app.OpenLoaded(g.gp.loc, g.gp.locPath, g.gp.proc, g.gp.procRoot)
		}
	}
	return g.a, g.err
}

// engagementsFor is resolveEngagementsFor on the shared store.
func (g *gateStore) engagementsFor(sessionID, targetTree string) (info engagement, engaged bool, live []engagement, err error) {
	a, oerr := g.open()
	if oerr != nil {
		return engagementOpenFailed(oerr)
	}
	return resolveEngagementsOn(a, sessionID, targetTree)
}

// resolve is resolveEngagements on the shared store.
func (g *gateStore) resolve(sessionID string) (engagement, bool, error) {
	info, engaged, _, err := g.engagementsFor(sessionID, "")
	return info, engaged, err
}

func (g *gateStore) close() {
	if g.a != nil {
		_ = g.a.Close()
		g.a = nil
	}
}

func commandAllowFrom(gp gateProcess) map[string][]string {
	proc, err := gp.proc, gp.err
	if err != nil || len(proc.Gate.CommandAllow) == 0 {
		return nil
	}
	// Normalize keys to lowercase for lookup.
	out := make(map[string][]string, len(proc.Gate.CommandAllow))
	for k, v := range proc.Gate.CommandAllow {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

// allowOutsideTreeEdits reports whether [gate] allow_outside_tree_edits is true.
// Default false = deny mutations in another repo's working tree (sty_a8454d10).
// Non-repo paths are never fenced. On config load failure, returns false
// (containment stays on).
func allowOutsideTreeEdits() bool {
	return allowOutsideTreeEditsFrom(loadGateProcess())
}

func allowOutsideTreeEditsFrom(gp gateProcess) bool {
	if gp.err != nil {
		return false
	}
	return gp.proc.Gate.AllowOutsideTreeEdits
}

// outsideAnchorBashReason is the agent-facing deny when a Bash mutation target
// lands in another repo's working tree (sty_a8454d10 / sty_aadd4d6c). Names the
// path and the foreign root; prescribes opening a session in THAT repo.
func outsideAnchorBashReason(path, foreignRoot string) string {
	return fmt.Sprintf(
		"satelle: refusing Bash mutation in another repo's tree (%s, root %s) — open a session in THAT repo to action it there (create stories cross-repo is fine; progressing/mutating is not). Temp/non-repo paths are not fenced. Opt-in only for a deliberate multi-repo install: [gate] allow_outside_tree_edits = true",
		path, foreignRoot)
}

// noEngagedStoryEditReason is the canonical agent-facing deny for product-code
// edits without a performing story (sty_e4902c51). Both Claude and Grok must
// surface this text — not a bare "hook denied (exit 2)".
const noEngagedStoryEditReason = "satelle: you're mutating the tree without a performing story, or you have used the wrong tool for reading. " +
	"Open a story session before editing code: satelle story create …, then satelle story set <id> --status plan. " +
	"That session stays open through your edits until the story reaches a terminal or parked state (done, cancelled, or blocked) — finishing an edit does NOT close it. " +
	"For research, use read tools (Read/read_file/grep/Glob) — not Edit/Write/search_replace."

const readOnlyPreflightBase = "Read-only preflight remains available: use Read/read_file/Grep/Glob or non-mutating shell commands, and record approved context with `satelle story attach` or `satelle story log`; advance through the workflow gate before editing."

// readOnlyPreflightReason names only drafting locations the live [gate]
// config actually allows. Config load failure or an empty exemption set
// promises nothing.
func readOnlyPreflightReason() string {
	return readOnlyPreflightReasonFrom(func() (config.Config, string, error) {
		proc, invoking, _, _, err := config.LoadInvokingProcess()
		if err != nil {
			return config.Config{}, "", err
		}
		// The seam derives the root with RepoRootFromConfigPath, which walks
		// two directories up. A path under the invoking tree joins exemptions
		// there. The config itself is the process of record.
		return proc, filepath.Join(invoking, config.DefaultDataDir, config.ConfigName), nil
	})
}

func readOnlyPreflightReasonFrom(load func() (config.Config, string, error)) string {
	cfg, cfgPath, err := load()
	if err != nil {
		return readOnlyPreflightBase + " No [gate] exemptions could be read — every in-repo path is gated."
	}
	root := config.RepoRootFromConfigPath(cfgPath)
	var locs []string
	seen := map[string]bool{}
	addLoc := func(p string) {
		if p == "" || seen[p] {
			return
		}
		if root != "" && withinRoot(root, p) {
			return
		}
		seen[p] = true
		locs = append(locs, p)
	}
	for _, p := range cfg.ResolveEditExemptPaths(root) {
		addLoc(p)
	}
	// Process temp is a mechanism drafting location, independent of the
	// authored list — keep the deny advice truthful when a custom list omits /tmp/.
	for _, p := range tempDraftRoots() {
		addLoc(p)
	}
	locs = append(locs, cfg.ResolveEditExemptGlobs()...)
	if len(locs) == 0 {
		return readOnlyPreflightBase + " No [gate] exemptions are authored in this repo — every in-repo path is gated."
	}
	return readOnlyPreflightBase + " Story-reference copies belong in " + strings.Join(locs, ", ") + "."
}

func editPermissionDenyReason(info engagement, live []engagement, tree string) string {
	pre := readOnlyPreflightReason()
	if info.ItemID == "" || !info.Engaged {
		return editGateDenyReason(live, tree) + " " + pre
	}
	if info.InFlight {
		target := info.State
		if target == "" {
			target = "the next state"
		}
		if info.Mine {
			return fmt.Sprintf(
				"satelle: story %s has a transition to %q IN FLIGHT (planner/reviewer or dispatched step running); the driving session cannot edit until the transition commits. %s",
				info.ItemID, target, pre)
		}
		return fmt.Sprintf(
			"satelle: story %s has a transition to %q IN FLIGHT (planner/reviewer or dispatched step running). %s",
			info.ItemID, target, pre)
	}
	agent := info.StateAgent
	states := strings.Join(info.EditStates, ", ")
	if states == "" {
		states = "(none declared)"
	}
	if agent == "" {
		return fmt.Sprintf(
			"satelle: story %s is at %q, which its workflow allocates to %q; source edits are permitted only in route steps allocated to agent=executor (%s). Do not work ahead. %s",
			info.ItemID, info.StoryStatus, "no in-loop executor", states, pre)
	}
	// A named performer owns this step: the driver's own edit is refused, and
	// the text names the one path that reaches that performer. The relay is
	// named only when the step declares a rework loop — naming it on a step
	// with none sends the driver at a command that refuses (sty_a7914904).
	path := fmt.Sprintf("the one-shot coder dispatch of %q performs this step — advance the edge (`satelle story set %s --status <next>`) to dispatch it", agent, info.ItemID)
	if info.StateRework {
		path = fmt.Sprintf("relay the change through the dispatched coder %q with `satelle story rework %s`", agent, info.ItemID)
	}
	return fmt.Sprintf(
		"satelle: story %s is at %q, which its workflow allocates to %q; source edits are permitted only in route steps allocated to agent=executor (%s). Do not edit in-loop — %s. Do not work ahead. %s",
		info.ItemID, info.StoryStatus, agent, states, path, pre)
}

// stepDeclaresRework reports whether the derived route declares a rework loop
// on step.
func stepDeclaresRework(reworks []wfroute.Rework, step string) bool {
	for _, w := range reworks {
		if w.Step == step {
			return true
		}
	}
	return false
}

// hookDenyReason selects the agent-facing deny text for gate/commitgate.
// A marked relay coder gets relayDenyReason; unmarked sessions keep
// editPermissionDenyReason byte-for-byte (sty_7567f047 AC5).
func hookDenyReason(info engagement, live []engagement, dm dispatchMarker, rm relayMarker, tree string) string {
	if rm.Binding != "" {
		return relayDenyReason(info, rm)
	}
	// A dispatched performer whose item is not performing is refused for THAT,
	// not for whichever other story the session happens to resolve
	// (sty_8d7d1c45). It never borrows a sibling's permission.
	if dm.Item != "" && info.ItemID != dm.Item {
		return dispatchDenyReason(dm)
	}
	return editPermissionDenyReason(info, live, tree)
}

// dispatchDenyReason names a dispatched performer whose item is not performing:
// the dispatch is the identity, so the reason names it.
func dispatchDenyReason(dm dispatchMarker) string {
	return fmt.Sprintf(
		"satelle: dispatch %s/%s (agent %q) has no open transition — a dispatched performer may edit only for the item it was dispatched for, while that item performs the step it was dispatched to. %s",
		dm.Item, dm.Step, dm.Agent, readOnlyPreflightReason())
}

// noEngagedStoryCommitReason is the agent-facing deny for git commit/push without
// an engaged story. Same harness-specific emission as the edit gate. States
// PreToolUse pre-execution semantics so agents do not retry a fused engage+commit
// in one tool call (sty_577d292f / session-trace-workflow-review-followups).
const noEngagedStoryCommitReason = "satelle: refusing to commit/push with no engaged story. " +
	"This gate runs BEFORE the command executes — an engage line inside the same tool call cannot pass it. " +
	"Engage in a SEPARATE, PRIOR tool call (satelle story set <id> --status plan or --status in_progress, per the governing workflow), then run git commit/push in a later call."

// fusedEngageAndCommitReason is the deny when the payload both tries to engage
// a story and run git commit/push. Still a deny (behavior unchanged); the text
// explicitly says to split into two tool calls (sty_577d292f optional variant).
const fusedEngageAndCommitReason = "satelle: refusing fused engage+commit/push in one tool call. " +
	"commitgate evaluates BEFORE any line runs, so a same-command 'satelle story set … --status …' cannot engage for this commit/push. " +
	"Split into TWO tool calls: (1) engage alone (status plan or in_progress per the governing workflow); (2) git commit/push only after engagement succeeds."

// commitDenyReason picks the agent-facing deny text for a blocked commit/push.
// Pure string selection — gate behavior is always deny when nothing is engaged.
func commitDenyReason(command string) string {
	if isFusedEngageAndCommit(command) {
		return fusedEngageAndCommitReason
	}
	return noEngagedStoryCommitReason
}

// outsideRepoEditReason is the agent-facing refusal when a PreToolUse edit
// targets a path inside another repo's working tree (sty_a8454d10). Kept as a
// pure string so harness-specific deny emission and unit tests share one stable
// message. foreignRoot is the git root of the denied tree.
func outsideRepoEditReason(path, foreignRoot string) string {
	return fmt.Sprintf(
		"satelle: refusing edit in another repo's tree (%s, root %s) — open a session in THAT repo and create/engage the story there: satelle story create … then satelle story set <id> --status plan. Temp/non-repo paths are not fenced; opt-in multi-repo: [gate] allow_outside_tree_edits = true",
		path, foreignRoot)
}

// outsideRepoEditErr wraps outsideRepoEditReason as an error for tests that
// still assert .Error() on the refusal helper.
func outsideRepoEditErr(path, foreignRoot string) error {
	return fmt.Errorf("%s", outsideRepoEditReason(path, foreignRoot))
}

// denyPreToolUse emits a harness-specific deny payload on stdout and returns an
// error, so direct invocation has both structured stdout and a plain stderr
// reason. The installed wrapper consumes that result and normalises a usable
// deny to structured stdout + handler exit 0; it never discards stderr and then
// exits 2. The harness is detected from the PreToolUse event envelope (raw).
func denyPreToolUse(cmd *cobra.Command, raw []byte, reason string) error {
	_ = emitPreToolUseDeny(cmd.OutOrStdout(), commandHarness(raw), reason)
	return fmt.Errorf("%s", reason)
}

// commandHarness is the harness a PreToolUse event came from: the wrapper's
// --harness, else sniffed from the event envelope.
func commandHarness(raw []byte) string {
	if hookHarnessFlag != "" {
		return hookHarnessFlag
	}
	return harnessFromEvent(raw)
}

// harnessFromEvent classifies a hook event envelope as claude, grok or
// unknown (sty_5e4bc568, sty_37fd5470). The per-provider fingerprints live in
// agentcli.HarnessFromHookEvent. Unrecognised input is "unknown", never
// "claude"; agentcli.PreToolUseDeny still gives every harness without its own
// shape (unknown included) the strict hookSpecificOutput shape, so the deny
// stays effective and the sty_5e4bc568 inert-gate bug is not reopened.
func harnessFromEvent(raw []byte) string {
	return agentcli.HarnessFromHookEvent(raw)
}

// claudePreToolUseDenyOut aliases the adapter's Claude deny shape so a test can
// decode what the verb printed; the wire struct itself lives in agentcli.
type claudePreToolUseDenyOut = agentcli.ClaudePreToolUseDenyOut

// emitPreToolUseDeny writes one harness-correct deny JSON line to out. The
// per-harness encodings (claude, grok, cursor; everything else gets Claude's)
// live in agentcli.PreToolUseDeny.
func emitPreToolUseDeny(out io.Writer, harness, reason string) error {
	_, err := fmt.Fprintf(out, "%s\n", agentcli.PreToolUseDeny(harness, reason))
	return err
}

// hookInfraUnavailableReason is the model-visible text when the scaffolded
// PreToolUse wrapper cannot run satelle at all (sty_c75c73ed). It must never
// look like a policy denial — the agent needs to diagnose PATH/binary and run
// `satelle init` to heal, which requires Bash not to be bricked.
const hookInfraUnavailableReason = "satelle unavailable in this hook shell env — INFRASTRUCTURE failure, NOT a policy denial. " +
	"The satelle binary could not be resolved or did not produce a decision. " +
	"Try: which satelle; satelle version; satelle init. Non-mutating bash stays allowed so you can diagnose."

// infraDenyJSON returns the harness-correct PreToolUse deny JSON for an
// infrastructure failure (same shape as a real satelle deny — sty_5e4bc568).
func infraDenyJSON(harness string) string {
	var buf strings.Builder
	_ = emitPreToolUseDeny(&buf, harness, hookInfraUnavailableReason)
	return strings.TrimSpace(buf.String())
}

// exemptTarget reports whether an edit to target is exempt from the engaged-story
// gate. Exemption is configuration plus one mechanism rule: a write under the
// process temp directory is not an in-repo product edit, so it is never gated
// (sty_e33f78fe). Authored exemptions remain [gate] edit_exempt_paths prefixes
// and [gate] edit_exempt_globs filename patterns. The binary does not special-
// case the data dir or managed paths; `satelle init` seeds .satelle/ and the
// footprint it deploys itself (.gitignore block, harness scaffolds) into
// edit_exempt_paths, and story-dump names into edit_exempt_globs, so authored
// substrate and satelle-written output stay editable OOTB, but the operator
// owns those lists (sty_8c3d345c / sty_f115e6bf / sty_926cfcdc / sty_fefc88cd).
// The target is resolved to absolute against the repo root FIRST, so a
// repo-relative path (as Grok sends) is classified correctly. Returns false if
// the config/root cannot be resolved, so the gate stays conservative (still
// applies) on any resolution failure — including a temp-dir target, so a
// broken config does not fail open.
func exemptTarget(target string) bool {
	proc, invoking, _, _, err := config.LoadInvokingProcess()
	if err != nil {
		return false
	}
	abs := resolveAbsTarget(invoking, target)
	if tempDraftTarget(invoking, abs) {
		return true
	}
	if editExempt(proc.ResolveEditExemptPaths(invoking), invoking, abs) {
		return true
	}
	return editExemptPattern(proc.ResolveEditExemptGlobs(), invoking, abs)
}

// tempDraftRoots is the process temp directory (os.TempDir already honours
// TMPDIR) plus a literal /tmp when that is not already covered. A session may
// run with TMPDIR pointed elsewhere while the agent still drafts to /tmp.
func tempDraftRoots() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || p == "." || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(os.TempDir())
	add("/tmp")
	return out
}

// tempDraftTarget reports whether target is an out-of-repo write under the
// process temp directory. A target inside repoRoot is never a temp draft — a
// repo that lives under the temp dir still gates its own tree (same
// containment as editExempt). Pure: no config, no extra filesystem. Containment
// is checked before the temp-prefix match so a t.TempDir repo cannot exempt
// its whole tree.
func tempDraftTarget(repoRoot, target string) bool {
	if repoRoot != "" && withinRoot(repoRoot, target) {
		return false
	}
	for _, r := range containmentTempRoots() {
		if withinRoot(r, target) {
			return true
		}
	}
	return false
}

// editExempt is the pure classification the edit-gate exemption rests on: target
// is exempt when it resolves under any configured exempt prefix. A prefix that
// itself sits outside repoRoot (e.g. /tmp/) matches only targets also outside
// repoRoot — it never exempts in-tree product code, even when the repo lives
// under that prefix. Kept pure (no config/filesystem) so the path classification
// is unit-tested directly. Callers pass an already-absolute target (see
// resolveAbsTarget) and absolute prefixes (ResolveEditExemptPaths); blank
// prefixes are skipped — a blank prefix would make withinRoot fail open TOWARD
// inside and exempt everything, so this is the guard. An empty repoRoot
// disables the outside-tree restriction (tests of prefix-only matching).
func editExempt(exemptRoots []string, repoRoot, target string) bool {
	inRepo := repoRoot != "" && withinRoot(repoRoot, target)
	for _, r := range exemptRoots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		if repoRoot != "" && !withinRoot(repoRoot, r) && inRepo {
			continue
		}
		if withinRoot(r, target) {
			return true
		}
	}
	return false
}

// editExemptPattern reports whether target matches an authored filename glob.
// Basename match unless the pattern contains `/` (then repo-relative, slash-
// normalized). Blank and unparseable patterns are skipped — never fail-open
// toward exempt-everything (sty_fefc88cd). Kept pure so classification is
// unit-tested directly.
func editExemptPattern(patterns []string, root, target string) bool {
	for _, p := range patterns {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		subject := filepath.Base(target)
		if strings.ContainsRune(s, '/') {
			if strings.TrimSpace(root) == "" {
				continue
			}
			rel, err := filepath.Rel(root, target)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			subject = filepath.ToSlash(rel)
		}
		ok, err := filepath.Match(s, subject)
		if err != nil {
			continue
		}
		if ok {
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

// runHookContext assembles and emits the session principle set. It fails open:
// any error opening the store or listing docs injects nothing and returns nil.
// The emitting event is the harness's configured session_context_event when the
// payload names that event; a payload with no event field keeps today's
// diagnostic contract (emit, rendered event name SessionStart). A payload that
// names a different event emits nothing. Delivery on a matching channel is once
// per session|harness|channel.
func runHookContext(out, stderr io.Writer, harness string, raw []byte) error {
	a, err := app.Open()
	if err != nil {
		return nil // fail open — unconfigured repo / unopenable db blocks nothing
	}
	defer func() { _ = a.Close() }()

	channel := a.Config.SessionContextEvent(harness)
	snake, camel := hookEventNames(raw)
	present, match := eventMatchesChannel(snake, camel, channel)
	// An event is present and neither field equals the configured channel
	// (including an empty channel): emit nothing. No event field keeps the
	// diagnostic contract.
	if present && !match {
		return nil
	}
	emitEvent := "SessionStart"
	unit := "bytes"
	limit := a.Config.ContextLimit(harness)
	if match {
		emitEvent = channel
		// A harness spells its session-start event its own way (cursor sends
		// sessionStart); every spelling is the SessionStart byte budget.
		if !strings.EqualFold(channel, "SessionStart") {
			unit = "characters"
			limit = a.Config.ToolContextLimitChars(harness)
		}
	}
	sessionID := sessionContextSessionID(raw)
	marker := ""
	if match && sessionID != "" {
		marker = sessionContextMarkerPath(a, sessionID, harness, channel)
		if sessionContextMarkerExists(marker) {
			return nil
		}
	}
	// The team line (sty_4cc971f7): a bound repo's tree is brought to the
	// project tier before the context renders, and each replaced local edit is
	// named with its backup. Fail open: an error is a warning line, never a block.
	teamLine := sessionTeamWriteDown(a)
	// The release line rides in model context AND, through the harness's own
	// user channel, to the user (agentcli.SessionContextOutput below).
	releaseLine := releaseAdvisoryFor(a)
	// The substrate line (sty_f1a3851d) joins it: from local and recorded state
	// only, silent when this machine's substrate is in step.
	substrateLine := substrateAdvisoryFor(a)
	// The story-sync line (sty_6e5fc41c): conflicts and holds recorded by the
	// last work-state passes. One notice, the lines joined, none put in
	// another's place.
	storyLine := storySyncAdvisoryFor(a)
	// The command-policy line (sty_041bf275): subscribed lists that are missing,
	// stale, unverified or failed to refresh, from the local cache only.
	policyLine := policyAdvisoryFor(a)
	// The harness-drift line (sty_32c61e68): this session's CLI reports a build
	// other than the one satelle's measured behavior was captured against. Read
	// from the payload alone, so it spawns nothing and costs the hook nothing.
	driftLine := agentcli.SessionDriftLine(harness, raw)
	content, omitted := assembleSessionContext(a, harness, limit, unit, teamLine, releaseLine, substrateLine, storyLine, policyLine, driftLine)
	if len(omitted) > 0 {
		fmt.Fprintf(stderr,
			"satelle hook context: %s exceeded the %s harness limit (%d %s) — indexed with a read instruction: %s\n",
			"always-content", harness, limit, contextUnitName(unit), strings.Join(omitted, ", "))
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}
	fmt.Fprintln(out, string(agentcli.SessionContextOutput(harness, emitEvent, content, joinNotice(teamLine, releaseLine, substrateLine, storyLine, policyLine, driftLine))))
	// The marker is written only after a channel-matching emit of non-empty
	// additionalContext. A non-matching event (including SessionStart when the
	// channel is elsewhere) writes nothing. Marker errors are swallowed.
	if match && marker != "" {
		writeSessionContextMarker(marker)
	}
	return nil
}

// assembleSessionContext renders the session principle set — constitution,
// principles:session docs, advisories, engaged stories — under the budget the caller
// resolved. unit is "bytes" (len, the SessionStart path) or "characters"
// (runes, a tool-event clip). teamLine is the project-tier write-down warning;
// releaseLine is the release-currency advisory
// ("" when none), placed beside the version-drift line; substrateLine is the
// substrate-currency advisory, likewise, and storyLine the story-sync advisory
// (held and conflicted stories), and policyLine the subscribed command-policy
// advisory, and driftLine the captured-CLI-version advisory. Fail-open: a list
// error returns empty content.
func assembleSessionContext(a *app.App, harness string, limit int, unit, teamLine, releaseLine, substrateLine, storyLine, policyLine, driftLine string) (string, []string) {
	if a == nil || a.Store == nil || a.Store.DocIndex == nil {
		return "", nil
	}
	docs, err := a.Store.DocIndex.List(context.Background(), "")
	if err != nil {
		return "", nil
	}
	always := selectAlwaysDocs(docs)
	constPath := a.PlaneConstitution()
	constitution := readConstitution(constPath)

	// Everything that rides around the principles (advisories, engaged stories) is measured
	// first, so the principles are rendered into what is LEFT of the harness
	// limit and the whole injection lands inside it (sty_ce1a2733).
	//
	// Web availability (sty_fb5e6d96): ONE line naming the URL and whether
	// anything answers on it, ahead of the constitution so a new user sees where
	// the server is without asking. Fail-open: probeWebAvailability never errors
	// and renders "unknown" rather than a fabricated "live".
	webLine := probeWebAvailability().hookLine()
	// Scaffold drift (sty_ac25b787): fail-open warning — never blocks SessionStart.
	// Names `satelle init` as the heal. DetectScaffoldDrift is pure comparison.
	scaffoldWarn := formatScaffoldDriftWarning(DetectScaffoldDrift(a.RepoRoot))
	// VERSION-STAMP drift (sty_8ecdae90): a separate, one-line advisory beside
	// the scaffold block above — different trigger, different text, neither
	// replacing the other. Advisory only: versionDriftAdvisory has no error
	// channel, so `hook context` keeps its fail-open contract.
	versionLine := versionDriftAdvisory(a.RepoRoot)
	// An auto-raised diagnosis is worth nothing unread (sty_88d40a60): when the
	// indexer has filed a high-priority system story about an authored document
	// that fails its structure check, name it here. Silent when there is none.
	docStoryLine := systemDocStoryAdvisory(openDocStories(a))
	// The items this repo is performing and the concurrency rule. Fail open — a
	// read error injects nothing.
	engagedBlock := sessionEngagementBlock(a)
	wrap := func(body string) string {
		content := prependContextLine(body, webLine)
		if scaffoldWarn != "" {
			if content == "" {
				content = scaffoldWarn
			} else {
				content = scaffoldWarn + "\n\n" + content
			}
		}
		content = prependContextLine(content, versionLine)
		content = prependContextLine(content, releaseLine)
		content = prependContextLine(content, substrateLine)
		content = prependContextLine(content, storyLine)
		content = prependContextLine(content, policyLine)
		content = prependContextLine(content, driftLine)
		content = prependContextLine(content, docStoryLine)
		content = prependContextLine(content, teamLine)
		return appendBlockToContext(content, engagedBlock, 0)
	}

	measure := contextMeasure(unit)
	overhead := measure(wrap("X")) - measure("X")
	if overhead < 0 {
		overhead = 0
	}
	rules, rerr := condenseRules(a.PlaneDir())
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "satelle hook context: %s — %v\n", config.CondenseOverrideRel, rerr)
	}
	var off compact.Offloader
	if a.Store.Retrieve != nil {
		off = retrieveAdapter{ctx: context.Background(), store: a.Store.Retrieve}
	}
	content, omitted := sessionAssemblyUnit(constitution, always, constPath, harness, limit, overhead, unit, rules, off)
	content = wrap(content)
	return content, omitted
}

// hookEventNames reads both event fields a harness may send. Captured grok
// stdin puts the PascalCase name on hook_event_name and the snake_case twin on
// hookEventName. Matching is exact equality with the configured channel — the
// snake_case twin is not case-folded into a match.
func hookEventNames(raw []byte) (snake, camel string) {
	if len(raw) == 0 {
		return "", ""
	}
	var ev struct {
		Snake string `json:"hook_event_name"`
		Camel string `json:"hookEventName"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return "", ""
	}
	return ev.Snake, ev.Camel
}

// eventMatchesChannel reports whether an event field is present and whether
// either field equals channel exactly. An empty channel never matches.
func eventMatchesChannel(snake, camel, channel string) (present, match bool) {
	present = snake != "" || camel != ""
	if channel == "" {
		return present, false
	}
	return present, snake == channel || camel == channel
}

// sessionContextSessionID prefers the hook payload's session id, then the
// SATELLE_SESSION stamp. Empty means the once-marker cannot key a delivery, so
// the caller delivers.
func sessionContextSessionID(raw []byte) string {
	if id := sessionIDFromHook(raw); id != "" {
		return id
	}
	return strings.TrimSpace(config.SessionFromEnv())
}

func sessionContextMarkerPath(a *app.App, session, harness, channel string) string {
	if a == nil || strings.TrimSpace(session) == "" || strings.TrimSpace(channel) == "" {
		return ""
	}
	dir := filepath.Join(a.Config.ResolveRuntimeDir(a.RepoRoot).Dir, "session-context")
	name := session + "|" + harness + "|" + channel
	name = strings.ReplaceAll(strings.ReplaceAll(name, "/", "_"), "\\", "_")
	return filepath.Join(dir, name)
}

func sessionContextMarkerExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// writeSessionContextMarker records a delivery. Any error — including an
// unwritable runtime dir — is swallowed so the emit already written still stands.
func writeSessionContextMarker(path string) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte("1\n"), 0o644)
}

func contextMeasure(unit string) func(string) int {
	if unit == "characters" || unit == "runes" {
		return utf8.RuneCountInString
	}
	return func(s string) int { return len(s) }
}

func contextUnitName(unit string) string {
	if unit == "" {
		return "bytes"
	}
	return unit
}

// sessionAssembly renders the deterministic body of a SessionStart injection —
// constitution, then session principles, degraded to index lines past the
// harness limit — and is the one place that rule is applied. overhead is what
// the volatile lines around it (web probe, drift advisories, engaged-stories block) will
// add; `satelle validate` passes 0 to size the stable part, so the hook and the
// report cannot disagree about what a harness receives.
func sessionAssembly(constitution string, always []docindex.Doc, constPath, harness string, limit, overhead int, rules condense.Rules) (string, []string) {
	return sessionAssemblyUnit(constitution, always, constPath, harness, limit, overhead, "bytes", rules, nil)
}

// sessionAssemblyUnit is sessionAssembly with an explicit budget unit. "bytes"
// keeps the existing len path; "characters" counts runes. validate and the
// SessionStart path stay on bytes. off keeps the originals of condensed bodies;
// nil sizes the render without storing anything (validate and doctor).
func sessionAssemblyUnit(constitution string, always []docindex.Doc, constPath, harness string, limit, overhead int, unit string, rules condense.Rules, off compact.Offloader) (string, []string) {
	return renderAlwaysContent(constitution, always, alwaysRender{
		Budget: limit - overhead, Harness: harness, ConstitutionPath: constPath, Unit: unit,
		Rules: rules, Offload: off,
	})
}

// resolveContextHarness names the in-loop harness a SessionStart injection is
// for: the explicit --harness flag, else the hook payload's fingerprint, else
// the session markers in the environment. Anything unrecognised is "unknown" —
// never assumed to be Claude — and takes the neutral limit.
func resolveContextHarness(flag string, raw []byte, environ []string) string {
	if h := strings.ToLower(strings.TrimSpace(flag)); h != "" {
		return h
	}
	if h := agentcli.HarnessFromHookEvent(raw); h != agentcli.HarnessUnknown {
		return h
	}
	if h, ok := agentcli.InLoopHarnessFromEnv(environ); ok {
		return h
	}
	return agentcli.HarnessUnknown
}

// openDocStories reads the open document-diagnosis stories for a, or nothing
// when the store is unavailable — the same fail-open contract runHookContext
// keeps for every other line it renders.
func openDocStories(a *app.App) []docstory.Ref {
	if a == nil || a.Store == nil || a.Store.Stories == nil {
		return nil
	}
	return docstory.Open(context.Background(), a.Store.Stories.List)
}

// systemDocStoryAdvisory renders the one-line SessionStart advisory naming open
// stories the indexer raised about failing authored documents (sty_88d40a60).
// Pure, for unit tests. Empty for no refs, so a repo with sound substrate gains
// no output at all — the bound that keeps this from becoming noise is
// docstory.Qualifies, not a judgement made here.
//
// One line, capped at three ids: it rides ahead of the always-content ceiling,
// so it must not be able to displace principle content however broken a repo is.
func systemDocStoryAdvisory(refs []docstory.Ref) string {
	if len(refs) == 0 {
		return ""
	}
	const show = 3
	named := make([]string, 0, show)
	for _, r := range refs[:min(len(refs), show)] {
		named = append(named, fmt.Sprintf("%s (%s)", r.ID, r.Doc()))
	}
	listed := strings.Join(named, ", ")
	if len(refs) > show {
		listed += fmt.Sprintf(", … (+%d more)", len(refs)-show)
	}
	return fmt.Sprintf(
		"⚠️ satelle: %d open system stor%s already diagnos%s a failing authored document — %s. Read %s before debugging governance behaviour: it names the file and the fault.",
		len(refs), plural(len(refs), "y", "ies"), plural(len(refs), "es", "e"), listed,
		plural(len(refs), "it", "them"))
}

// prependContextLine puts a one-line SessionStart advisory at the head of the
// body (sty_fb5e6d96 for web availability; sty_8ecdae90 for version-stamp
// drift). Exactly one line of content is added — no heading, no table — so the
// always-resident cost stays a line, and an empty line is a no-op so every
// caller can hand it a value that is silent on the common path. Pure, for unit
// tests.
// sessionTeamWriteDown runs the project-tier write-down for session start and
// returns its warnings as one line ("" when there are none).
func sessionTeamWriteDown(a *app.App) string {
	res, err := writeDownProjectTier(a.ProcessConfig, a.ProcessRoot, a.RuntimeDir, a.RepoRoot, a.Config, false)
	warnings := res.Warnings()
	if err != nil {
		warnings = append(warnings, "team substrate: "+err.Error())
	}
	return strings.Join(warnings, "\n")
}

func prependContextLine(content, line string) string {
	if strings.TrimSpace(line) == "" {
		return content
	}
	if strings.TrimSpace(content) == "" {
		return line
	}
	return line + "\n\n" + content
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

// alwaysRender carries the inputs of one SessionStart render that are not the
// content itself: the byte budget the harness delivers inline, the harness it
// is for (named to the agent when content is omitted), and where the
// constitution can be read back.
type alwaysRender struct {
	Budget           int
	Harness          string
	ConstitutionPath string
	// Unit is how Budget is counted. Empty or "bytes" uses len — the SessionStart
	// path, unchanged. "characters" or "runes" uses utf8.RuneCountInString for a
	// tool-event clip. The omit header names this unit.
	Unit string
	// Rules is how a body that does not fit is condensed. Zero means the embedded
	// defaults. Offload is where the originals of condensed bodies are kept; nil
	// sizes the markers without storing anything (an estimate, not a delivery).
	Rules   condense.Rules
	Offload compact.Offloader
}

func (r alwaysRender) measure() func(string) int {
	if r.Unit == "characters" || r.Unit == "runes" {
		return utf8.RuneCountInString
	}
	return func(s string) int { return len(s) }
}

func (r alwaysRender) unitName() string {
	if r.Unit == "" {
		return "bytes"
	}
	return r.Unit
}

// principleIndexLine is the one-line stand-in for a principle whose body does
// not fit: its name, its description (falling back to its first heading), and
// the exact command that pulls the whole rule.
func principleIndexLine(d docindex.Doc) string {
	desc := strings.Trim(frontmatterLine(d.Body, "description"), `"'`)
	if desc == "" {
		for _, ln := range strings.Split(stripFrontmatter(d.Body), "\n") {
			if t := strings.TrimSpace(ln); strings.HasPrefix(t, "#") {
				desc = strings.TrimSpace(strings.TrimLeft(t, "#"))
				break
			}
		}
	}
	const maxDesc = 160
	if r := []rune(desc); len(r) > maxDesc {
		desc = string(r[:maxDesc-1]) + "…"
	}
	line := "- `" + d.Name + "`"
	if desc != "" {
		line += " — " + desc
	}
	return line + " — read: `satelle doc get principles " + d.Name + "`"
}

// renderAlwaysContent assembles the bounded injection body + the standing index
// instruction. The project constitution (when present) rides FIRST as order-zero
// context, then the session-resident principles. What fits in full is delivered
// in full; the rest is stepped down the condense ladder (condense.Fit owns it),
// each condensed body carrying a marker that retrieves the original. Only a set
// that cannot fit even condensed falls to index lines carrying their pull
// command, under a directive that tells the agent what was omitted for which
// harness limit and to read it before working (sty_ce1a2733). Returns the content
// and the names omitted to a pointer (nil when none; "constitution" when the
// constitution itself was indexed). The instruction is present whenever it fits,
// so the pull-on-reference discipline is taught from day one.
func renderAlwaysContent(constitution string, docs []docindex.Doc, r alwaysRender) (string, []string) {
	var items []condense.Item
	consPath := r.ConstitutionPath
	if consPath == "" {
		consPath = ".satelle/constitution.md"
	}
	if constitution != "" {
		// What a marker retrieves is the authored file, frontmatter and all.
		raw, _ := os.ReadFile(r.ConstitutionPath)
		items = append(items, condense.Item{
			Name: "constitution", Heading: "# Project constitution", Body: constitution, Original: string(raw),
			Pointer: "- `constitution` — the project constitution — read: the file `" + consPath + "`",
		})
	}
	for _, d := range docs {
		body := strings.TrimSpace(stripFrontmatter(d.Body))
		if body == "" {
			continue
		}
		items = append(items, condense.Item{
			Name: d.Name, Group: "# Always-resident principles (satelle)", Heading: "### " + d.Name,
			Body: body, Original: d.Body, Pointer: principleIndexLine(d),
		})
	}
	rules := r.Rules
	if rules.Unset() {
		rules, _ = condenseRules("")
	}
	frame := condense.Frame{
		PointerHeader: "OMITTED FOR THE " + strings.ToUpper(r.Harness) + " CONTEXT LIMIT (" + strconv.Itoa(r.Budget) +
			" " + r.unitName() + "): the following are not inlined — read each now with the command shown, before doing any work.",
		Trailer: alwaysIndexInstruction,
	}
	out, rungs, err := condense.Fit(items, frame, max(r.Budget, 1), r.measure(), rules, r.Offload)
	if err != nil {
		return "", nil
	}
	var omitted []string
	for _, rg := range rungs {
		if rg.Rung == condense.RungPointer {
			omitted = append(omitted, rg.Name)
		}
	}
	return out, omitted
}

// condenseRules resolves the condense rules for a repo: the embedded defaults
// with <dataDir>/config/condense.toml laid over them. A repo override that does
// not parse yields the embedded rules and the error, so a typo costs the
// override and never the context.
func condenseRules(dataDir string) (condense.Rules, error) {
	rules, err := condense.ParseRules(config.EmbeddedCondense(), config.CondenseOverride(dataDir))
	if err == nil {
		return rules, nil
	}
	embedded, eerr := condense.ParseRules(config.EmbeddedCondense())
	if eerr != nil {
		return condense.Rules{}, eerr
	}
	return embedded, err
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

// emitAdditionalContext writes the hook JSON that adds advisory context. event is
// the hook event name; context is the body the model reads (a system reminder for
// SessionStart; beside the tool result for PreToolUse). permissionDecision is
// optional — "" omits it (SessionStart, which makes no permission decision); a
// PreToolUse allow-with-nudge sets "allow" so the edit proceeds while the
// additionalContext advisory rides alongside (the only model-visible channel on an
// ALLOWED edit — bare stderr is transcript-only on exit 0). One emitter for both
// callers (sty_f5f351d1).
//
// UserPromptSubmit shape (sty_e16a2cd7 AC7): the same Claude-shaped
// hookSpecificOutput/additionalContext envelope is used for both Claude and Grok
// harnesses. Grok documents additionalContext as a working channel (Stop non-error
// feedback; Claude-compat vocabulary) and does not define a distinct
// UserPromptSubmit output envelope. PreToolUse deny still splits harnesses because
// Claude rejects top-level decision/reason — a real schema conflict that context
// injection does not share. Finding recorded on sty_e16a2cd7 as grok-prompt-contract.
//
// PreToolUse with no permission decision is a warn (sty_a74551d6): the command
// runs and the agent is told why it was flagged. Whether a harness carries that
// to the model is a per-harness fact (agentcli HarnessFacts.PreToolUseWarn), so
// harness picks the encoding and a harness that cannot deliver one gets nothing
// on stdout. Every other event keeps the one Claude-shaped envelope.
func emitAdditionalContext(out io.Writer, harness, event, permissionDecision, context string) error {
	if event == "PreToolUse" && permissionDecision == "" {
		if b := agentcli.PreToolUseWarn(harness, context); b != nil {
			fmt.Fprintln(out, string(b))
		}
		return nil
	}
	fmt.Fprintln(out, string(agentcli.HookEnvelope(event, permissionDecision, context, "")))
	return nil
}

// hookPromptReminder is the CONCISE standing nudge the UserPromptSubmit hook
// re-injects every turn (the full rule is the satelle-edits-require-a-story
// principle injected at SessionStart). It keeps the engaged-story discipline in
// front of the agent between session starts — a single PreToolUse gate is not the
// only line of defence (sty_949e8739).
const hookPromptReminder = "satelle: edits require an ENGAGED story. Before any Edit/Write/create/delete, engage a story in a performing state — `satelle story create …` then `satelle story set <id> --status plan` — and drive it through its workflow. Research uses read tools (Read/grep/Glob), never Edit/Write. The edit gate enforces this; never route around it. " + gateWaitNote

// gateWaitNote is the one sentence every agent-facing surface carries about a
// gate that outlasts the call (sty_c4b92c9e): the command returns a handle, the
// verdict arrives by the harness's own hook, and the driver waits for it instead
// of asking. It rides the standing reminder and the engaged form so an agent
// reads it on every turn it could be tempted to poll on, and the help topic
// carries the same two phrases (gate_wait_text_test.go asserts both).
const gateWaitNote = "A slow gate returns a handle and its verdict is delivered into this session as a notification — never poll, sleep-loop, or re-run a backgrounded command."

// gateNotWiredWarning is the LOUD banner the UserPromptSubmit self-check prepends
// when it can confidently see the PreToolUse edit gate is NOT wired into the
// repo's committed hook settings — the countermeasure to a silently inert gate
// (the incident this story fixes): without a wired gate, code edits are ungated
// with no signal.
const gateNotWiredWarning = "⚠️ satelle: the edit gate is NOT wired into this repo's hooks — code edits are currently UNGATED. Do NOT edit code until enforcement is live: run `satelle init` to (re)install the PreToolUse gate, then restart the session so the harness loads it."

// promptEngagedCeiling is the byte budget for the engaged replacement text.
// The engaged form replaces the static reminder, so it must never be longer
// (sty_e16a2cd7 AC4).
var promptEngagedCeiling = len(hookPromptReminder)

// formatEngagedPrompt builds the engaged UserPromptSubmit body: story id,
// status, forward target(s) + gates from the route, and the story set
// command (sty_e16a2cd7). Returns "" when there is nothing useful to say
// (no id, no advance options) so the caller keeps the reminder path.
// Never includes the static create-and-engage reminder — it REPLACES it.
func formatEngagedPrompt(info engagement) string {
	if info.ItemID == "" || len(info.Advance) == 0 {
		return ""
	}
	state := info.State
	if state == "" {
		state = "?"
	}

	// Build advance clauses and gate lists; degrade under the ceiling.
	type targetView struct {
		to    string
		gates []string
	}
	views := make([]targetView, 0, len(info.Advance))
	for _, a := range info.Advance {
		views = append(views, targetView{to: a.To, gates: append([]string(nil), a.Gates...)})
	}

	build := func(capGates int, withGates bool) string {
		var advParts []string
		var gateParts []string
		for _, v := range views {
			advParts = append(advParts, fmt.Sprintf("`satelle story set %s --status %s`", info.ItemID, v.to))
			if !withGates || len(v.gates) == 0 {
				continue
			}
			g := v.gates
			suffix := ""
			if capGates > 0 && len(g) > capGates {
				suffix = fmt.Sprintf("+%d", len(g)-capGates)
				g = g[:capGates]
			}
			gateParts = append(gateParts, strings.Join(g, ", ")+suffix)
		}
		msg := fmt.Sprintf("satelle: %s ENGAGED (%s) — advance: %s",
			info.ItemID, state, strings.Join(advParts, " | "))
		if withGates && len(gateParts) > 0 {
			msg += "; gates: " + strings.Join(gateParts, " | ")
		}
		return msg + ". " + gateWaitNote
	}

	// Degradation ladder: full → cap gates at 3 → drop gates → give up.
	for _, try := range []struct {
		capGates  int
		withGates bool
	}{
		{0, true},  // full (0 = no cap)
		{3, true},  // cap gates
		{0, false}, // drop gates
	} {
		out := build(try.capGates, try.withGates)
		if len(out) <= promptEngagedCeiling {
			return out
		}
	}
	return ""
}

// runHookPrompt is the UserPromptSubmit handler: it re-injects the concise
// edits-require-a-story reminder and, when a gate-liveness self-check confidently
// finds no wired edit gate, prepends the LOUD not-wired warning. With a story
// engaged from this tree that has forward route advances, the reminder is
// REPLACED by the engaged form (id, status, next gate, story set)
// (sty_e16a2cd7). Fails open — a resolve/read failure injects only the reminder.
func runHookPrompt(out io.Writer) error {
	return runHookPromptWith(out, true)
}

// runHookPromptWith is runHookPrompt that can leave a finished gate unclaimed.
// A harness whose resume wake owns delivery (resumeWakeFor) discards this hook's
// additionalContext, so claiming a verdict here would lose it: it stays for the
// resume (sty_eac9b28d).
func runHookPromptWith(out io.Writer, gatesInContext bool) error {
	body := hookPromptReminder
	// Engagement: fail-open (a resolve error leaves the static reminder)
	// (sty_e16a2cd7 AC6).
	if info, engaged, err := currentEngagement(); err == nil && engaged {
		if eng := formatEngagedPrompt(info); eng != "" {
			body = eng // REPLACE the create-and-engage reminder
		} else {
			// Engaged but no forward advance (e.g. a baseline workflow whose only
			// next is terminal): keep the reminder plus the engagement line so the
			// turn still names the story (architecture note on sty_e16a2cd7 plan).
			body = appendEngagementToPrompt(body, info)
		}
	}
	if root, ok := repoRootForHook(); ok {
		if wired, checked := gateWiredInSettings(root); checked && !wired {
			// Warning is orthogonal to the reminder/engaged body and does not
			// count against promptEngagedCeiling.
			body = gateNotWiredWarning + "\n\n" + body
		}
	}
	// A gate that finished between turns (sty_c4b92c9e): put its verdict in front
	// of the model with this prompt. No waiting — the Stop hook owns the wait.
	if gatesInContext {
		if verdicts := gateDeliveryFor(0); verdicts != "" {
			body += "\n\n" + verdicts
		}
	}
	return emitAdditionalContext(out, "", "UserPromptSubmit", "", body)
}

// runHookStopcheck is the Stop handler: a post-hoc detector for the exact
// incident the PreToolUse gate prevents. It blocks finishing when the tree has
// uncommitted non-exempt in-repo changes while NO story is engaged from this
// tree — so an ungated edit cannot be silently finished even if the PreToolUse
// hook never fired. Honours stop_hook_active (never re-blocks its own block) and
// fails open (git absent, clean tree, only exempt changes, or a story engaged
// from this tree → allow). A story engaged from this tree owns its dirty files
// whichever session engaged it, so a sibling session is never blocked with a
// demand it cannot satisfy (sty_211d8419, sty_9c782f0e).
func runHookStopcheck(raw []byte, out io.Writer) error {
	stopEmitHarness = stopHarness(raw)
	// Scoped to this answer: a later emitter in the same process (in-process
	// tests run stopchecks for several harnesses) must not inherit it.
	defer func() { stopEmitHarness = "" }()
	// A cursor stop carries the turn's token usage and nothing else keeps it, so it
	// is recorded before any early return below — a turn that ends on a delivered
	// verdict is still a turn the session paid for. Fail open.
	if stopEmitHarness == agentcli.HarnessCursor && !isDispatchedProcess() {
		_ = agentcli.RecordCursorStop(bindSessionID(raw), raw)
	}
	// A gate the session handed off (sty_c4b92c9e) is what it is waiting on: wait
	// for it here and answer with its verdict, which the harness feeds back as the
	// session's next input — the wake that costs the driver no call to ask. A gate
	// still running at the end of the wait is answered too, with a still-running
	// note, so the session is never released to idle while its gate goes on and
	// nothing would wake it. This runs before the anti-loop guard, which protects
	// only stopcheck's own block: a verdict is consumed once, so it cannot loop,
	// and a still-running note ends with the run (finished, died, or — where the
	// platform cannot verify liveness — noted once).
	//
	// A dispatched process never takes this wait: it inherits the driver's
	// SATELLE_SESSION, so the handle it would wait on is the very gate that is
	// waiting for it to exit — a cycle only idle_timeout ends. The driving
	// session, which is not dispatched, still waits. For the same reason the
	// ungated-edit check below (stopcheckEdits) never judges a dispatched process.
	//
	// A harness that caps the continuations of a turn (resumeWakeFor) is woken by
	// resuming its session instead of by a note it would count: see resumeWake.
	//
	// A harness whose stop cannot hold a session (facts.SettleNotifyOnly) is the
	// exception: a block there is a user message that starts a turn of its own, so
	// a still-running note would re-prompt on every settle with nothing to cap it.
	// Its hook waits once (settleGateDelivery), blocks only with a verdict, and
	// answers a gate still going with an allow that names it as pending, which the
	// harness adapter asks about again.
	//
	// A harness whose event counts its own continuations (agentcli.StopWithinCap)
	// takes none of this once the turn has spent them: it would not act on what is
	// said, so nothing is claimed and the verdict stays for the next turn.
	var wake *resumeWake
	defer func() { wake.settle() }()
	var settle *agentcli.StopAllow
	if !isDispatchedProcess() && agentcli.StopWithinCap(stopHarness(raw), raw) {
		facts := agentcli.FactsFor(stopHarness(raw))
		if !facts.SettleNotifyOnly {
			if wake = resumeWakeFor(raw); wake != nil {
				if text := wake.stop(raw); text != "" {
					return wake.block(out, text)
				}
				if wake.spent() {
					return nil // the turn ends on its own; nothing more can be said
				}
			} else if text := stopGateDeliveryFor(stopGateWait()); text != "" {
				return emitStopBlock(out, text)
			}
		} else {
			d, limited := settleGateDelivery(stopGateWait(), hookNoWakeFlag, facts.NoWakeLimitation)
			if d.text != "" {
				return emitStopBlock(out, d.text)
			}
			settle = &agentcli.StopAllow{SystemMessage: d.note, Pending: d.pending, Limited: limited}
			for _, id := range limited {
				settle.SystemMessage = joinNonEmpty("\n", settle.SystemMessage, fmt.Sprintf("satelle: gate %s undelivered — %s", id, facts.NoWakeLimitation))
			}
		}
	}
	block, err := stopcheckEdits(raw)
	if err != nil {
		return err
	}
	if block != "" {
		return wake.block(out, block) // counted where the harness caps continuations
	}
	if settle != nil && settle.SystemMessage != "" {
		return emitStopAllow(out, *settle)
	}
	return nil
}

// stopHarness is the harness the Stop event came from: the hook's own --harness
// token, else a neutral sniff of the event envelope.
func stopHarness(raw []byte) string {
	if hookHarnessFlag != "" {
		return hookHarnessFlag
	}
	return harnessFromEvent(raw)
}

func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

// stopcheckEdits is the ungated-edit half of the Stop hook: the block reason when
// this tree has uncommitted non-exempt changes and no item is engaged from it;
// empty when nothing is wrong or nothing can be known. It judges only the
// driving session: a block in a process satelle dispatched (a reviewer, planner,
// coder) would run another turn whose output replaces the final output satelle
// reads as its verdict. The driving session's own Stop still flags the tree.
func stopcheckEdits(raw []byte) (block string, err error) {
	if stopHookActive(raw) {
		return "", nil // anti-loop: never re-block a stop we already blocked
	}
	if isDispatchedProcess() {
		return "", nil // not the driver: its final output is a verdict, never to be displaced
	}
	root, ok := repoRootForHook()
	if !ok {
		return "", nil // fail open — unresolvable repo blocks nothing
	}
	engaged, err := stopcheckEngaged()
	if err != nil || engaged {
		// An item is engaged from this tree (edits are legitimate) OR engagement is
		// unknowable — stopcheck is a secondary detector, so it fails OPEN rather
		// than blocking a finish on a broken deployment (the PreToolUse gate is the
		// fail-closed one).
		return "", nil
	}
	gated, derr := dirtyGatedPaths(root)
	if derr != nil || len(gated) == 0 {
		return "", nil // git absent / clean / only exempt (.satelle) changes — nothing to flag
	}
	return stopcheckReason(gated), nil
}

// repoRootForHook resolves this repo's root from the committed config, or
// (false) when it cannot be resolved — hooks that call it fail open on false.
func repoRootForHook() (string, bool) {
	_, cfgPath, err := config.Load("")
	if err != nil {
		return "", false
	}
	root := config.RepoRootFromConfigPath(cfgPath)
	if strings.TrimSpace(root) == "" {
		return "", false
	}
	return root, true
}

// gateWiredInSettings reports whether the repo's committed hook settings wire the
// PreToolUse edit gate, and whether the check could be made at all. checked=false
// means no settings file was present/readable (the caller fails OPEN — no
// warning); checked=true with wired=false means a settings file exists but no
// PreToolUse Edit-matcher hook invokes `satelle hook gate` — a confident missing
// wire the caller surfaces LOUDLY.
func gateWiredInSettings(repoRoot string) (wired bool, checked bool) {
	for _, path := range []string{
		filepath.Join(repoRoot, ".claude", "settings.json"),
		filepath.Join(repoRoot, filepath.FromSlash(grokHooksRel)),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue // absent/unreadable — skip (fail open on this candidate)
		}
		checked = true
		if settingsWiresGate(raw) {
			return true, true
		}
	}
	return false, checked
}

// settingsWiresGate reports whether a hook-settings JSON wires a PreToolUse
// Edit-matcher hook that invokes the edit gate. Recognises:
//   - legacy one-liner / inline wrapper containing "hook gate" (sty_c75c73ed)
//   - script-file form containing "pretooluse-gate-" (sty_adfb9862)
//   - parameterized form containing "satelle-hook.sh" (epic:minimal-harness-footprint)
//
// Pure over the bytes so it is unit-tested directly; a parse failure returns
// false (no confident wire).
func settingsWiresGate(raw []byte) bool {
	var s struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	for _, e := range s.Hooks.PreToolUse {
		if !strings.Contains(e.Matcher, "Edit") {
			continue
		}
		for _, h := range e.Hooks {
			if strings.Contains(h.Command, "hook gate") ||
				strings.Contains(h.Command, "pretooluse-gate-") ||
				strings.Contains(h.Command, "satelle-hook.sh") {
				return true
			}
		}
	}
	return false
}

// dirtyGatedPaths returns the repo-relative paths git reports as modified/added
// that are NOT edit-gate-exempt — the ungated-edit surface the stopcheck flags.
// Returns an error when git is unavailable or root is not a repo, so the caller
// fails open. Exempt paths (e.g. .satelle/ authored substrate) are filtered out.
func dirtyGatedPaths(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		return nil, err
	}
	var gated []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue // "XY p" is the minimum porcelain line
		}
		path := strings.TrimSpace(line[3:])
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:] // a rename reports "old -> new"; the new path is what exists
		}
		path = strings.Trim(path, `"`)
		if path == "" || exemptTarget(path) {
			continue
		}
		gated = append(gated, path)
	}
	return gated, nil
}

// stopHookActive reports whether the Stop event marks that a stop hook is already
// active — honoured so stopcheck never re-blocks a stop it already blocked
// (anti-loop). Accepts snake_case (Claude) and camelCase (Grok) shapes.
func stopHookActive(raw []byte) bool {
	var ev struct {
		Snake bool `json:"stop_hook_active"`
		Camel bool `json:"stopHookActive"`
	}
	_ = json.Unmarshal(raw, &ev)
	return ev.Snake || ev.Camel || agentcli.StopContinued(raw)
}

// stopcheckReason is the agent-facing block message naming the ungated files
// (capped so a large dirty tree does not flood the reason).
func stopcheckReason(paths []string) string {
	shown := paths
	const max = 10
	suffix := ""
	if len(shown) > max {
		suffix = fmt.Sprintf(" (+%d more)", len(shown)-max)
		shown = shown[:max]
	}
	return "satelle: STOP BLOCKED — the tree has uncommitted, non-exempt changes but NO story is engaged, so these edits were made UNGATED: " +
		strings.Join(shown, ", ") + suffix + ". This is exactly what the edit gate exists to prevent. " +
		"Engage a story now (satelle story create … then satelle story set <id> --status plan) so the change is tracked through its workflow, or revert the ungated edits."
}

// emitStopNote writes the allow-with-note JSON (one line) and returns nil. The
// payload is agentcli.StopAllow: no decision field, so the stop proceeds, and
// systemMessage surfaces the note to the operator. It is deliberately NOT
// stopBlockOut — the JSON on stdout must never read as a block on this path
// (sty_211d8419).
func emitStopNote(out io.Writer, note string) error {
	return emitStopAllow(out, agentcli.StopAllow{SystemMessage: note})
}

func emitStopAllow(out io.Writer, allow agentcli.StopAllow) error {
	// A harness whose only Stop channel re-prompts takes no note (nil).
	if b := agentcli.StopAllowOutput(stopEmitHarness, allow); b != nil {
		fmt.Fprintln(out, string(b))
	}
	return nil
}

// stopBlockOut is the Stop-hook block payload (AC6 / sty_5e4bc568 audit): Claude
// Code's Stop control channel IS top-level {"decision":"block","reason":…} —
// distinct from PreToolUse's hookSpecificOutput shape. The same top-level shape
// best-effort covers Grok.
type stopBlockOut struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// emitStopBlock writes the Stop block JSON (one line) and returns nil — the Stop
// hook blocks via the stdout decision, not an exit code (its wiring carries no
// '|| exit 2'). Every block path goes through here so none can emit the wrong
// decision value.
func emitStopBlock(out io.Writer, reason string) error {
	fmt.Fprintln(out, string(agentcli.StopOutput(stopEmitHarness, true, reason)))
	return nil
}

// stopEmitHarness is the harness the Stop answer in flight is encoded for. It is
// set once per stopcheck process (runHookStopcheck), because the block and note
// emitters sit several calls below the payload that names the harness; the
// encodings themselves live in agentcli.StopOutput.
var stopEmitHarness string
