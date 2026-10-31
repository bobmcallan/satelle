---
name: satelle-start-small
type: principle
tags: [type:principle, principles:seat]
applies_to: ["*"]
description: Start small, prove it and scale. A change ships as the smallest slice that proves its behaviour; scope, concurrency or rules grow only once the ledger shows the smaller form held.
---

Start small, prove it and scale. Ship the smallest slice that proves the
behaviour the card states ([[satelle-story-shape]]). Widen scope, run more work
in parallel, or add rules only once the ledger shows the smaller form held.
Parallel changes on one area of code raise throughput but turn each release
into a hand-merge: prove one before running many.
