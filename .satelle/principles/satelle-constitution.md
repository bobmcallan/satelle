---
name: satelle-constitution
scope: project
kind: principle
tags: [kind:principle, principles:always, area:substrate]
applies_to: ["*"]
description: The project constitution — the local/repo definition the agent reads as order-zero context. satelle is a HARNESS that runs this repo's process as configuration; it ships no process of its own as code. Process, gates, workflows, and opinions are authored substrate (documents, principles, skills, workflow config) the operator edits without a binary release — never Go branches. The binary holds MECHANISM only.
---

# satelle constitution

satelle is a **harness** that runs any repo's process as **configuration**. It
ships no process of its own as code. This constitution is the local/repo
definition the agent reads first: it says what satelle *is*, what belongs in the
binary versus the authored substrate, and the conventions that keep the two
separable. It is the order-one companion to [[satelle-repo-agnostic]] (which is
the order-zero guard on every code change).

## Configuration over code

Process, gates, workflows, and opinions are **substrate** — markdown the
operator authors and edits without a binary release. The binary *runs* them; it
never *is* them. When a change proposes baking a process, a gate, or an opinion
into Go, that is the violation this constitution exists to prevent — move it to
the substrate under `.satelle/`.

- **No gate as code.** A gate is configuration: a reviewer skill (LLM judgment)
  plus an optional functional check (a deterministic command the gate carries).
  The binary runs the gate (`agent -p → verdict`); it never decides the verdict
  in a Go branch. A version-bump rule, a debt rule, or a surface rule compiled
  into the binary is the defect — no other repo could change it.
- **Determinism is not a licence to hardcode the DECISION.** A gate's pass/fail
  rule stays configuration even when deterministic. The functional check it names
  is a COMMAND that MAY invoke a binary mechanism (build/test, enumerate a
  surface, diff a version against its tag): running mechanism is the binary's
  job, deciding is the gate's.
- **Gates read this constitution,** so enforcement tracks evolving intent
  without a recompile.

## Mechanism vs. substrate (what lives where)

The binary holds **mechanism only**:

- the verb registry and dispatch seam (`CLI / web → verb.Dispatch → store`),
- the per-repo SQLite stores (stories, tasks, ledger) and the directory monitor
  that indexes authored markdown,
- the reviewer run path (isolated, read-only `agent -p` → JSON verdict),
- the embedded *canonical defaults* (the baseline workflow and the
  required-structure reviewer) as the single source of those bytes,
- the local web project page and the CLI surface.

**Behaviour** — which gate runs, what it judges, the lifecycle a repo enforces,
the opinions beyond the required structure — lives in the **substrate**
(`.satelle/{documents,principles,skills,workflows}`). A repo layers its own
authored markdown ON TOP of the embedded defaults (a same-named file overrides
its default); it never edits the embedded source.

## Substrate naming

A substrate artifact's name encodes its type so owner and kind read from the
name alone:

- **Principles** — kebab-case, no prefix (`satelle-repo-agnostic`,
  `satelle-constitution`).
- **Skills / reviewers** — `satelle-<kebab>`; a reviewer gate is
  `satelle-<object>-<stage>-review` (`satelle-story-done-review`).
- **Workflows** — `satelle-<kebab>-workflow` (`satelle-baseline-workflow`).

## The test

If another repo installs satelle, only the **required structure** travels with
the binary; everything opinionated — the states beyond the baseline, this repo's
discipline, deploy mechanics — stays in *that* repo's authored substrate. A
change that only makes sense because of how *this* repo works belongs in
`.satelle/`, not in the binary.

See [[satelle-repo-agnostic]].
