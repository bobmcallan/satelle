---
name: satelle-design-review
scope: project
type: skill
tags: [solo-dev, reviewer, gate, design]
description: Reviewer gate for UI/design-system alignment on surface-scoped slices. Isolated read-only judge; n/a-fast-accepts when the slice does not touch UI surfaces.
---

# Design-system review (surface:ui scoped gate)

## Primary objective

Decide whether a **UI-touching** story's presented slice follows **this repository's**
design system as encoded in `internal/web/static/app.css` (and related templates
under `internal/web/`). Answer only: may the story proceed past this gate?

You receive `{story, from, to}` on stdin. **Read-only** (Read/Grep/Glob). Do not
edit, format, commit, or invent a redesign. Fair gate: judge the slice against
the rules below; do not add perfectionist requirements the story never stated.

**Authority:** `internal/web/static/app.css` in THIS repo is the source of truth.
Do **not** import Claude Design kit layout models, accent-dot branding, or kit
v0.1.x snapshots as requirements.

## When this gate applies

The workflow enqueues this skill only for stories tagged `surface:ui` (DOT
`applies_to="surface:ui"`). If you are invoked without UI-relevant changes, accept
with a note that the slice is design-exempt rather than inventing UI work.

## How to judge

Inspect presented UI changes (CSS, HTML templates, static assets, web Go that
emits markup). Check:

1. **Tokens over ad-hoc literals.** New colors, radii, control heights, and
   content width should use CSS variables from `:root` / `[data-theme="dark"]`
   (`--ink`, `--muted`, `--accent`, `--chip`, `--panel`, `--content-w`,
   `--content-max`, `--control-height`, `--fail`, …) rather than inventing a
   parallel palette. Hard-coded hex in a tiny local exception is only acceptable
   when no token fits and the story AC requires it — name it in notes if
   accepting with caveat; reject a new free-form palette.

2. **Layout width model (the adopting repo).** Content width is
   `width: var(--content-w)` with `max-width: var(--content-max)` where
   `--content-w: 80%` and `--content-max: 1600px` (see `.wrap` and the
   `<story-id>` comment in app.css). Do **not** require or invent a
   “max-width only / proportional removed” model from external kits. Reject a
   slice that reintroduces conflicting width schemes without reconciling to
   these tokens.

3. **Badge / chip language.** Status chips and badges should follow existing
   chip/badge patterns (`.chip`, color-mix tints on accent/fail where the CSS
   already does so). Reject one-off badge systems that ignore chip tokens.

4. **Both themes.** UI that sets only light or only dark colors without using
   theme tokens must remain legible under both `default` and `[data-theme="dark"]`.
   Prefer token-backed colors.

5. **Typography.** Space Grotesk (self-hosted) + system fallbacks for UI; mono
   via `var(--mono)`. Do not pull remote font CDNs.

6. **Iconography.** No emoji, icon fonts, or raster icon packs as product
   chrome. The brand mark is the **◐** (half-circle) / SMIL mark language already
   in the app.

7. **Wordmark period is allowed; retired footer accent is not.** The adopting repo's
   wordmark uses a period after the name styled as `span.dot` / `header.app h1
   .dot` (e.g. `satelle<span class="dot">.</span>`). That is **current** branding
   — do not reject it. What is retired (and must not return) is an **extra
   decorative trailing accent** on the footer product name separate from that
   wordmark period (the Claude Design kit's app footer reintroduced such a
   decoration; the adopting repo's `.site-footer` stays free of it).

### Accept / reject

- **Accept** when the presented UI slice follows the rules above (or the story
  has no UI surface in the tree).
- **Reject** when a rule is clearly violated — name the file/pattern and the
  rule (e.g. “retired accent dot in page.go footer”).

## Verdict

Reply with exactly one JSON object, nothing else of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` names the gap on reject (may be
empty on accept).
