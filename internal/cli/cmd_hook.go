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
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sessionTag is the sole system-residency marker (taxonomy: system|ondemand).
// A principle carrying it is system-resident — injected every SessionStart.
// A principle WITHOUT it is ondemand — resolvable substrate pulled only when a
// skill or workflow references it, never auto-injected. No other frontmatter
// field (including legacy scope:) participates in injection.
const sessionTag = "principles:session"

// alwaysContextCeiling is THE SessionStart budget (bytes) for constitution +
// system-resident principle bodies + the on-demand pointer. There is no second
// budget — this constant IS the ceiling (epic:substrate-convergence order:4 /
// sty_cd5e341c). Keep the resident set to the operating triad (agent-goals,
// edits-require-a-story, recognise-blockage) plus the order-zero constitution.
// Overflow truncates with a stderr note; the hook still fails open.
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
		Long: `The handlers a harness calls on satelle's behalf: session context injection, the
edit and commit gates, the prompt reminder.

You do not run these by hand — the harness does, on its own events, and they read
their payload from stdin. Read them to understand what satelle enforces in a
session; install or remove the wiring with satelle agents.`,
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
search_replace|write. It returns a deny unless a story is ENGAGED — in one of the active workflow's
non-terminal engaging states (e.g. plan, in_progress, integration, release) —
so the agent works under a tracked story. On deny it emits a single harness-
correct JSON shape on stdout (detected from the event envelope: tool_input =
Claude, toolInput = Grok) so the harness surfaces the reason to the agent
(Claude: hookSpecificOutput.permissionDecision=deny + permissionDecisionReason;
Grok: decision=deny + reason), not a bare "hook denied (exit 2)". Emitting both
shapes in one blob fails Claude's PreToolUse schema and silently unblocks the
tool (sty_5e4bc568). The "engaged" policy is authored substrate — it reads the
route's start/terminal markers (Mdiamond=start, Msquare=terminal) rather than
hardcoding state names, so configuration drives the decision (sty_f3d5d4b8,
sty_e4902c51).

The installed satelle-hook.sh wrapper normalises that direct command result to
structured deny JSON plus handler exit 0. Claude and Codex process structured
stdout only on the successful-handler path; their exit-2 path instead requires
a non-empty reason on stderr. Never combine JSON-only stdout, exit 2, and empty
stderr. Grok receives its top-level decision/reason envelope through the same
exit-0 structured path.

The edit target is resolved to an ABSOLUTE path against the repo root before any
containment test (sty_8c3d345c). Harnesses differ: Claude Code sends an absolute
file_path, Grok sends a repo-relative one — resolving up front means a relative
target is never nested under a narrower tested root (e.g. the data dir) and
wrongly classed as inside it, which previously let Grok edits bypass the gate.

Edits that land in ANOTHER git working tree (root differs from the session
anchor) are REFUSED (sty_a8454d10; supersedes the sty_3026d890 stance that
closed /tmp and every non-repo path). A session in satelle-server must not
rewrite files under a sibling repo such as ../satelle — open a session in THAT
repo. Temp dirs, scratchpads, and non-repo paths are outside the fence's
concern and are allowed.

Exemption is CONFIGURATION, not code (the constitution: configuration over
code). An edit is exempt from the engaged-story check ONLY when its target falls
under a [gate] edit_exempt_paths prefix (repo-root-relative or absolute). The
binary does NOT special-case the data dir or any managed path: 'satelle init'
SEEDS .satelle/ and .gitignore into edit_exempt_paths so authored substrate
(workflows, skills, principles, documents, tasks, config) and satelle-managed
.gitignore output stay editable without a release OOTB — but the operator sees
and owns that list and may add a harness authoring dir like .claude/ or remove
either default. With an empty edit_exempt_paths, even a .satelle/ edit needs an
engaged story: config decides, never a Go rule. Generated views under the data
dir stay protected by their 0o444 file mode regardless.

