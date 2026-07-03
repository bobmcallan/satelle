---
name: reviewer-skill-author
description: Author and audit satelle REVIEWER skills so they are strictly review-only — an isolated, read-only gate that JUDGES what was implemented and returns a verdict, never implementing or mutating anything itself. Use when creating a new `satelle-*-review` gate skill, converting an executor step into a reviewer gate, or checking that an existing reviewer skill hasn't drifted into doing work. Triggers: "reviewer skill", "gate skill", "review-only", "author a gate", "audit reviewers", "satelle-*-review".
---

# Authoring review-only reviewer skills (satelle)

A satelle **reviewer** is an isolated sub-process spawned at a workflow transition
with a **read-only** tool grant. Its single job is to **judge** whether the edge
may be taken — reading the repository for evidence — and emit a verdict. It never
edits, writes, commits, or otherwise mutates the tree. The **executor** (the
in-loop driving session) does the work; the reviewer only gates it.

This skill scaffolds a new reviewer and audits an existing one against that
contract.

## The review-only contract (the invariant)

A reviewer skill MUST:
- **Judge, never do.** It reads (`Read`/`Grep`/`Glob`) to find evidence and
  decides accept/reject. It must contain NO instruction to create, modify, fix,
  format, stage, commit, push, or otherwise change any file or state.
- **Be self-contained.** Everything it needs to judge is in its own rubric plus
  the repo it can read + the transition payload on stdin (`{story, from, to}` —
  `story` is the item, carrying title/body/acceptance criteria/parent).
- **End in exactly one verdict**, nothing else of that shape:
  ```json
  {"decision": "accept", "notes": ""}
  ```
  `decision` is `"accept"` or `"reject"`; `notes` is a brief, actionable string
  (name the specific gap on reject) and may be empty on accept.
- **Be a fair gate, not a perfectionist** — judge the item's stated acceptance
  criteria as written, not extra requirements.

A functional/coded check is the deterministic sibling: a reviewer skill MAY carry
a self-contained ```check``` block (pure shell reading the stdin payload, exit 0 =
pass) INSTEAD of an agent judgment. A coded check still only DECIDES — it may run a
mechanism (build/test/enumerate) but must not mutate the tree.

## Naming (satelle constitution)

- **Structure reviewer** (validates an artifact on create/upsert):
  `satelle-<object>-review` — e.g. `satelle-story-review`.
- **Workflow stage gate** (gates one transition):
  `satelle-<object>-<stage>-review` — e.g. `satelle-story-release-review`,
  `satelle-story-done-review`.
- Frontmatter: `type: skill`, `tags: [type:skill, type:reviewer]` (add
  `type:functional-check` when it carries a ```check``` block), `scope: system`
  for an embedded default or `project` for a repo override, and a `description`
  that says which edge it gates and what it judges.

## Scaffold a new reviewer

```markdown
---
name: satelle-<object>-<stage>-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: <edge it gates> gate. An isolated, read-only reviewer judging whether <transition> may proceed — that <the concrete condition>, reading the repo for evidence. Judges, never enacts.
---

# <Object> — <stage> gate

You are an isolated, **read-only** reviewer deciding whether <transition>. You
receive `{story, from, to}` on stdin; `story` is the item. Read the repository
(Read/Grep/Glob) to verify; do not modify anything and do not run commands that
mutate state.

## How to judge

Work through <what to check> and look for concrete evidence in the repository:
- <evidence 1>
- <evidence 2>

- **Accept** when <the condition> is satisfied by evidence you can see.
- **Reject** when <gap>; name the specific gap so the executor can fix and resubmit.

## Verdict

Reply with exactly one JSON object:

```json
{"decision": "accept", "notes": ""}
```
```

## Audit an existing reviewer (checklist)

Flag the skill as NOT review-only if any of these are true:
1. It tells the agent to **Edit/Write/create/modify/fix/format/stage/commit/push**
   anything, or to "make the change", "add the test", "bump the version".
2. Its tool expectations include write/mutation tools rather than read-only
   (`Read`/`Grep`/`Glob`) — a reviewer binding grants read-only; naming write
   tools is a smell that the rubric expects to do work.
3. It never emits a `{"decision": …}` verdict, or emits more than a verdict.
4. It re-implements the executor's job "to check" (e.g. "run gofmt to see if it's
   formatted" — instead READ for the evidence, or carry a ```check``` that runs a
   NON-mutating command).
5. It judges requirements the item never stated (perfectionism), or it is vague
   about what evidence flips accept↔reject.

For each finding, rewrite the offending instruction into a read-only judgment
("verify that X exists / would pass", not "do X"). Re-run `satelle skill validate`
after editing.
