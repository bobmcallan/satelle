---
name: satelle-dispatch-pointers
type: principle
tags: [type:principle, principles:seat]
applies_to: ["*"]
description: Before designing how work reaches an agent, establish what it can already read and run; send it the instruction and pointers, never content it can fetch — a design that copies readable content into a prompt is wrong whatever its size.
---

Before designing how work or evidence reaches an agent, establish what that
agent can already read and run: its grant, the CLI, the files on disk. Send it
the instruction and pointers (ids, paths, retrieval commands) and never content
it can fetch for itself. A design that copies readable content into a prompt is
wrong whatever its size, and bounding or batching that content repeats the
mistake. [[satelle-agent-model]] [[satelle-start-small]]
