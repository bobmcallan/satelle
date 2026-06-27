---
name: satelle-yagni
scope: system
kind: principle
tags: [kind:principle, principles:always]
applies_to: ["*"]
description: YAGNI — build a capability when a real need calls for it, not when you merely foresee one. Premature generality (a speculative abstraction, contract, tool, config knob, or layer of indirection) is paid up front for a need that may never arrive — pure cost that also makes the system harder to change in the directions that do. Prefer the smallest change that serves the objective. Adapted from satellites' yagni.
---

# YAGNI — you aren't gonna need it

Build a capability when a real need calls for it, not when you merely foresee
one. Do the simplest thing that could work for the objective in front of you, and
let the next confirmed need — not a guess about it — drive the next change.

Premature generality is not free: every speculative abstraction, contract, tool,
config knob, or layer of indirection must be built, tested, documented, and
carried by everyone who reads the code afterwards. Paid up front for a need that
may never arrive, it is pure cost — and it makes the system harder to change in
the directions that DO arrive.

- Prefer the smallest change that serves the objective. When tempted to add a new
  abstraction, parameter, or generalisation, first ask whether an existing one
  does the job, then whether the need is real NOW or only imagined.
- Solve the case you have, not the family of cases you picture. One concrete
  implementation you can refactor later beats a flexible framework no caller yet
  needs.
- This is not a licence to under-engineer. YAGNI counterbalances over-engineering;
  it never excuses skipping refactoring, tests, or error handling. Doing the
  simplest thing AND keeping the tree clean are the same discipline, not a
  trade-off — cutting those corners is how YAGNI decays into technical debt.

See [[satelle-constitution]], [[satelle-reviewer-self-contained]].