Fails closed: a store open error, listing error, unresolvable workflow, or
workflow body that declares no route blocks the edit with a clear error message rather than
silently allowing it on a broken deployment (sty_f3d5d4b8).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			// currentSeatTouch is resolved once so deny reasons can name a non-live
			// holder without a second store open (sty_1738f973 AC6). A live
			// owner-held seat is also heartbeated (sty_3bb1d8be).
			info, engaged, engErr := currentSeatTouch()
			p := filePathFromEvent(raw)
			command := bashCommandFromEvent(raw)
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
				// Exemption is CONFIGURATION, not code (the constitution:
				// configuration over code). Only a path under a [gate]
				// edit_exempt_paths prefix is exempt from the engaged-story gate.
				// The data dir / managed paths are NOT special-cased in the binary —
				// `satelle init` seeds .satelle/ and .gitignore into
				// edit_exempt_paths so authored substrate and satelle-managed
				// .gitignore output stay editable without a release OOTB, but the
				// operator owns that list (sty_8c3d345c / sty_f115e6bf). With an
				// empty list, even a .satelle/ edit needs an engaged story: config
				// decides, never a Go rule.
				if exemptTarget(p) {
					return nil
				}
			} else if command != "" {
				// Codex routes Bash/shell through this hook. Preserve read-only
				// preflight: only shell commands with an in-tree mutation target
				// need workflow edit permission. Foreign-tree containment stays
				// active here as well as commitgate.
				if !allowOutsideTreeEdits() {
					_, foreign := bashMutationTargets(command, sessionAnchor())
					if path, foreignRoot, ok := foreignTreeTarget(sessionAnchor(), foreign); ok {
						return denyPreToolUse(cmd, raw, outsideAnchorBashReason(path, foreignRoot))
					}
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
			if editPermitted(info, currentDispatchMarker()) {
				return nil
			}
			return denyPreToolUse(cmd, raw, editPermissionDenyReason(info, time.Now().UTC()))
		},
	}

	commitgate := &cobra.Command{
		Use:   "commitgate",
		Short: "PreToolUse Bash gate — foreign-tree containment + engaged-story commit/push",
		Long: `commitgate is the PreToolUse handler for Bash. It first applies best-effort
foreign-tree containment (sty_a8454d10 / sty_aadd4d6c): a command whose mutation
target resolves inside a git working tree whose root differs from the session
anchor is denied unless [gate] allow_outside_tree_edits is true. Temp/scratchpad/
non-repo paths are not fenced (the sty_3026d890 stance that closed /tmp is
superseded). Containment is a reminder and boundary, not a sandbox. Then, for
git commit/push only, it exits non-zero unless a story is engaged. On deny it
emits the same harness-specific PreToolUse deny shape and uses the same installed
wrapper contract as gate: structured deny JSON plus handler exit 0; an exit-2
fallback would require a non-empty stderr reason (sty_5e4bc568, sty_56cda59c).
Fails closed on store/listing/workflow-resolution errors (sty_f3d5d4b8).

Optional step policy (sty_c21490cc): when [gate.command_allow] is authored in
satelle.toml (e.g. push = ["release"]), an engaged story must also be at one of
the listed statuses for that git subcommand. Absent/empty command_allow leaves
behaviour exactly as above — opt-in, not a satelle default.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			command := bashCommandFromEvent(raw)
			// Containment BEFORE the engaged-story branch: a foreign-tree mutation
			// is wrong even with a story engaged (sty_aadd4d6c / sty_a8454d10).
			if !allowOutsideTreeEdits() {
				cands := mutationTargets(command, sessionAnchor())
				if path, foreignRoot, ok := foreignTreeTarget(sessionAnchor(), cands); ok {
					return denyPreToolUse(cmd, raw, outsideAnchorBashReason(path, foreignRoot))
				}
			}
			var info seatInfo
			var engaged bool
			var seatResolved bool
			if bashMutatesTree(command, sessionAnchor()) {
				var err error
				info, engaged, err = currentSeatTouch()
				seatResolved = true
				if err != nil {
					return denyPreToolUse(cmd, raw, "satelle: "+err.Error())
				}
				if !editPermitted(info, currentDispatchMarker()) {
					return denyPreToolUse(cmd, raw, editPermissionDenyReason(info, time.Now().UTC()))
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
			if !needsEngage && !commandAllowRestricts(subs) {
				return nil // not a gated command — allow
			}
			// Heartbeat only for gated commands (after the early return above) —
			// activity on commit/push keeps the seat alive (sty_3bb1d8be).
			if !seatResolved {
				var err error
				info, engaged, err = currentSeatTouch()
				if err != nil {
					return denyPreToolUse(cmd, raw, "satelle: "+err.Error())
				}
			}
			if needsEngage && !engaged {
				// Deny only — never allow a fused engage+commit form. PreToolUse cannot
				// know the engage line would succeed; pick the message that teaches the
				// recovery path (sty_577d292f). Name a non-live seat when present so
				// the agent can inspect/release without digging (sty_1738f973 AC6).
				return denyPreToolUse(cmd, raw, commitDenyReason(command)+seatSuffix(info, time.Now().UTC()))
			}
			// Step-scoped policy (opt-in): engaged story must be at an allowed status.
			if engaged {
				st := info.StoryStatus
				if st == "" {
					st = info.State
				}
				if reason, deny := commandAllowDeny(subs, st); deny {
					return denyPreToolUse(cmd, raw, reason+seatSuffix(info, time.Now().UTC()))
				}
			}
			return nil
		},
	}

	prompt := &cobra.Command{
		Use:   "prompt",
		Short: "UserPromptSubmit reinforcement — re-inject the edits-require-a-story rule + a gate-liveness self-check",
		Long: `prompt is the UserPromptSubmit handler. With no live engagement seat it
re-injects a CONCISE reminder that tree edits require an engaged story (the full
principle rides at SessionStart). With a LIVE seat that has a forward route
advance, that reminder is REPLACED by the engaged form — story id, status, next
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
			_, _ = io.ReadAll(cmd.InOrStdin())
			return runHookPrompt(cmd.OutOrStdout())
		},
	}
	stopcheck := &cobra.Command{
		Use:   "stopcheck",
		Short: "Stop post-hoc detector — block finishing when the tree was edited ungated (no engaged story)",
		Long: `stopcheck is the Stop handler. It catches the incident the PreToolUse gate is
meant to prevent even if that gate never fired this session: on Stop, if the tree
has uncommitted, NON-EXEMPT in-repo changes while NO story is engaged, it emits a
Stop block ({"decision":"block","reason":…}) naming the ungated files so the agent
cannot silently finish an ungated edit (sty_949e8739). Stop uses top-level
decision/reason (not PreToolUse hookSpecificOutput; sty_5e4bc568 AC6). It
honours the event's stop_hook_active flag so it never re-blocks a stop it already
blocked, and fails OPEN when git is absent, the tree is clean, only exempt paths
changed, or a story is engaged.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := io.ReadAll(cmd.InOrStdin())
			return runHookStopcheck(raw, cmd.OutOrStdout())
		},
	}

	// Explicit harness for deny shape (sty_9e86f407): wrapper forwards
	// --harness claude|grok|codex; empty falls back to harnessFromEvent.
	gate.Flags().StringVar(&hookHarnessFlag, "harness", "", "claude|grok|codex — deny envelope (default: sniff event)")
	commitgate.Flags().StringVar(&hookHarnessFlag, "harness", "", "claude|grok|codex — deny envelope (default: sniff event)")
	hook.AddCommand(context, gate, commitgate, prompt, stopcheck)
	register(hook)
}

// hookHarnessFlag is set by gate/commitgate --harness (wrapper forwards the
// scaffold harness token). Empty → harnessFromEvent fallback.
var hookHarnessFlag string

// seatInfo is a single engagement-lease view for gate denials and session inject
// (sty_1738f973). Empty ItemID means no lease row was relevant.
type seatInfo struct {
	ItemID      string
	State       string // lease target / in-flight target (messaging)
	TargetState string // immutable lease target used to authenticate a dispatched performer
	StoryStatus string // committed work-item status (step policy; sty_c21490cc)
	StateAgent  string // agent allocated to lease target / State
	Owner       string
	// Worktree is the git working tree the lease was engaged from, when
	// recorded (sty_c098dc2d). It is how a session picks ITS seat out of
	// several: the default owner is pid-less local@host, so co-held sibling
	// leases are indistinguishable by owner.
	Worktree    string
	AcquiredAt  time.Time
	HeartbeatAt time.Time
	Stale       bool
	InFlight    bool
	// EditCapable is true only when the committed status is a spine performing
	// step the route allocates to agent=executor. It is intentionally narrower
	// than Engaged, which also includes isolated-agent planning states.
	EditCapable bool
	EditStates  []string
	// DispatchAgents is the route-authored performer identity by target state.
	// Lease.State intentionally remains the last committed state across a new
	// transition, so an in-flight child marker must be checked against the spec,
	// not mistaken for that prior state.
	DispatchAgents map[string][]string
	// Engaged is true when this row qualifies as live engagement (performing
	// committed status, or fresh in-flight mid-transition).
	Engaged bool
	// Advance are route-declared forward transitions from the seat's committed
	// status — terminal, park, and back-edges excluded (sty_e16a2cd7). Empty when
	// the seat is live only via InFlight, or when no forward target survives.
	Advance []wfdot.Advance
}

// storyEngaged reports whether a LIVE engagement seat is active (sty_8426b9c0 /
// sty_1738f973). A bare engagement_lease row is not enough: stale heartbeats and
// leases whose committed story is not in a performing state do NOT open the gate.
// Fails CLOSED on store open/query errors. Does NOT heartbeat (Stop is out of
// scope for sty_3bb1d8be).
func storyEngaged() (bool, error) {
	_, engaged, err := currentSeat()
	return engaged, err
}

// currentSeat resolves the engagement seat for gate/session surfaces without
// refreshing the lease heartbeat. Prefer currentSeatTouch from hook handlers
// that observe ongoing work (gate, commitgate, prompt).
func currentSeat() (info seatInfo, engaged bool, err error) {
	return resolveSeat(false)
}

// currentSeatTouch is currentSeat plus a fail-open heartbeat of a live
// owner-held seat (sty_3bb1d8be). Activity keeps the seat alive; the write
// never changes a verdict.
func currentSeatTouch() (info seatInfo, engaged bool, err error) {
	return resolveSeat(true)
}

// resolveSeat is the shared seat lookup. When touch is true and a live seat is
// found for the current owner, heartbeat_at is refreshed (fail-open).
func resolveSeat(touch bool) (info seatInfo, engaged bool, err error) {
	a, openErr := app.Open()
	if openErr != nil {
		// An ungoverned repo has no seat to determine — a session opened in an
		// ordinary non-satelle directory must go INERT, not error (sty_20a7824c).
		// This is strictly quieter than before: previously such a session paid
		// for a materialised runtime plane and then got a "fix config and retry"
		// it could not act on. Any other open failure still reports.
		if errors.Is(openErr, app.ErrNotInitialised) {
			return seatInfo{}, false, nil
		}
		return seatInfo{}, false, fmt.Errorf("cannot determine engagement (store open failed: %w) — fix config and retry", openErr)
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	wfs, werr := a.Store.DocIndex.List(ctx, "workflows")
	if werr != nil {
		return seatInfo{}, false, fmt.Errorf("cannot determine engagement (workflow list failed: %w) — fix config and retry", werr)
	}
	items, lerr := a.Store.Stories.List(ctx, workitem.ListFilter{})
	if lerr != nil {
		return seatInfo{}, false, fmt.Errorf("cannot determine engagement (story list failed: %w) — fix config and retry", lerr)
	}
	if a.Store.Leases == nil {
		// Pre-migration or incomplete bootstrap: fall back to derived status scan.
		return derivedSeat(items, wfs)
	}
	leases, qerr := a.Store.Leases.List(ctx)
	if qerr != nil {
		return seatInfo{}, false, fmt.Errorf("cannot determine engagement (lease query failed: %w) — fix config and retry", qerr)
	}
	live, other, eerr := evaluateSeat(leases, items, wfs, time.Now().UTC())
	if eerr != nil {
		return seatInfo{}, false, eerr
	}
	if len(live) > 0 {
		pick := pickSessionSeat(live)
		if touch {
			touchSeat(ctx, a.Store.Leases, pick)
		}
		return pick, true, nil
	}
	return other, false, nil
}

// pickSessionSeat chooses which live seat is THIS session's when a project
// holds several (sty_c098dc2d). The session's own working tree is the
// discriminator — owner cannot be, since the default is pid-less local@host and
// co-held sibling leases share it. Routing the heartbeat this way is what stops
// one sibling's activity from refreshing another's lease (and letting the
// actually-worked one go stale at TTL).
//
// No tree match (unanchored leases, non-git session) falls back to the first
// live seat — the previous behaviour, and still correct for a single seat.
func pickSessionSeat(live []seatInfo) seatInfo {
	if len(live) == 0 {
		return seatInfo{}
	}
	if tree := sessionWorktree(); tree != "" {
		for _, s := range live {
			if s.Worktree == tree {
				return s
			}
		}
	}
	return live[0]
}

// sessionWorktree resolves the git working tree this hook process runs in.
// Empty when git cannot answer — callers then fall back rather than guess.
var sessionWorktree = func() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	out, gerr := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if gerr != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// touchSeat refreshes heartbeat_at for a live, owner-held seat (sty_3bb1d8be).
// Activity keeps the seat alive; the write is a side effect of observation.
// Never revives a stale lease (callers pass evaluateSeat's live pick, which is
// non-stale by construction) and never touches another owner's row. Fail-open
// by construction: every error is discarded.
func touchSeat(ctx context.Context, ls *lease.Store, info seatInfo) {
	if ls == nil || info.ItemID == "" || info.Stale {
		return
	}
	owner := lease.ResolveOwner()
	if info.Owner != owner {
		return
	}
	_ = seatHeartbeat(ctx, ls, info.ItemID, owner)
}

// seatHeartbeat is the heartbeat write, injectable so the fail-open path is
// testable (same idiom as lease.PidAlive / healthzOK).
var seatHeartbeat = func(ctx context.Context, ls *lease.Store, itemID, owner string) error {
	return ls.Heartbeat(ctx, itemID, owner)
}

// evaluateSeat is the pure engagement predicate (sty_1738f973 AC2). A lease L is
// a live seat iff NOT IsStale(L) AND (committed status is a NonTerminalEngaging
// state of the item's governing workflow OR (L.InFlight AND non-stale)). The
// InFlight+fresh disjunct preserves the acquire-at-start window for a legit
// first-transition dispatch; the residual sub-TTL orphan gap is the documented
// cost (Acquire steals after HeartbeatTTL).
//
// Returns (live, other, err): live is EVERY qualifying seat (Engaged=true), in
// lease order — a project may hold more than one under a shared arbitration key
// (sty_c098dc2d), and "is work in flight?" is len(live) > 0 whether that is one
// lease or five. other is the most relevant non-qualifying lease for messaging
// (stale first, else any row), with Engaged=false. err is non-nil when a leased
// item has no resolving workflow (fail-closed, same as anyEngaged).
func evaluateSeat(leases []lease.Lease, items []workitem.Item, wfs []docindex.Doc, now time.Time) (live []seatInfo, other seatInfo, err error) {
	byID := make(map[string]workitem.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	// The front door resolves the lifecycle per item, because a DERIVED route
	// depends on the item's category and tags, not only on a workflow name — a
	// name-keyed cache would hand one story another's route (sty_9835070d).
	var otherPick seatInfo
	for _, l := range leases {
		stale := lease.IsStale(l, now)
		info := seatInfo{
			ItemID:      l.ItemID,
			State:       l.State,
			Owner:       l.Owner,
			AcquiredAt:  l.AcquiredAt,
			HeartbeatAt: l.HeartbeatAt,
			Stale:       stale,
			// EffectiveInFlight ages stuck flags; raw InFlight would wedge the
			// edit gate forever when hooks keep the heartbeat fresh (sty_bf797fa9).
			InFlight: lease.EffectiveInFlight(l, now),
		}
		info.Worktree = l.Worktree
		if stale {
			// Prefer naming a stale holder in deny/session text.
			if otherPick.ItemID == "" || (!otherPick.Stale && stale) {
				otherPick = info
			}
			continue
		}
		it, ok := byID[l.ItemID]
		if !ok {
			// Leased id not in stories/tasks list — treat as non-performing residue.
			if otherPick.ItemID == "" {
				otherPick = info
			}
			continue
		}
		// Prefer the committed item status for the performing check; fall back to
		// the lease's last-committed state when the item row is somehow missing a
		// status (should not happen for real rows).
		status := it.Status
		if status == "" {
			status = l.State
		}
		info.StoryStatus = status
		// Surface lease.State (last committed engaging target / in-flight target)
		// in the seat descriptor when more informative than committed backlog.
		if l.State != "" {
			info.State = l.State
		}
		// Also expose the committed status when it differs (e.g. lease state=plan
		// while story is still backlog mid-transition) by preferring lease state
		// for messaging, but judging on committed status.
		spec, wfName, _, serr := wfgovern.SpecFor(wfs, it)
		if serr != nil {
			return nil, seatInfo{}, fmt.Errorf("item %s: %w — cannot determine engagement", it.ID, serr)
		}
		if _, known := spec.StateAgent(status); !known {
			return nil, seatInfo{}, fmt.Errorf(
				"item %s status %q is not declared by workflow %s — cannot classify edit permission",
				it.ID, status, wfName)
		}
		info.EditCapable = spec.IsEditCapableState(status)
		info.EditStates = spec.EditCapableStates()
		info.DispatchAgents = dispatchAgents(spec)
		target := info.State
		if target == "" {
			target = status
		}
		info.TargetState = target
		var targetKnown bool
		info.StateAgent, targetKnown = spec.StateAgent(target)
		if !targetKnown {
			return nil, seatInfo{}, fmt.Errorf(
				"lease for item %s targets state %q not declared by workflow %s — cannot classify edit permission",
				it.ID, target, wfName)
		}
		engaging := map[string]bool{}
		for _, s := range spec.NonTerminalEngagingStates() {
			engaging[s] = true
		}
		// Live seat: committed status is performing, OR effective in-flight mid-transition.
		// Use info.InFlight (EffectiveInFlight) not raw l.InFlight — a dead
		// transitioning pid must not keep the seat "engaged" via residue
		// (sty_bf797fa9 AC3).
		// Effective in-flight covers the acquire-at-start window when status is
		// still the start state (backlog) — without this, mid-dispatch edits
		// would be denied.
		if engaging[status] || info.InFlight {
			info.Engaged = true
			// Prefer the committed status in the descriptor when it is performing;
			// otherwise keep lease.State (target of the in-flight transition).
			if engaging[status] {
				info.State = status
				// Advance only when committed status is genuinely performing —
				// in-flight-only seats sit at a not-yet-committed state (sty_e16a2cd7).
				info.Advance = spec.AdvanceOptions(status)
			}
			live = append(live, info)
			continue
		}
		if otherPick.ItemID == "" {
			otherPick = info
		}
	}
	return live, otherPick, nil
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

// editPermitted separates lease engagement from source-edit authorization.
// A dispatched performer is allowed only for the exact in-flight item/target
// and authored agent allocation. The driving session is allowed only in a
// committed route step allocated to the in-loop executor, never mid-transition.
func editPermitted(info seatInfo, marker dispatchMarker) bool {
	if info.ItemID == "" || info.Stale || !info.Engaged {
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
		return info.InFlight && marker.Item == info.ItemID && slices.Contains(agents, marker.Agent)
	}
	return !info.InFlight && info.EditCapable
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

// seatSuffix appends a discoverable seat descriptor to a gate deny reason when
// a non-live lease row exists (sty_1738f973 AC6). Empty when no row to name.
func seatSuffix(info seatInfo, now time.Time) string {
	if info.ItemID == "" {
		return ""
	}
	return " — " + formatSeat(info, now) + "; inspect with `satelle story seat`, release with `satelle story seat release " + info.ItemID + "`"
}

// formatSeat renders a one-line seat descriptor for deny reasons and session inject.
func formatSeat(info seatInfo, now time.Time) string {
	if info.ItemID == "" {
		return ""
	}
	state := info.State
	if state == "" {
		state = "?"
	}
	age := formatSeatAge(now.Sub(info.HeartbeatAt))
	if info.Stale {
		return fmt.Sprintf("seat held by %s (%s, stale %s)", info.ItemID, state, age)
	}
	acq := formatSeatAge(now.Sub(info.AcquiredAt))
	return fmt.Sprintf("seat held by %s (%s, owner %s, acquired %s, heartbeat %s)",
		info.ItemID, state, info.Owner, acq, age)
}

// formatSeatAge rounds a duration to a short human age for seat messaging.
func formatSeatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dd", int(d.Hours())/24)
}

// seatModeLine states the ACTIVE seat concurrency mode as mechanism: what the
// mode is, and — for a grouped seat — the two preconditions a second engagement
// must satisfy. An agent that only meets the mode in a refusal has already lost a
// turn to it. Nothing here says when a repo should choose a mode: that opinion is
// the repo's own substrate, and satelle polices no sibling contention.
func seatModeLine(mode string) string {
	if mode == config.ParallelEpic {
		return "seat mode: epic — a sibling under the SAME parent may engage alongside the holder, from a DISTINCT git working tree; a story under another parent (or none) is refused while that seat is held."
	}
	return "seat mode: none — one performing story at a time."
}

// formatSeatBlock is the SessionStart / prompt multi-line seat inject body.
func formatSeatBlock(info seatInfo, now time.Time, mode string) string {
	if info.ItemID == "" {
		return ""
	}
	return "## Engagement seat\n" + formatSeat(info, now) + "\n" + seatModeLine(mode) +
		"\nInspect: `satelle story seat` · Release: `satelle story seat release " + info.ItemID + "`"
}

// appendSeatToContext merges a seat block into SessionStart content when room
// remains under ceiling (sty_1738f973 AC6). Pure — unit-tested so removing the
// inject path fails a test rather than leaving the suite green.
func appendSeatToContext(content, seatBlock string, ceiling int) string {
	if strings.TrimSpace(seatBlock) == "" {
		return content
	}
	if content == "" {
		return seatBlock
	}
	if ceiling > 0 && len(content)+2+len(seatBlock) > ceiling {
		return content // prefer principles over a truncated seat line
	}
	return content + "\n\n" + seatBlock
}

// appendSeatToPrompt appends a one-line seat descriptor to the UserPromptSubmit
// reminder when a lease exists (sty_1738f973 AC6). Pure for unit tests.
func appendSeatToPrompt(msg string, info seatInfo, now time.Time) string {
	if info.ItemID == "" {
		return msg
	}
	return msg + "\n" + formatSeat(info, now) + " — inspect: `satelle story seat`"
}

// anyEngaged reports whether any work item sits in a non-terminal engaging state
// of the workflow that governs IT — the stamped workflow, else its category-selected
// one (wfgovern.GoverningWorkflow). A "non-terminal engaging state" is one that
// is neither start (Shape Mdiamond) nor terminal (Shape Msquare) nor cancel/exception
// (agent=reviewer with no outgoing edges) — read from the Spec's own markers,
// not hardcoded (sty_f3d5d4b8).
//
// Returns (engaged, err): err is non-nil when an item has NO resolving workflow
// or the workflow does not yield a Spec — fail-closed, not a silent allow. Pure
// core, split for testing.
func anyEngaged(items []workitem.Item, wfs []docindex.Doc) (bool, error) {
	for _, it := range items {
		spec, _, _, serr := wfgovern.SpecFor(wfs, it)
		if serr != nil {
			return false, fmt.Errorf("item %s: %w — cannot determine engagement", it.ID, serr)
		}
		engaging := map[string]bool{}
		for _, s := range spec.NonTerminalEngagingStates() {
			engaging[s] = true
		}
		if engaging[it.Status] {
			return true, nil
		}
	}
	return false, nil
}

// derivedSeat is the pre-lease-store compatibility path. It derives both the
// broad engagement predicate and the narrower edit-capable predicate from the
// governing route, so an old store never re-opens planning/reviewer states merely
// because they are non-terminal.
func derivedSeat(items []workitem.Item, wfs []docindex.Doc) (seatInfo, bool, error) {
	var other seatInfo
	for _, it := range items {
		spec, wfName, _, serr := wfgovern.SpecFor(wfs, it)
		if serr != nil {
			return seatInfo{}, false, fmt.Errorf("item %s: %w — cannot determine engagement", it.ID, serr)
		}
		agent, known := spec.StateAgent(it.Status)
		if !known {
			return seatInfo{}, false, fmt.Errorf(
				"item %s status %q is not declared by workflow %s — cannot classify edit permission",
				it.ID, it.Status, wfName)
		}
		info := seatInfo{
			ItemID: it.ID, State: it.Status, StoryStatus: it.Status,
			StateAgent: agent, EditCapable: spec.IsEditCapableState(it.Status),
			EditStates: spec.EditCapableStates(),
		}
		engaging := false
		for _, state := range spec.NonTerminalEngagingStates() {
			if state == it.Status {
				engaging = true
				break
			}
		}
		if engaging {
			info.Engaged = true
			return info, true, nil
		}
		if other.ItemID == "" {
			other = info
		}
	}
	return other, false, nil
}

// bashCommandFromEvent pulls the bash command out of a PreToolUse event.
// Accepts Claude Code's snake_case envelope (tool_input.command) AND Grok's
// camelCase envelope (toolInput.command) — both harnesses fire the same hook
// (epic:scoped-sync order:9 / sty_0d3665ee). Codex may send command as a JSON
// array of argv tokens (shell); those are joined with spaces (sty_9e86f407).
// Prefer the first non-empty value.
func bashCommandFromEvent(raw []byte) string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return ""
	}
	for _, key := range []string{"tool_input", "toolInput"} {
		rawTI, ok := top[key]
		if !ok || len(rawTI) == 0 {
			continue
		}
		var ti map[string]json.RawMessage
		if err := json.Unmarshal(rawTI, &ti); err != nil {
			continue
		}
		cmdRaw, ok := ti["command"]
		if !ok || len(cmdRaw) == 0 {
			continue
		}
		// String form (Claude / Grok).
		var s string
		if err := json.Unmarshal(cmdRaw, &s); err == nil && s != "" {
			return s
		}
		// Array form (Codex shell argv).
		var parts []string
		if err := json.Unmarshal(cmdRaw, &parts); err == nil && len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	return ""
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

// anchorFrom is the pure resolver for sessionAnchor. getenv is injected for tests.
// An env pin wins over cfgRoot because config.Load walks up from CWD and is not
// trustworthy alone once a persistent shell has cd'd.
func anchorFrom(getenv func(string) string, cfgRoot string) string {
	for _, key := range []string{"SATELLE_PROJECT_DIR", "CLAUDE_PROJECT_DIR"} {
		if p := strings.TrimSpace(getenv(key)); p != "" {
			if abs, err := filepath.Abs(p); err == nil {
				return filepath.Clean(abs)
			}
			return filepath.Clean(p)
		}
	}
	if strings.TrimSpace(cfgRoot) == "" {
		return ""
	}
	if abs, err := filepath.Abs(cfgRoot); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(cfgRoot)
}

// commandAllowRestricts reports whether any git subcommand is listed in the
// opt-in [gate.command_allow] policy (even if not commit/push).
func commandAllowRestricts(subs []string) bool {
	return commandAllowRestrictsWith(loadCommandAllow(), subs)
}

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

// commandAllowDeny returns a deny reason when a restricted subcommand is not
// permitted at the engaged story's current status. deny=false when allowed or
// unconfigured.
func commandAllowDeny(subs []string, storyStatus string) (reason string, deny bool) {
	return commandAllowDenyWith(loadCommandAllow(), subs, storyStatus)
}

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
	cfg, _, err := config.Load("")
	if err != nil {
		return nil
	}
	if len(cfg.Gate.CommandAllow) == 0 {
		return nil
	}
	// Normalize keys to lowercase for lookup.
	out := make(map[string][]string, len(cfg.Gate.CommandAllow))
	for k, v := range cfg.Gate.CommandAllow {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

// allowOutsideTreeEdits reports whether [gate] allow_outside_tree_edits is true.
// Default false = deny mutations in another repo's working tree (sty_a8454d10).
// Non-repo paths are never fenced. On config load failure, returns false
// (containment stays on).
func allowOutsideTreeEdits() bool {
	cfg, _, err := config.Load("")
	if err != nil {
		return false
	}
	return cfg.Gate.AllowOutsideTreeEdits
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

// droppedSeatEditReason is the deny when a story is still in a performing
// status but its engagement lease is missing (sty_4f74d01f). Distinct from
// noEngagedStoryEditReason so agents re-acquire instead of creating a new story.
func droppedSeatEditReason(id, status string) string {
	return fmt.Sprintf(
		"satelle: story %s is performing (status %q) but its engagement seat was dropped — re-acquire with `satelle story set %s --status %s` (same status is intentional; it grants a seat without inventing a new step), then retry the edit. Inspect with `satelle story seat`.",
		id, status, id, status)
}

// editGateDenyReason picks the agent-facing text when no live seat is engaged
// (sty_4f74d01f): prefer naming a performing story with a dropped seat over the
// generic "open a story" message.
func editGateDenyReason(info seatInfo, now time.Time) string {
	if drop := firstDroppedPerformingSeat(); drop.ItemID != "" {
		return droppedSeatEditReason(drop.ItemID, drop.StoryStatus)
	}
	return noEngagedStoryEditReason + seatSuffix(info, now)
}

const readOnlyPreflightReason = "Read-only preflight remains available: use Read/read_file/Grep/Glob or non-mutating shell commands, and record approved context with `satelle story attach` or `satelle story log`; advance through the workflow gate before editing."

func editPermissionDenyReason(info seatInfo, now time.Time) string {
	if info.ItemID == "" || !info.Engaged || info.Stale {
		return editGateDenyReason(info, now) + " " + readOnlyPreflightReason
	}
	if info.InFlight {
		target := info.State
		if target == "" {
			target = "the next state"
		}
		return fmt.Sprintf(
			"satelle: story %s has a transition to %q IN FLIGHT (planner/reviewer or dispatched step running); the driving session cannot edit until the transition commits. %s",
			info.ItemID, target, readOnlyPreflightReason)
	}
	agent := info.StateAgent
	if agent == "" {
		agent = "no in-loop executor"
	}
	states := strings.Join(info.EditStates, ", ")
	if states == "" {
		states = "(none declared)"
	}
	return fmt.Sprintf(
		"satelle: story %s is at %q, which its workflow allocates to %q; source edits are permitted only in route steps allocated to agent=executor (%s). Do not work ahead. %s",
		info.ItemID, info.StoryStatus, agent, states, readOnlyPreflightReason)
}

// firstDroppedPerformingSeat finds a story/task whose committed status is
// performing but that has no live (non-stale) engagement lease (sty_4f74d01f).
func firstDroppedPerformingSeat() seatInfo {
	a, err := app.Open()
	if err != nil {
		return seatInfo{}
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	wfs, err := a.Store.DocIndex.List(ctx, "workflows")
	if err != nil {
		return seatInfo{}
	}
	items, err := a.Store.Stories.List(ctx, workitem.ListFilter{})
	if err != nil {
		return seatInfo{}
	}
	leased := map[string]bool{}
	if a.Store.Leases != nil {
		leases, lerr := a.Store.Leases.List(ctx)
		if lerr == nil {
			now := time.Now().UTC()
			for _, l := range leases {
				if !lease.IsStale(l, now) {
					leased[l.ItemID] = true
				}
			}
		}
	}
	for _, it := range items {
		if leased[it.ID] {
			continue
		}
		spec, _, _, serr := wfgovern.SpecFor(wfs, it)
		if serr != nil {
			continue
		}
		engaging := false
		for _, s := range spec.NonTerminalEngagingStates() {
			if s == it.Status {
				engaging = true
				break
			}
		}
		if engaging {
			return seatInfo{ItemID: it.ID, StoryStatus: it.Status, State: it.Status}
		}
	}
	return seatInfo{}
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
	h := hookHarnessFlag
	if h == "" {
		h = harnessFromEvent(raw)
	}
	_ = emitPreToolUseDeny(cmd.OutOrStdout(), h, reason)
	return fmt.Errorf("%s", reason)
}

// harnessFromEvent returns "grok" or "claude" from a PreToolUse event envelope
// (sty_5e4bc568). Claude Code uses snake_case tool_input; Grok Build uses
// camelCase toolInput. Presence is tested with json.RawMessage so a Bash
// commitgate event (tool_input.command only) classifies correctly.
//
// "grok" only when toolInput is present AND tool_input is absent. Default is
// "claude" (the strict validator): mis-detecting Grok as Claude is safe (Grok
// is lenient and exit 2 still blocks); mis-detecting Claude as Grok reintroduces
// the inert-gate bug. Bias to the strict shape on ambiguity/empty input.
func harnessFromEvent(raw []byte) string {
	var top struct {
		ToolInputSnake json.RawMessage `json:"tool_input"`
		ToolInputCamel json.RawMessage `json:"toolInput"`
	}
	_ = json.Unmarshal(raw, &top)
	if len(top.ToolInputCamel) > 0 && len(top.ToolInputSnake) == 0 {
		return "grok"
	}
	return "claude"
}

// claudePreToolUseDenyOut is Claude Code's PreToolUse deny shape (sty_5e4bc568).
// Claude's schema rejects top-level decision/reason; only hookSpecificOutput is
// valid. permissionDecisionReason is the model-visible deny channel.
type claudePreToolUseDenyOut struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// grokPreToolUseDenyOut is Grok Build's PreToolUse deny shape: top-level
// decision + reason (sty_e4902c51 / sty_5e4bc568).
type grokPreToolUseDenyOut struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// emitPreToolUseDeny writes one harness-correct deny JSON line to out.
// harness is "grok" → top-level decision/reason; "claude"/"codex"/other → Claude
// envelope (Codex rejects empty reasons and does not accept Grok's top-level
// shape for PreToolUse deny — sty_9e86f407).
func emitPreToolUseDeny(out io.Writer, harness, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "satelle: denied (no reason supplied)"
	}
	var b []byte
	var err error
	if harness == "grok" {
		b, err = json.Marshal(grokPreToolUseDenyOut{Decision: "deny", Reason: reason})
	} else {
		// Claude + Codex: hookSpecificOutput with deny + non-empty reason.
		var doc claudePreToolUseDenyOut
		doc.HookSpecificOutput.HookEventName = "PreToolUse"
		doc.HookSpecificOutput.PermissionDecision = "deny"
		doc.HookSpecificOutput.PermissionDecisionReason = reason
		b, err = json.Marshal(doc)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(b))
	return nil
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
// gate: it resolves under a configured [gate] edit_exempt_paths prefix. Exemption
// is CONFIGURATION, not code (the constitution: configuration over code) — the
// binary no longer special-cases the data dir or managed paths; `satelle init`
// seeds .satelle/ and .gitignore into edit_exempt_paths so authored substrate
// and satelle-managed .gitignore output stay editable OOTB, but the operator
// owns that list (sty_8c3d345c / sty_f115e6bf). The target is resolved to
// absolute against the repo root FIRST, so a repo-relative path (as Grok sends)
// is classified correctly. Returns false if the config/root cannot be resolved,
// so the gate stays conservative (still applies) on any resolution failure.
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
// When any engagement lease exists, appends a seat block so the agent sees the
// holder without digging (sty_1738f973 AC6) — also fail-open on lease read errors.
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
	// Web availability (sty_fb5e6d96): ONE line naming the URL and whether
	// anything answers on it, ahead of the constitution so a new user sees where
	// the server is without asking. Fail-open: probeWebAvailability never errors
	// and renders "unknown" rather than a fabricated "live". This reaches Claude,
	// Grok and Codex through the SessionStart wiring all three already carry.
	content = prependAvailabilityLine(content, probeWebAvailability().hookLine())
	// Scaffold drift (sty_ac25b787): fail-open warning — never blocks SessionStart.
	// Names `satelle init` as the heal. DetectScaffoldDrift is pure comparison.
	if warn := formatScaffoldDriftWarning(DetectScaffoldDrift(a.RepoRoot)); warn != "" {
		if content == "" {
			content = warn
		} else {
			content = warn + "\n\n" + content
		}
	}
	// Seat inject: prefer a live seat; else name any non-live residue so the agent
	// can release a stuck holder. Fail open — a seat-read error injects nothing.
	content = appendSeatToContext(content, sessionSeatBlock(a), alwaysContextCeiling)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return emitAdditionalContext(out, "SessionStart", "", content)
}

// prependAvailabilityLine puts the one-line web-availability statement at the
// head of the SessionStart body (sty_fb5e6d96). Exactly one line of content is
// added — no heading, no table — so the always-resident cost stays a line.
// Pure, for unit tests.
func prependAvailabilityLine(content, line string) string {
	if strings.TrimSpace(line) == "" {
		return content
	}
	if strings.TrimSpace(content) == "" {
		return line
	}
	return line + "\n\n" + content
}

// sessionSeatBlock returns the SessionStart seat inject for a, or "" when no
// lease exists / store is incomplete. Fail-open helper for runHookContext.
func sessionSeatBlock(a *app.App) string {
	if a == nil || a.Store == nil || a.Store.Leases == nil {
		return ""
	}
	ctx := context.Background()
	mode := a.Config.ResolveEngagementParallel()
	leases, err := a.Store.Leases.List(ctx)
	if err != nil || len(leases) == 0 {
		// No seat to describe. Under the default mode that is the whole story and
		// the inject stays silent; under a NON-default mode the agent still has to
		// know the rule it will be engaging under, and a refusal is too late to
		// learn it.
		if mode != config.ParallelNone {
			return "## Engagement seat\nseat free.\n" + seatModeLine(mode)
		}
		return ""
	}
	wfs, _ := a.Store.DocIndex.List(ctx, "workflows")
	items, _ := a.Store.Stories.List(ctx, workitem.ListFilter{})
	now := time.Now().UTC()
	live, other, _ := evaluateSeat(leases, items, wfs, now)
	// Several live seats: lead with this session's own (by working tree) but
	// render them ALL — an operator joining a project where work is in flight
	// under a shared key must see every holder, not just one.
	if len(live) > 0 {
		return renderSeatBlocks(live, now, mode)
	}
	info := other
	// No evaluateSeat pick (e.g. empty items+wfs): still name the first raw row.
	if info.ItemID == "" {
		l := leases[0]
		info = seatInfo{
			ItemID: l.ItemID, State: l.State, Owner: l.Owner, Worktree: l.Worktree,
			AcquiredAt: l.AcquiredAt, HeartbeatAt: l.HeartbeatAt,
			Stale: lease.IsStale(l, now), InFlight: lease.EffectiveInFlight(l, now),
		}
	}
	return formatSeatBlock(info, now, mode)
}

// renderSeatBlocks formats every live seat for the SessionStart inject, leading
// with this session's own (sty_c098dc2d). A project may hold more than one, and
// an operator joining that work must see all of it — the old leases[0] pick
// would have shown one and hidden the rest.
func renderSeatBlocks(live []seatInfo, now time.Time, mode string) string {
	if len(live) == 0 {
		return ""
	}
	mine := pickSessionSeat(live)
	blocks := []string{formatSeatBlock(mine, now, mode)}
	for _, s := range live {
		if s.ItemID != mine.ItemID {
			blocks = append(blocks, formatSeatBlock(s, now, mode))
		}
	}
	return strings.Join(blocks, "\n\n")
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
//
// UserPromptSubmit shape (sty_e16a2cd7 AC7): the same Claude-shaped
// hookSpecificOutput/additionalContext envelope is used for both Claude and Grok
// harnesses. Grok documents additionalContext as a working channel (Stop non-error
// feedback; Claude-compat vocabulary) and does not define a distinct
// UserPromptSubmit output envelope. PreToolUse deny still splits harnesses because
// Claude rejects top-level decision/reason — a real schema conflict that context
// injection does not share. Finding recorded on sty_e16a2cd7 as grok-prompt-contract.
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

// hookPromptReminder is the CONCISE standing nudge the UserPromptSubmit hook
// re-injects every turn (the full rule is the satelle-edits-require-a-story
// principle injected at SessionStart). It keeps the engaged-story discipline in
// front of the agent between session starts — a single PreToolUse gate is not the
// only line of defence (sty_949e8739).
const hookPromptReminder = "satelle: edits require an ENGAGED story. Before any Edit/Write/create/delete, engage a story in a performing state — `satelle story create …` then `satelle story set <id> --status plan` — and drive it through its workflow. Research uses read tools (Read/grep/Glob), never Edit/Write. The edit gate enforces this; never route around it."

// gateNotWiredWarning is the LOUD banner the UserPromptSubmit self-check prepends
// when it can confidently see the PreToolUse edit gate is NOT wired into the
// repo's committed hook settings — the countermeasure to a silently inert gate
// (the incident this story fixes): without a wired gate, code edits are ungated
// with no signal.
const gateNotWiredWarning = "⚠️ satelle: the edit gate is NOT wired into this repo's hooks — code edits are currently UNGATED. Do NOT edit code until enforcement is live: run `satelle init` to (re)install the PreToolUse gate, then restart the session so the harness loads it."

// promptEngagedCeiling is the byte budget for the live-seat replacement text.
// The engaged form replaces the static reminder, so it must never be longer
// (sty_e16a2cd7 AC4).
var promptEngagedCeiling = len(hookPromptReminder)

// formatEngagedPrompt builds the live-seat UserPromptSubmit body: story id,
// status, forward target(s) + gates from the route, and the story set
// command (sty_e16a2cd7). Returns "" when there is nothing useful to say
// (no id, no advance options) so the caller keeps today's reminder+seat path.
// Never includes the static create-and-engage reminder — it REPLACES it.
func formatEngagedPrompt(info seatInfo, now time.Time) string {
	if info.ItemID == "" || len(info.Advance) == 0 {
		return ""
	}
	state := info.State
	if state == "" {
		state = "?"
	}
	hb := formatSeatAge(now.Sub(info.HeartbeatAt))

	// Build advance clauses and gate lists; degrade under the ceiling.
	type targetView struct {
		to    string
		gates []string
	}
	views := make([]targetView, 0, len(info.Advance))
	for _, a := range info.Advance {
		views = append(views, targetView{to: a.To, gates: append([]string(nil), a.Gates...)})
	}

	build := func(capGates int, withGates, withSeat bool) string {
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
		msg := fmt.Sprintf("satelle: %s ENGAGED (%s, hb %s) — advance: %s",
			info.ItemID, state, hb, strings.Join(advParts, " | "))
		if withGates && len(gateParts) > 0 {
			msg += "; gates: " + strings.Join(gateParts, " | ")
		}
		if withSeat {
			msg += ". Seat: `satelle story seat`"
		}
		return msg
	}

	// Degradation ladder: full → cap gates at 3 → drop gates → drop seat → give up.
	for _, try := range []struct {
		capGates  int
		withGates bool
		withSeat  bool
	}{
		{0, true, true},  // full (0 = no cap)
		{3, true, true},  // cap gates
		{0, false, true}, // drop gates
		{0, false, false},
	} {
		out := build(try.capGates, try.withGates, try.withSeat)
		if len(out) <= promptEngagedCeiling {
			return out
		}
	}
	return ""
}

// runHookPrompt is the UserPromptSubmit handler: it re-injects the concise
// edits-require-a-story reminder and, when a gate-liveness self-check confidently
// finds no wired edit gate, prepends the LOUD not-wired warning. With a LIVE
// seat that has forward route advances, the reminder is REPLACED by the engaged
// form (id, status, next gate, story set) (sty_e16a2cd7). Otherwise a seat line
// is appended as before (sty_1738f973 AC6). Fails open — a resolve/read failure
// injects only the reminder.
func runHookPrompt(out io.Writer) error {
	body := hookPromptReminder
	now := time.Now().UTC()
	// Seat + heartbeat: fail-open (resolve error leaves the static reminder;
	// heartbeat write failures never block the prompt) (sty_3bb1d8be, sty_e16a2cd7 AC6).
	if info, engaged, err := currentSeatTouch(); err == nil {
		if engaged {
			if eng := formatEngagedPrompt(info, now); eng != "" {
				body = eng // REPLACE the create-and-engage reminder
			} else {
				// Live seat but no forward advance (e.g. release after AC5, or a
				// baseline workflow whose only next is terminal): keep today's
				// reminder + seat line so the turn still names the holder
				// (architecture note on sty_e16a2cd7 plan).
				body = appendSeatToPrompt(body, info, now)
			}
		} else {
			body = appendSeatToPrompt(body, info, now)
		}
	}
	if root, ok := repoRootForHook(); ok {
		if wired, checked := gateWiredInSettings(root); checked && !wired {
			// Warning is orthogonal to the reminder/engaged body and does not
			// count against promptEngagedCeiling.
			body = gateNotWiredWarning + "\n\n" + body
		}
	}
	return emitAdditionalContext(out, "UserPromptSubmit", "", body)
}

// runHookStopcheck is the Stop handler: a post-hoc detector for the exact
// incident the PreToolUse gate prevents. It blocks finishing when the tree has
// uncommitted non-exempt in-repo changes while no story is engaged — so an
// ungated edit cannot be silently finished even if the PreToolUse hook never
// fired. Honours stop_hook_active (never re-blocks its own block) and fails open
// (git absent, clean tree, only exempt changes, or a story engaged → allow).
func runHookStopcheck(raw []byte, out io.Writer) error {
	if stopHookActive(raw) {
		return nil // anti-loop: never re-block a stop we already blocked
	}
	root, ok := repoRootForHook()
	if !ok {
		return nil // fail open — unresolvable repo blocks nothing
	}
	engaged, err := storyEngaged()
	if err != nil || engaged {
		// A story is engaged (edits are legitimate) OR engagement is unknowable —
		// stopcheck is a secondary detector, so it fails OPEN rather than blocking a
		// finish on a broken deployment (the PreToolUse gate is the fail-closed one).
		return nil
	}
	gated, derr := dirtyGatedPaths(root)
	if derr != nil || len(gated) == 0 {
		return nil // git absent / clean / only exempt (.satelle) changes — nothing to flag
	}
	return emitStopBlock(out, stopcheckReason(gated))
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
	return ev.Snake || ev.Camel
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

// stopBlockOut is the Stop-hook block payload (AC6 / sty_5e4bc568 audit): Claude
// Code's Stop control channel IS top-level {"decision":"block","reason":…} —
// distinct from PreToolUse's hookSpecificOutput shape. The same top-level shape
// best-effort covers Grok. No harness split needed; confirmed still honored.
type stopBlockOut struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// emitStopBlock writes the Stop block JSON (one line) and returns nil — the Stop
// hook blocks via the stdout decision, not an exit code (its wiring carries no
// '|| exit 2').
func emitStopBlock(out io.Writer, reason string) error {
	b, err := json.Marshal(stopBlockOut{Decision: "block", Reason: reason})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(b))
	return nil
}
