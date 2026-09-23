# Compact CLI rendering

Several read commands an agent pulls through `Bash(satelle:*)` — `ledger
list`, `story list`, `story docs`, `story messages`, `story diff --patch` —
can render COMPACT instead of indented JSON: a CSV-backed table for a list of
records, and noise stripped from a unified diff patch. Every fold is
lossless-or-nothing: it applies only when decoding it back reproduces the
exact original AND the result is smaller (`internal/compact.Fold` /
`FoldTable`); otherwise the command prints the same indented JSON it always
has. Nothing about the underlying verb or its data changes — only how the CLI
prints the response.

## Which commands, and when

`[output]` in `satelle.toml`:

```toml
[output]
compact_for_agents = true
compact_commands = ["ledger-list", "story-list", "story-doc-list", "story-messages", "story-diff"]
long_cell_bytes = 200   # default 200 — per-cell offload threshold
repeat_min = 3          # default 3 — collapse runs of this many identical lines
noise_patterns = ["go.sum"]
```

- `compact_commands` opts a verb in by name. A verb absent from the list
  always prints plain indented JSON, `--compact` or not.
- `compact_for_agents` makes compact the default, for a listed verb, when the
  caller looks like a dispatched or in-loop agent — `SATELLE_SCRATCH` set (every
  dispatched agent gets one) or `CLAUDECODE=1` (an in-loop Claude Code
  session). A human or script at a terminal still gets plain JSON unless it
  passes `--compact` itself.
- `--compact` (a persistent flag on every command) forces compact mode for a
  listed verb regardless of `compact_for_agents` or the caller.
- `--json` (also persistent) always wins: it forces plain indented JSON even
  over `--compact`.
- `noise_patterns` are globs identifying generated/lockfile files (basename
  match unless the glob contains `/`) whose diff hunks `story diff --patch`
  offloads whole. The binary ships no pattern of its own — an empty list
  never treats any file as noise.

The zero value of `[output]` (an absent table) keeps every command exactly as
it always printed: plain indented JSON, unconditionally.

## The table form

A JSON array of objects where each column holds one JSON kind throughout (a
`null`, or a key some rows omit entirely — the ordinary shape of Go's
`omitempty`, e.g. the ledger's `story_id`/`actor`/`body`/`payload` — never
disqualifies or fixes a column; only a real type mismatch does) renders as:

```
[N]{col1:kind1,col2:kind2,...}
<CSV row>
<CSV row>
...
```

`N` is the row count; each header entry names a column and its JSON kind
(`str`, `num`, `bool`, `null`, or `json` for a nested object/array). A present
cell is the record's exact compact JSON for that field — decoding parses it
back rather than re-deriving it; an absent cell (the key missing on that row)
is the empty CSV field, and decoding leaves the key out rather than inventing
one. A response that is not an array of objects, or has a real type mismatch
in some column, always falls back to plain JSON; there is nothing to fold.

A string cell over `long_cell_bytes` is replaced with a quoted retrieve marker
(see below) instead of printing the long value inline.

## The diff patch form

`story diff --patch` in compact mode:

- always drops every `index abc..def` line — the one piece of unified-diff
  framing this fold is lossy-without-offload about (there is nothing
  reviewable to retrieve back for a blob-id pair on its own).
- offloads a **whitespace-only hunk** (every removed/added line pairs up
  equal once whitespace is stripped) behind a marker.
- offloads a **whole file section** whose path matches a `noise_patterns`
  glob behind a marker.
- collapses runs of `repeat_min`+ identical lines to the line once plus
  `... (repeated N times)`.

An offload never applies unless the marker line it leaves behind is actually
shorter than what it replaces — a tiny hunk stays inline.

## Retrieving offloaded content

An offloaded cell or hunk leaves an EXTENDED retrieve marker:
`<<ccr:HASH,KIND,SIZE>>` (`retrieve.MarkerKind`) — `retrieve.MarkerRE` detects
both this and the plain `<<ccr:HASH>>` form, so anything already scanning for
markers keeps working unchanged. Resolve it exactly the same way as any other
CCR marker:

```
satelle retrieve <hash>
```

See `satelle help retrieve` for the marker grammar, retention, and which agent
seats can reach the verb.

## Mechanism vs. configuration

`internal/compact` is pure mechanism: the table/diff/line folds and the
round-trip-and-smaller guard, with no config reads and no filename compiled
in. What counts as noise, which verbs compact, and whether an agent gets it by
default are `[output]` in `satelle.toml` — this repo's own configuration, not
a binary opinion (`satelle-constitution`: configuration over code).
