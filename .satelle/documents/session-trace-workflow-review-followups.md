---
type: document
title: Session trace — epic:workflow-review-followups (commitgate compound-command deny)
description: Detailed timeline and root-cause analysis of the failed order:2 commit after a successful order:1 substrate close, for operator/agent review.
tags: [document, session-trace, diagnosis, epic:workflow-review-followups, commitgate]
timestamp: '2026-07-12T00:00:00Z'
---

# Session trace: epic:workflow-review-followups

**Purpose.** Reconstruct what the agent session did, what failed, and why — so the residual work and the process defect can be judged without re-deriving from chat.

**Session objective.** User: `implement epic:workflow-review-followups`  
**Epic.** `sty_4603db29` (category `feature`, tag `epic:workflow-review-followups`)  
**Children (order).**

| Order | Id | Category | Title (short) | Status at end of traced session* |
| --- | --- | --- | --- | --- |
| 1 | `sty_e3687ec4` | substrate | Record hybrid decision (A) | **done** |
| 2 | `sty_e433dee4` | substrate | Substrate prose pass | **in_progress** (re-engaged during diagnosis; was backlog after the failure) |
| 3 | `sty_64ffe668` | substrate | Clear format lag substrate+task | backlog (work authored on disk, uncommitted) |
| 4 | `sty_ca97c680` | feature | Plan-fidelity in code-ac-review | backlog (work authored on disk, uncommitted) |
| folded | `sty_dfbbf9ad`, `sty_5c325147`, `sty_3e65beeb` | — | Folded into order:2 | cancelled (pre-session) |

\* “Traced session” ends when the operator asked for this document. Diagnosis of the deny **re-engaged** `sty_e433dee4` (see §5).

**Repo HEAD at failure.** `26a4781` — `docs(substrate): record hybrid agent-model decision (A) (sty_e3687ec4)`  
**Not pushed** (order:1 commit local only at time of trace).

---

## 1. What the agent intended

Drive children **in order** on the substrate workflow (`backlog → in_progress → done`, close gated by `satelle-substrate-only-check`), then close the epic.

Substrate close rule (relevant): the functional check finds commits with `git log --grep=<sty_id>` and requires the slice under `.satelle/` / `docs/` / `edit_exempt_paths`. So each child needs **its own commit** whose subject ends with `(sty_…)`.

Enforcement that matters for this incident:

| Hook | When | Rule |
| --- | --- | --- |
| `satelle hook gate` | PreToolUse Edit/Write | Block non-exempt path edits without an engaged story |
| `satelle hook commitgate` | PreToolUse **Bash** | If command string contains `git commit` or `git push`, require **some** engaged story **before the shell runs** |
| `satelle hook stopcheck` | Stop | Secondary detector for dirty non-exempt tree with no engagement |

**Important asymmetry.** `[gate] edit_exempt_paths = [".satelle/", ".claude/"]` allows **editing** substrate without engagement. **Committing** still requires engagement — `commitgate` does not care that the paths are exempt.

---

## 2. Timeline (chronological)

### 2.1 Orient

1. Resolved epic via `satelle story get` / story files under `.satelle/stories/`.
2. Confirmed children, cancelled fold-ins, order tags.
3. Read project workflow: perform steps **in-loop** `agent=executor`; plan/reviewers dispatched.
4. Confirmed `format-drift`: substrate + task workflows had `promptless_performing` on `in_progress`; project CLEAN.
5. Engaged **order:1 only**: `satelle story set sty_e3687ec4 --status in_progress` → **ok**.

### 2.2 Implementation bulk (before per-story commits)

Agent authored **all** intended slices on disk while only order:1 was engaged (edits under `.satelle/` are edit-exempt, so the edit gate allowed this):

| Slice | Files (intended owner) |
| --- | --- |
| Order 1 | `documents/decision-hybrid-agent-model.md`, `principles/satelle-agent-model.md` (fix worker example → planner / hybrid) |
| Order 2 | `skills/code.md` (in-loop), `skills/release.md` + `skills/satelle-story-release-review.md` (named dogfood triad), `documents/estimate-and-lessons.md`, `documents/browser-multiserve-flake.md` |
| Order 3 | `skills/substrate.md`, `skills/task-run.md`, `workflows/satelle-substrate-workflow.md` + `satelle-task-workflow.md` (`prompt="@skill:…"`) |
| Order 4 | `skills/satelle-code-ac-review.md` (hard plan-fidelity), `documents/decision-plan-fidelity-in-code-ac-review.md` |

Validation at that point (read-only):

- `satelle reindex` — ok  
- `satelle skill validate` — 32 pass  
- `satelle workflow validate` — 4 pass  
- `satelle workflow format-drift` — **0 findings** (substrate + task CLEAN)  
- `satelle agent validate` — green  

