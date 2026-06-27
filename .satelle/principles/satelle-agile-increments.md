---
name: satelle-agile-increments
scope: project
kind: principle
tags: [kind:principle, principles:always]
applies_to: ["*"]
description: Agile increments — deliver work as the smallest independent stories, each driven on its own and paired with its own focused commit. A batch of feedback is decomposed into single small stories, not bundled into one large story or one sweeping commit. This is THIS repo's delivery paradigm (project scope), not a satelle mechanism.
---

# Agile increments — small stories, one commit each

Deliver work the agile way: when a request arrives as a batch of feedback or a
broad goal, decompose it into the **smallest independent stories** that each
produce one observable change. Drive a single story to its terminal state, commit
it, then take the next. Do not bundle several items into one large story, and do
not fold several changes into one sweeping commit.

Why small increments: each story and its commit map one-to-one to a single
reviewable change. That keeps the history bisectable, makes a revert surgical,
shrinks the blast radius of any one change, and lets the operator see progress as
a steady cadence rather than a single late drop. A large story hides its risk
until the end; a small one surfaces it immediately.

- One feedback item → one small story → one focused commit. If an item is itself
  large, split it again until each story is something you can carry to done in a
  single pass.
- Engage one story at a time and finish it before engaging the next, so the
  working tree always reflects one in-flight change.
- The commit is part of the increment, not an afterthought: a story is not
  delivered until its change is committed with a message that names what changed.
- Small is not careless — each increment still passes its gates and leaves the
  tree clean. Decomposition reduces scope, never rigour.

See [[satelle-yagni]], [[satelle-done-is-last]], [[satelle-constitution]].
