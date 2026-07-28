---
name: satelle-executor-deliverables
type: principle
tags: [type:principle]
applies_to: ["*"]
description: An executor is told the deliverables it owes — never how gates decide. Declare required outcome artifacts (and their independent verification counterparts); do not expose reviewer judging criteria.
---

# Executor deliverables — declare outcomes, never gate criteria

An **executor** is told **what artifacts it must produce** before hand-off.
It is **never** told how gates decide. An executor that models its gates
optimises for the verdict rather than the work.

## The rule

1. **Declare required deliverables** in the executor rubric (and, when
   durable, in this principle's spirit): concrete outcome artifacts the
   implementer owes — named, observable, and finishable before the exit edge.
2. **Never declare judging criteria** to the executor: do not list what a
   reviewer will accept or reject, the reviewer's checklist, or how to "pass
   the gate". The gates judge; the executor builds.

The anti-workaround half — never route around a gate; surface a gap and stop —
is already [[satelle-agent-goals]]. This principle does not restate it; it
adds the complementary half: **what to produce**, not **how to be judged**.

## Why (the evidence trap)

When an executor is left without a closing account of outcomes, reviewers
reconstruct the mapping from the tree on every round. Worse, limited gate
awareness already produces a failure mode where a test that **existed,
compiled, passed, and measured the wrong thing** survives multiple expensive
review rounds. Widening the executor's model of the gates would likely
**sharpen** optimisation-for-verdict, not fix it. The fix is an outcome
artifact the executor must close with — a realised mapping of evidence to
criteria, format and lint evidence — not a deeper brief of reviewer rubrics.

## Every required artifact must be verifiable

A principle (or rubric) that lists required artifacts can decay into ceremony
the executor satisfies formally. Mitigation: **every required artifact is
diffable against an independent source**.

| Deliverable | Independent counterpart |
| --- | --- |
| Closing per-criterion evidence (realised proof + divergence from plan) | The plan's named proof per criterion; the tree's actual tests or checks |
| Format and lint clean before hand-off | The workflow's coded functional check on the implement-to-test edge, when the repo authors one |

An artifact nobody can diff against is either given a counterpart or
**dropped** — ceremony is not an acceptable outcome.

## What this is not

- Not a license to hide process: the workflow and gate skills remain the
  authority for transitions (see [[satelle-agent-goals]]).
- Not a reason to expand executor awareness of reviewer rubrics.

See [[satelle-agent-goals]], [[satelle-agent-model]],
[[satelle-reviewer-self-contained]].