### 2.3 Order:1 commit + close — **succeeded**

Single bash tool call (conceptually):

```text
git add <order1 files>
git commit -m "… (sty_e3687ec4)"
./satelle story set sty_e3687ec4 --status done
```

**Why commitgate allowed it.** At PreToolUse time, `sty_e3687ec4` was already `in_progress` (engaged). `isGitCommitOrPush` matched; `storyEngaged()` true → allow → shell ran → commit → close accepted by `satelle-substrate-only-check`.

Result:

- Commit: `26a4781 docs(substrate): record hybrid agent-model decision (A) (sty_e3687ec4)`
- Story status: **done**
- Ledger: `in_progress → done` + step summary

### 2.4 Order:2 engage + commit — **failed (the issue)**

Single bash tool call:

```bash
./satelle story set sty_e433dee4 --status in_progress 2>&1 | tail -20
git add .satelle/skills/code.md .satelle/skills/release.md \
  .satelle/skills/satelle-story-release-review.md \
  .satelle/documents/estimate-and-lessons.md \
  .satelle/documents/browser-multiserve-flake.md
git commit -m "docs(substrate): prose pass … (sty_e433dee4)"
./satelle story set sty_e433dee4 --status done 2>&1 | tail -40
```

**Observed tool result (only line that mattered):**

```text
Hook denied: satelle: refusing to commit/push with no engaged story —
engage a story (satelle story set <id> --status plan) so the change is
tracked through the workflow.
```

**Immediate evidence after failure:**

| Check | Result |
| --- | --- |
| `sty_e433dee4.status` | **`backlog`** (never transitioned) |
| Ledger for `sty_e433dee4` | only earlier `story_updated` (restructure); **no** `status_transition` |
| `git log -1` | still `26a4781` (order:1 only) |
| Working tree | order:2–4 files still dirty / untracked |

So: the shell body **did not run** (or at least never reached a successful `story set`). Engagement for order:2 never happened; commit never happened.

---

## 3. Root cause analysis

### 3.1 Mechanism (primary)

`commitgate` is a **PreToolUse** Bash hook (`.claude/settings.json`):

```json
"matcher": "Bash",
"command": "satelle hook commitgate || exit 2"
```

PreToolUse runs **before** the harness executes the command. Gate logic (`internal/cli/cmd_hook.go`):

1. Parse the **entire** Bash payload string.
2. If it contains substring `git commit` or `git push` (`isGitCommitOrPush` — case-insensitive substring), treat as commit/push.
3. Call `storyEngaged()` against the **current store** (any item in a non-terminal engaging state of its governing workflow).
4. If none engaged → **deny** the tool call; **no line of the script runs**.

For the order:2 compound command:

| Time | Engaged story? | Command contains `git commit`? | Gate |
| --- | --- | --- | --- |
| PreToolUse (before any shell) | No (`sty_e3687ec4` already **done**; `sty_e433dee4` still **backlog**) | Yes | **DENY** |
| (never reached) | Would become yes after `story set in_progress` | — | — |

**Primary defect is agent process, not a broken gate:** engage and commit were fused into one Bash tool call, so the gate evaluated engagement *before* the engage line could run.

### 3.2 Why order:1 did not hit this

Order:1 engagement was a **prior, separate** tool call. Commit bash started with engagement already true. Closing to `done` was after commit inside the same bash — allowed because engagement is checked only at PreToolUse start, not after each line.

### 3.3 Contributing process smells

| Smell | Effect |
| --- | --- |
| Bulk-authored order:2–4 while only order:1 engaged | Fine for **edit** (exempt paths); misleading for **commit ownership** (which commit greps which sty_id) |
| Compound “engage + commit + close” in one Bash | Systematically fails commitgate when no prior engaged story |
| Commit message template in deny text says `--status plan` | Substrate stories use `in_progress`, not `plan` — misleading remediation string (`noEngagedStoryCommitReason`) |
| No `set -e` / no assert-after-engage | Even if PreToolUse were post-line, silent engage failure would still leave a racey commit |

### 3.4 What this is *not*

- Not a substrate-only-check failure (close never reached).
- Not an edit-gate failure (writes under `.satelle/` succeeded).
- Not “git hooks” (no real `.git/hooks/pre-commit`; enforcement is harness PreToolUse).
- Not missing files (work is still on disk).
- Not that `story set` is broken for substrate (diagnosis later engaged `sty_e433dee4` successfully with EXIT 0).

---

## 4. Residual tree state (at document write)

