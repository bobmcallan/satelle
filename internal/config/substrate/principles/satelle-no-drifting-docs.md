---
name: satelle-no-drifting-docs
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: The code and the configuration are the documentation. Do not write prose that restates them, and do not create a document nothing reads — both drift, and a drifted document misleads.
---

# satelle-no-drifting-docs — no drifting or orphan documents

**The code and the configuration are the documentation.** What a repo does is
written once, in the thing that does it; a second description of it can only fall
out of step.

1. **Do not restate.** Never create a document whose content is code,
   configuration, or another substrate artifact put into prose. Point at the
   source instead.
2. **Do not orphan.** Never create a document that nothing reads — no loader,
   gate, skill, principle or workflow refers to it. A document with no reader has
   no one to notice it is wrong.
3. **Explain beside the source.** Needed explanation belongs next to what it
   explains: a code comment, a frontmatter `description`, help text.
4. **A drifted document is a defect.** Delete it or replace it with a pointer;
   do not patch it to match and leave it to drift again. Never add a gate whose
   only job is to keep such a document in sync.

See [[satelle-constitution]], [[satelle-configure-freely]].
