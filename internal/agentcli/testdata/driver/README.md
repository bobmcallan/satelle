# Driver-usage session record fixtures (sty_81caa41b)

These fixtures back `internal/agentcli/driver_usage_test.go` — the readers
that snapshot a driving (in-loop) session's own cumulative usage record,
distinct from `testdata/usage/` (a dispatched one-shot invocation's captured
stdout).

**Provenance (round 2).** Round 1's fixtures were constructed-representative
because that dispatch's `[coder]` grant could not read outside the repo tree.
Round 2 replaces them with real captures pulled from this machine's own
`~/.claude`, `~/.grok` and `~/.codex` session directories (prompt content
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
- `codex_rollout.jsonl` — four lines from a real
  `~/.codex/sessions/2026/09/27/rollout-2026-09-27T19-15-09-01a0e225-973d-7f11-90e8-68681c9d6d67.jsonl`
  capture (`session_meta`/`turn_context` trimmed to a few keys; the
  `token_usage_record` and `event_msg` token events verbatim). This capture
  fixed two more reader bugs: the model lives on `turn_context.payload.model`
  (not on the token_count event itself), and the cumulative usage is nested
  two levels inside the `event_msg` event, at
  `payload.info.total_token_usage` (not directly under `payload`). The
  capture holds only ONE `token_count` event (one completed turn), so
  `TestCodexDriverSnapshotDelta` treats the prefix (before that event) as the
  Available=false baseline and the full file as the first available snapshot
  — a real "no usage yet → first turn's cumulative" delta, with no invented
  second turn.

Each fixed assumption is called out at its reader call site in
`internal/agentcli/driver_usage.go` (`grokRepoDirName`, `grokDriverSnapshot`,
`codexDriverSnapshot`) and in the story's round-2 coder hand-off.