```text
HEAD 26a4781  docs(substrate): record hybrid agent-model decision (A) (sty_e3687ec4)

 M .satelle/skills/code.md
 M .satelle/skills/release.md
 M .satelle/skills/satelle-code-ac-review.md
 M .satelle/skills/satelle-story-release-review.md
 M .satelle/workflows/satelle-substrate-workflow.md
 M .satelle/workflows/satelle-task-workflow.md
?? .satelle/documents/browser-multiserve-flake.md
?? .satelle/documents/decision-plan-fidelity-in-code-ac-review.md
?? .satelle/documents/estimate-and-lessons.md
?? .satelle/skills/substrate.md
?? .satelle/skills/task-run.md
(+ this session-trace document once written)
```

Suggested ownership when finishing:

| Commit | Story | Paths |
| --- | --- | --- |
| prose pass | `sty_e433dee4` | `code.md`, `release.md`, `satelle-story-release-review.md`, `estimate-and-lessons.md`, `browser-multiserve-flake.md` |
| format lag | `sty_64ffe668` | `substrate.md`, `task-run.md`, both workflows |
| plan fidelity | `sty_ca97c680` | `satelle-code-ac-review.md`, `decision-plan-fidelity-in-code-ac-review.md` (category is `feature` — restamp to `substrate` if closing via substrate-only-check) |

---

## 5. Diagnosis side effects (this trace session)

While building this document the agent **re-tested engagement**:

```bash
./satelle story set sty_e433dee4 --status in_progress   # EXIT 0 → in_progress
./satelle story set sty_e433dee4 --status backlog        # REJECTED: no edge in_progress→backlog
```

So after diagnosis:

- `sty_e433dee4` is **`in_progress`** (engaged).
- Cannot return to backlog without a declared recovery edge; drive forward (`done`) or `cancelled` with reason.
- This is convenient for finishing order:2 **if** commit is a **separate** Bash tool call (no need to re-engage).

---

## 6. Remediation (process + optional product)

### 6.1 Agent process (required to finish the epic)

Split tool calls:

1. **Bash A (no git commit/push):** `satelle story set <id> --status in_progress`  
2. Verify: `satelle story get <id>` → `"status":"in_progress"`  
3. **Bash B:** `git add … && git commit -m "… (sty_…)"`  
4. **Bash C (optional separate):** `satelle story set <id> --status done`  

Never put first engagement and first `git commit` in the same PreToolUse Bash payload when nothing else is engaged.

### 6.2 Product / substrate follow-ups (optional stories — not required to unblock)

| Idea | Why |
| --- | --- |
| Improve `noEngagedStoryCommitReason` | Mention `in_progress` (and substrate path), not only `plan` |
| Document “compound Bash + commitgate” | SessionStart / principle / commit skill: PreToolUse evaluates *before* script lines |
| commitgate ignores `git commit` after a same-script engage | Hard: PreToolUse cannot see future state; would need in-shell gate or two-phase policy |
| stopcheck already helps | Dirty exempt-only tree with no engagement is allowed; commit still blocked |

### 6.3 Resume checklist

```text
[ ] Confirm sty_e433dee4 still in_progress (or re-engage if not)
[ ] Commit order:2 files with (sty_e433dee4); set done
[ ] Engage sty_64ffe668; commit format-lag files; set done
[ ] Engage sty_ca97c680 (consider category substrate); commit plan-fidelity; set done
[ ] Push substrate commits (or batch push once children done — still needs engaged story for push!)
[ ] Close epic sty_4603db29 per parent/project workflow ACs
```

**Push caveat.** `git push` is also matched by `isGitCommitOrPush`. After all substrate children are `done`, a push still needs **some** engaged story (or a separate engaged vehicle). Options: push while a child is still `in_progress` after its commit; engage the epic if its workflow has a performing state; or push under a temporary engaged story. Do not assume push works with zero engagement.

---

## 7. Evidence pointers

| Artifact | Location |
| --- | --- |
| Order:1 commit | `git show 26a4781` |
| Hybrid decision | `.satelle/documents/decision-hybrid-agent-model.md` |
| commitgate source | `internal/cli/cmd_hook.go` (`commitgate`, `isGitCommitOrPush`, `storyEngaged`, `noEngagedStoryCommitReason`) |
| Hook wiring | `.claude/settings.json` → `PreToolUse` Bash → `satelle hook commitgate` |
| Substrate close check | `.satelle/skills/satelle-substrate-only-check.md` |
| Edit exempt config | `.satelle/satelle.toml` `[gate] edit_exempt_paths` |
| Order:1 ledger | `satelle ledger list --story sty_e3687ec4` |

---

## 8. One-line summary

**The order:2 commit failed because PreToolUse `commitgate` denied a single Bash tool call that both engaged the story and ran `git commit`, evaluating engagement before any shell line could run; after order:1 closed, nothing was engaged at gate time.** Work for orders 2–4 is still on disk; finish by splitting engage and commit into separate tool calls (order:2 is already re-engaged from diagnosis).
