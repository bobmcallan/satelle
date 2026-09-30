# Driver-usage session record fixtures (sty_81caa41b)

These fixtures back `internal/agentcli/driver_usage_test.go` — the readers
that snapshot a driving (in-loop) session's own cumulative usage record,
distinct from `testdata/usage/` (a dispatched one-shot invocation's captured
stdout).

**Provenance (round 2).** Round 1's fixtures were constructed-representative
because that dispatch's `[coder]` grant could not read outside the repo tree.
Round 2 replaces them with real captures pulled from this machine's own
`~/.claude` and `~/.grok` session directories (prompt content
redacted; ids, models and usage numbers verbatim):

- `claude_session.jsonl` — three `type":"assistant"` lines from
  `~/.claude/projects/-home-bobmcallan-Development-satelle/916b6026-dd37-453e-b2e8-983ce7bb3a9e.jsonl`.
  The first two lines share `message.id` (`msg_011CejQ6vxKekDDPGtMQraze`) —
  streamed chunks of the same message with identical usage — and must
  dedupe to ONE entry, not sum twice; the third line is a distinct message id
  with its own usage. This is what `TestClaudeDriverSnapshotDelta` pins.
- `grok_usage_t1.json` / `grok_usage_t2.json` — derived from one real
  `~/.grok/sessions/%2Fhome%2Fbobmcallan%2FDevelopment%2Fsatelle/01a0c7e2-c0a2-76c2-b858-1122a914ebcf/usage.json`
  capture. `t2` is that file verbatim (two turns). `t1` is the same file with
  the second `turns[]` element removed and `session` recomputed as turn one's
  own totals — exactly reproducing the state the real file was in after turn
  one alone. This capture is also what fixed two reader bugs: the per-repo
  directory is the URL-encoded absolute cwd (`%2Fhome%2F…`), not a dash-slug
  like claude's, and the cumulative totals live under a nested `"session"`
  object (with a `"turns"` breakdown alongside it), not at the top level.

Each fixed assumption is called out at its reader call site in
`internal/agentcli/driver_usage.go` (`grokRepoDirName`, `grokDriverSnapshot`)
and in the story's round-2 coder hand-off.

## pi (sty_ca1ca935)

`pi_session.jsonl` is a trimmed capture of a real pi session record from this
machine (prompt, tool and error text redacted to `<redacted>`; ids, model and
usage numbers verbatim, taken from
`~/.pi/agent/sessions/--home-bobmcallan-Development-satelle--/2026-09-30T02-22-37-333Z_01a0f01e-fb15-7095-8798-9b7ab9d3d092.jsonl`).

What pi exposes, and what `piDriverSnapshot` reads:

- **Path.** `<agent dir>/sessions/--<cwd>--/<UTC timestamp>_<sessionId>.jsonl`;
  the agent dir is `~/.pi/agent` or `PI_CODING_AGENT_DIR`. `<cwd>` is the absolute
  cwd with the leading separator dropped and `/`, `\` and `:` turned into `-`
  (pi's `docs/session-format.md`). Pi also exports `PI_SESSION_ID` and
  `PI_SESSION_FILE` to the tools it runs.
- **Format.** JSONL: a `{"type":"session","id","cwd"}` header, `model_change`
  rows (`provider`, `modelId`), and `{"type":"message","message":{role,…}}` rows.
- **Usage.** An assistant message carries
  `usage:{input,output,cacheRead,cacheWrite,reasoning,totalTokens,cost:{…,total}}`.
  The split is disjoint (`input` is the fresh share;
  `totalTokens = input+output+cacheRead+cacheWrite`). One assistant row is one
  model call, written once when the message completes, so there are no streamed
  partials to dedupe.
- **Cost is pi's own figure or nothing.** This capture's model (`stealth/…` via
  openrouter) has no price in pi, so every row reports `cost.total: 0` beside
  real token counts. That is "unpriced", not "free": the reader reports the cost
  as unavailable rather than a measured $0. A priced model yields a summed
  dollar figure (`TestPiDriverSnapshotMeasuredCost`).
- **Errored requests** are recorded as assistant rows with all-zero usage (the
  first assistant row of this fixture) and are not counted as calls.
- **No in-flight undercount.** The assistant row is persisted when its message
  completes, before the tool call it requested runs, so a snapshot taken by a
  `satelle story set` call already includes the calling message. This is
  inferred from pi persisting on message completion, not observed with a
  timestamped probe; if a pi session shows the closing turn missing, set
  `MayUndercountInFlightTurn` in `piDriverSnapshot`.
