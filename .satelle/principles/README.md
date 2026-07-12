# principles

Authored principles (markdown, `type: principle`).

**Residency** (the only injection axis):

- **system** — carries the `principles:session` tag; injected at every SessionStart
- **ondemand** — no marker; pull with `satelle doc get principles <name>` when referenced

There is no `scope:` field on principles. Ownership (`embedded_sha`) is orthogonal
to residency. See [[satelle-residency]].
