---
name: satelle-broken-windows
scope: project
type: principle
tags: [type:principle, principles:always]
applies_to: ["*"]
description: Broken windows — a failure you encounter is yours: fix it if cheap or in your change's path, otherwise file a tracked story naming it and surface it. Never silently pass a failure by. Never add a new red (a failing test, a skipped check, undocumented debt); known debt only shrinks, every quarantined failure named in a technical-debt register and owned by a story. This is THIS repo's working discipline (project scope), not a satelle mechanism — adapted from satellites' broken-windows.
---

# Broken windows

A failure you encounter is **yours**. Fix it if it is cheap or in your change's
path; otherwise file a tracked story that names it and surface it to the
operator. **Never silently pass a failure by** — an unowned failure is the broken
window that invites the next one.

Never add a **new red**. A change that introduces a failing test, a skipped or
disabled check, or undocumented debt is **not done**, however finished it looks.
Known debt only **shrinks**: every quarantined failure is named in the
technical-debt register and owned by a story, so nothing rots untracked.

At commit the tree is **clean, or its debt is a story you created**. "It was
already broken" is not a pass — inheriting a failure you choose to build on top
of makes it yours to name. The discipline is not perfectionism; it is refusing to
let the count of unowned failures climb.

See [[satelle-agent-goals]], [[satelle-done-is-last]].
