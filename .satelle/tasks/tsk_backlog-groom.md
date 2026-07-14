---
id: tsk_backlog-groom
type: task
status: done
priority: medium
category: substrate
tags: backlog-groom, quality, substrate
created: 2026-07-14T00:00:00Z
updated: 2026-07-14T00:00:00Z
---

# Groom a sprint or epic — apply review findings without changing objectives

A re-runnable **backlog restructure** task. The operator (or agent) first runs a
**read-only review** (`review sprint:N` / `review epic:x` — conversational, no
engagement). Findings are **injected** into a new execution of this header; the
executor applies only HOW changes (order, tags, split, AC accuracy, body
alignment). Story **OBJECTIVES** are inviolate.

New execution each run (done is terminal — re-run = new execution):

```bash
# 1) Review (read-only; no task required)
#    Enumerate: satelle story list --tag sprint:N   (or --tag epic:theme)

# 2) Restructure — inject findings into a fresh execution
satelle execution create --parent tsk_backlog-groom \
  --title "Groom sprint:N" \
  --body "$(cat <<'EOF'
## Findings
(paste review findings)

## Apply-to
- story ids / tags in scope
- allowed mutations: reorder (order:N), retag, split, AC/body accuracy
- FORBIDDEN: change any story OBJECTIVE or AC intent

## Escalation
If a finding requires an objective change or is ambiguous, stay in_progress and
surface the conflict — or cancel. Do not invent a blocked status.
EOF
)"
```

Then drive the execution: `backlog → in_progress → done` on
`satelle-task-workflow` (validate-before → task-run → validate-after).

## Action

1. Enumerate the target set with `satelle story list --tag <sprint:N|epic:theme>`
   (compose with `--status` / `--parent` as needed). Prefer `--tag` over
   client-side JSON filtering.
2. Read each member story (title, body OBJECTIVE, ACs, tags, order, parent).
3. Apply only the injected findings that stay within each story's OBJECTIVE:
   - reorder / retag / split into new stories / rewrite ACs for accuracy
   - body accuracy that clarifies HOW without changing intent
4. On conflict with an OBJECTIVE: **do not apply**. Stay `in_progress` and
   surface the conflict to the operator, or move to `cancelled`. Never invent a
   `blocked` status (task workflow has only done|cancelled terminals).
5. Leave VERIFICATION evidence (list of mutations applied + any escalations) in
   the execution body or a doc under the task/execution folder.

## Verification

1. Every applied mutation is listed with story id and before/after summary.
2. No member story's stated OBJECTIVE (or AC intent) was altered — only HOW
   (order, tags, split, accuracy).
3. Escalations (if any) are explicit; nothing was silently guessed.
4. Enumeration used tag-filter when available (`story list --tag`).

## Guardrails

- Concurrent with story work: this is a **task** execution (task lease only) —
  it never takes the story seat.
- Review stays free research; only this restructure run engages.
- Objective-adherence is judged at `satelle-task-validate-after-review`.
