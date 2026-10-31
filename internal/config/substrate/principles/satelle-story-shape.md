---
name: satelle-story-shape
type: principle
tags: [type:principle, principles:seat]
applies_to: ["*"]
description: A story is behaviour-driven (BDD). The card states one behaviour — a role, its context, the outcome it gets — and each acceptance criterion is a technical requirement that meets it, provable at the gate that judges it.
---

A story is BDD. The card states a behaviour: a role, the context it acts in, and
the outcome it gets (given/when/then is one form). The ACs are the technical
requirements that meet that behaviour; each traces to the card. Each AC is
provable by the gate that judges it, with evidence that exists when that gate
runs. Evidence that exists only after release is an AC of the release step,
never of the code step. [[satelle-start-small]] [[satelle-agent-goals]]

A story may enter as a behaviour with no criteria. On a route with a plan step,
its criteria are authored at planning, under the plan's `## Acceptance criteria`,
judged there and applied to the story when work starts. On a route with no plan
step, the card is the whole spec.
