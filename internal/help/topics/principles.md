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
  **adds** to the set. The directory monitor (`satelle index`) syncs them into
  the doc index.

List them with `satelle doc list --kind principles`; read one with
`satelle doc get principles <name>`.

## Residency: two tiers — session and on-demand

Residency has **two tiers**, set by a frontmatter tag:

- **session** (`principles:session`) — injected into the agent's context at the
  start of every session so the agent is driven to the result. Kept **minimal** —
  currently the operating principle `satelle-agent-goals` — to protect the context
  budget and keep the standing guidance sharp.
- **on-demand** (the default — no residency tag) — **resolvable when referenced**:
  the agent pulls it with `satelle doc get principles <name>` when a skill or
  workflow references it. Not auto-injected. (`satelle doc list` is the
  quality-management browse surface for authoring, not a step in the work loop.)

## How the session set reaches the agent (injection)

A Claude Code **SessionStart hook** runs `satelle hook context`. It injects the
body of every `principles:session` doc and appends the standing note that the rest
is on-demand (pulled via `satelle doc get` when referenced). It **fails open**: an
unconfigured repo or any read error injects nothing and never blocks the session.

Wire it once, in `.claude/settings.json`:

```json
{ "hooks": { "SessionStart": [ { "hooks": [
  { "type": "command", "command": "satelle index" },
  { "type": "command", "command": "satelle hook context" }
] } ] } }
```

Run `satelle hook context` by hand to see exactly what a session would receive.

## Authoring a principle

1. Add a markdown file under `.satelle/principles/<name>.md` (repo) — or, for a
   universal default, under `config/substrate/principles` in the binary.
2. Give it frontmatter: `name`, `kind: principle`, a `description`, and `tags`.
   Tag it `principles:session` only if it is short and belongs in every session;
   otherwise leave it untagged — on-demand is the default.
3. Link related principles with `[[other-principle-name]]`.
4. `satelle index`, then confirm with `satelle doc get principles <name>` (and,
   for a session principle, that it appears in `satelle hook context`).

## The order-zero principles

- **`satelle-constitution`** — satelle is a harness that runs your repo's process
  as configuration; the binary holds mechanism, the substrate holds behaviour.
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
