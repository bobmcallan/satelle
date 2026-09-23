# satelle retrieve — CCR originals of condensed content

satelle's retrieval store implements compress-cache-retrieve (CCR): a
compressor that drops content stores the exact original bytes under a content
hash and leaves a marker behind. `satelle retrieve <hash>` later returns those
bytes exactly — the ledger and gate evidence stay complete even though the
model only read a condensed view.

## The marker grammar

A compressor leaves ONE of two forms in its condensed output, both detected by
the single exported regex `retrieve.MarkerRE` (`internal/retrieve/marker.go`):

- an inline marker: `<<ccr:HASH>>`
- a summary line: `[N lines compressed to M. Retrieve more: satelle retrieve HASH]`

`HASH` is always 24 lowercase hex characters — the first 24 hex characters of
the content's sha256 digest (`retrieve.Hash`). `retrieve.FindHashes(s)` returns
every hash referenced in a string, deduplicated, in order of first appearance.
Every later compressor reuses `MarkerRE` / `FindHashes` rather than growing a
second detector, so the grammar and its detection never drift apart.

## The verb

```
satelle retrieve <hash>
```

Prints the exact original bytes to stdout — no trailing newline, no framing —
or exits non-zero with `retrieve: no stored original for hash <hash>` when the
hash is unknown or malformed (not 24 lowercase hex characters; the two cases
are indistinguishable to the caller on purpose). The verb never appends to the
ledger and never touches an engagement seat: it is pure read.

## Retention

`retrieve_keep_days` in `satelle.toml` (default `0`, keep forever) prunes a
stored original once EVERY story referencing it is terminal (done/cancelled)
and its terminal update is older than the configured days. A non-terminal
story's reference always keeps the blob alive, and a blob two stories share
survives as long as either reference does — pruning one story's retention
window never deletes bytes a live sibling still points at.

## Reachability — which seats can run it

The verb is read-only by design, but whether a given AGENT SEAT can actually
issue `satelle retrieve <hash>` depends on how that seat's transport enforces
its tool grant:

- **A configured command-transport reviewer** (e.g. this repo's `[reviewer]`
  binding: `command = "claude -p … --disallowedTools Write,Edit,NotebookEdit
  --allowedTools {tools} …"` with `tools = "Read,Grep,Glob,Bash(satelle:*)"`)
  CAN reach it: the argv allow-list names `Bash(satelle:*)`, and Bash is absent
  from the deny list, so the deny-wins-over-allow ceiling does not block it.
- **The binary's embedded default reviewer template**
  (`agentcli.DefaultClaudeCommand`) CANNOT reach it: that template's
  `--disallowedTools` includes bare `Bash`, and a deny always beats an allow —
  so a repo that keeps the shipped default reviewer command cannot run ANY
  satelle verb over Bash, `retrieve` included. Fix: author a `[reviewer]`
  command without `Bash` in the deny list and with `Bash(satelle:*)` in
  `tools`, as this repo does.
- **A live consulting session** (stream/ACP transport, e.g. `[reviewer-consult]`)
  CANNOT reach it today: those transports classify any `Bash` request as an
  `execute`-kind mutator and deny it under a read-only grant, regardless of a
  `satelle:*` scope. Letting a read-only live session run a whitelisted
  read-only satelle verb over Bash is a follow-up, not shipped here.

`agentcli.GrantAllowsMutators(tools)` is the shared predicate for "does this
grant admit a mutator" — a `Bash(satelle:*)`-only grant answers `false`, which
is what keeps a command-transport reviewer's seat read-only while still
letting it reach `retrieve`.
