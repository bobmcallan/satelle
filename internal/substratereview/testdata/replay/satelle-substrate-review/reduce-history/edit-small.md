---
name: edit-small
type: principle
description: Keep each edit small.
---

We used to gate every edit behind a full review. In sty_1a2b3c4d the team
found reviews took days, so sty_5e6f7a8b relaxed it, and later sty_9c0d1e2f
added the size cap after a large merge broke the build. Before that we tried
pairing, which was dropped in the spring.

Keep each commit under 400 changed lines.
