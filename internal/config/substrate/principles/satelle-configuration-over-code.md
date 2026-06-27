---
name: satelle-configuration-over-code
scope: system
kind: principle
tags: [kind:principle, principles:always]
applies_to: ["*"]
description: satelle runs a repo's process as configuration, never as code. Process, gates, workflows, and opinions are authored substrate (markdown the operator edits without a binary release); the binary holds mechanism only and runs that substrate — it never IS it. This is an embedded canonical default the satelle binary ships to every repo.
---

# Configuration over code

satelle is a **harness**: it *runs* a repo's process, it never *contains* it.
Process, gates, workflows, and opinions are **substrate** — markdown the operator
authors and edits without a binary release. The binary holds **mechanism**; the
substance is the `.md`. When a change proposes baking a process, a gate, or an
opinion into the binary, that is the violation this principle exists to prevent —
it belongs in the authored substrate, not in code.

- **No process as code.** A gate is configuration: a reviewer (LLM judgment) plus
  an optional functional check (a deterministic command the gate carries). The
  binary *runs* the gate and records its verdict; it never *decides* the verdict
  in a code branch. A version rule, a debt rule, or a surface rule compiled into
  the binary is the defect — no other repo could change it.
- **Determinism is not a licence to hardcode the decision.** A gate's pass/fail
  rule stays configuration even when it is deterministic. The check it names is a
  command that MAY invoke binary mechanism (build, test, enumerate a surface, diff
  a version); running mechanism is the binary's job, deciding is the gate's.
- **Load layer, not opinion layer.** The binary is the load-and-run layer: it
  indexes the authored substrate, dispatches verbs, runs reviewers, and persists
  evidence. Which gate runs, what it judges, and the lifecycle a repo enforces all
  live in the substrate, so enforcement tracks evolving intent without a recompile.
- **Embedded defaults are mechanism, not process.** Only the required structure
  travels embedded in the binary as the single source of those bytes. Everything
  opinionated — the states beyond the baseline, a repo's discipline, deploy
  mechanics — stays in that repo's authored substrate, layered on top.

The test: if another repo installs satelle, only the required structure travels
with the binary; a change that only makes sense because of how one repo works
belongs in that repo's substrate, not in the binary.

See [[satelle-constitution]], [[satelle-repo-agnostic]].
