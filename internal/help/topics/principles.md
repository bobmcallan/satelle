# Principles — the authored guardrails the agent reads

Principles are authored markdown that informs the agent. They are **guides, not
gates**: a principle never blocks a transition (only a reviewer does that — see
`satelle help reviewer-checks`). What principles do is shape *how* work is done —
they are the order-zero context the executor carries.

## Two layers: embedded vs repo

Like every authored kind, principles resolve in two layers:

- **Embedded (canonical, in the binary)** — the operating-essential principles
  every satelle repo inherits, shipped under `config/substrate/principles`. These
  are the single source of those bytes; a repo never edits them. The embedded set
  is deliberately tiny — `satelle-agent-goals` (the operating discipline) and
  `satelle-agent-model` (the execution model). Everything else (constitution,
  yagni, done-is-last, …) is authoring/development substrate that lives in a repo
  under `.satelle/principles`.
- **Repo (layered, under `.satelle/principles/`)** — a repo's own principles. A
  repo file with the same name **overrides** the embedded default; a new name
  **adds** to the set. The directory monitor (`satelle reindex`) syncs them into
  the doc index.

List them with `satelle doc list --kind principles`; read one with
`satelle doc get principles <name>`.

## Residency: two tiers — system and ondemand

Residency is the **single injection axis** for principles (see the ondemand
reference principle `satelle-residency`). One classifier — the frontmatter tag
`principles:session`. There is no `scope:` axis on principles.

| Tier | Marker | Behaviour |
| --- | --- | --- |
| **system** | carries `principles:session` | injected at every SessionStart |
| **ondemand** | no marker (default) | pull with `satelle doc get principles <name>` when referenced |

Keep the system set **minimal** under the single SessionStart ceiling
(`alwaysContextCeiling` = 16384 bytes — constitution + resident bodies + pointer).
The operating triad that ships session-tagged is `satelle-agent-goals`,
`satelle-edits-require-a-story`, and `satelle-recognise-blockage`. Ownership
(`embedded_sha` from init) is **orthogonal** to residency: a repo may author its
own system principle without a stamp; an embedded default may be ondemand
(e.g. `satelle-agent-model`, `satelle-residency`).

## How the system set reaches the agent (injection)

A Claude Code **SessionStart hook** runs `satelle hook context`. It injects the
body of every `principles:session` (system-resident) doc and appends the standing
note that the rest is ondemand (pulled via `satelle doc get` when referenced). It
**fails open**: an unconfigured repo or any read error injects nothing and never
blocks the session.

Wire it once, in `.claude/settings.json`:

```json
{ "hooks": { "SessionStart": [ { "hooks": [
  { "type": "command", "command": "satelle reindex" },
  { "type": "command", "command": "satelle hook context" }
] } ] } }
```

Run `satelle hook context` by hand to see exactly what a session would receive.

## Authoring a principle

1. Add a markdown file under `.satelle/principles/<name>.md` (repo) — or, for a
   universal default, under `config/substrate/principles` in the binary.
2. Give it frontmatter: `name`, `type: principle`, a `description`, and `tags`.
   Tag it `principles:session` only if it is short and belongs in every session
   (system residency); otherwise leave it untagged — ondemand is the default.
   Do **not** put a `scope:` field on principles (workflows still use `scope:`).
3. Link related principles with `[[other-principle-name]]`.
4. `satelle reindex`, then confirm with `satelle doc get principles <name>` (and,
   for a system principle, that it appears in `satelle hook context`).

## The order-zero context

- **The project constitution** — `.satelle/constitution.md` (repo root, NOT a
  principle): the local/repo definition injected first every session. satelle is a
  harness that runs your repo's process as configuration; the binary holds
  mechanism, the substrate holds behaviour. Authored per repo; `satelle init`
  scaffolds a template.
- **`satelle-repo-agnostic`** — keep the product separable from the one repo that
  dogfoods it; configuration over code.
- **`satelle-agent-goals`** — drive a story to its terminal state through every
  gate; status is the sole proof of done; never route around a gate.
- **`satelle-done-is-last`** — `done` is always the terminal state; gates precede
  it.
- **`satelle-agent-model`** — every step is run by a defined agent role
  (executor does the work; reviewer is limited to read-only reviewing); each gate
  is an isolated fresh-context call; satelle gates status; process is
  configuration.

See also: `satelle help reviewer-checks`, `satelle help create-story`.
