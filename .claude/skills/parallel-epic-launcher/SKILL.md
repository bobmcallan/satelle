---
name: parallel-epic-launcher
description: >-
  Materialise a parallel-epic wave: one git worktree + branch + driving session
  per child story. Use when orchestrating satelle-epic-parallel-workflow,
  launching parallel dogfood leaves, or when asked to "launch parallel stories",
  "worktree per story", or "parallel epic launcher".
---

# Parallel epic launcher (harness-side)

Satelle **governs** parallelism (`[gate] allow_parallel`, per-story leases,
workflow stamps) but does **not spawn** executors — the executor is in-loop by
design. This skill is **harness configuration**: create worktrees and launch
(or precisely instruct) one independent driving session per child.

## Preconditions

1. `[gate] allow_parallel = true` in the repo's satelle config for the run
   (dry-run story decides whether it stays long-term).
2. Each child stamped `workflow:satelle-parallel-story-workflow`.
3. Epic stamped `workflow:satelle-epic-parallel-workflow` when driving via the
   epic graph.
4. You are in the **main worktree** of the repo (launcher creates sibling
   worktrees).

## Naming

| Resource | Pattern |
| --- | --- |
| Worktree path | `../<repo>-wt-<sty_id>` (sibling of the main clone) |
| Branch | `story/<sty_id>` from current `main` |
| Session cwd | that worktree path |

Example for repo `satelle` and `sty_4a5c6924`:
`../satelle-wt-sty_4a5c6924` on branch `story/sty_4a5c6924`.

## Launch mechanism (chosen default)

**Prefer: instruct the operator (or orchestrator) to open one CLI agent session
per worktree** with an explicit engage prompt. That is the simplest path that
yields a **genuinely independent** driving session (separate process, full
tools) without inventing a satelle spawn API.

Optional when automation is available in the harness:

- `claude` / `grok` headless in each worktree with a one-shot prompt, **or**
- Agent-tool worktree isolation if the host already supports it

Record which mechanism you used in the epic session notes.

## Procedure

Given a list of story ids (one wave):

```bash
REPO_ROOT=$(git rev-parse --show-toplevel)
REPO_NAME=$(basename "$REPO_ROOT")
MAIN_BRANCH=$(git rev-parse --abbrev-ref origin/main 2>/dev/null || echo main)
git fetch origin "$MAIN_BRANCH" 2>/dev/null || true

for STY in "$@"; do
  WT="${REPO_ROOT}/../${REPO_NAME}-wt-${STY}"
  BR="story/${STY}"
  # Branch from main if missing
  if ! git show-ref --verify --quiet "refs/heads/${BR}"; then
    git branch "${BR}" "origin/${MAIN_BRANCH}" 2>/dev/null || git branch "${BR}" "${MAIN_BRANCH}"
  fi
  if [ ! -d "$WT" ]; then
    git worktree add "$WT" "${BR}"
  fi
  echo "WORKTREE ${STY} -> ${WT} branch ${BR}"
done
```

### Per-worktree session prompt (paste into each session)

```
Engage story <STY> and drive it through satelle-parallel-story-workflow to ready
(not done, not merged to main). Work only in this worktree on branch story/<STY>.
Never push main. Never edit CHANGELOG.md for release. Shared satelle DB is the
runtime plane (git common-dir repo key). Stop at ready when the branch is green
and pushed.
```

### Mapping report (return to epic orchestrate session)

```
| story | worktree | branch | session |
| --- | --- | --- | --- |
| sty_… | ../satelle-wt-sty_… | story/sty_… | <pid or "operator"> |
```

Epic monitors with:

```bash
satelle story list --parent <epic_id>
satelle story seat
```

## Teardown (after epic integrate merged the branch)

```bash
git worktree remove "../${REPO_NAME}-wt-${STY}" --force  # when clean/merged
git branch -d "story/${STY}"  # optional after merge
```

Do not remove a worktree while the child is still in_progress/ready and unmerged.

## Safety

- Flipping `allow_parallel` is part of the dogfood story, not a silent permanent
  default unless the dry-run close records that decision.
- Double-dispatch is still impossible per story (leases); parallel means
  **different** stories engaged concurrently.
- Worktrees share the satelle runtime plane via `git rev-parse --git-common-dir`
  repo keying — do not copy `.satelle` databases between worktrees.

## Verify (order:3 AC)

With two story ids and `allow_parallel=true`, create two worktrees, start two
sessions, engage both; `satelle story seat` / list shows concurrent engagement.
No Go changes in this skill.
