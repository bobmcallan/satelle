# pi in-flight probe result (sty_89768625)

Captured by `pi_inflight_probe.sh` against a real pi 0.99.2 session
(`stealth/space-bunny-alpha` via openrouter), 2026-10-01. Session file:
`2026-10-01T20-56-27-543Z_01a0f941-1697-74d9-b2be-1fc616734ea3.jsonl`; its redacted
copy is `pi_inflight_probe.jsonl`, the tool's own log is `pi_inflight_probe.log`.

Each of three bash tool calls counted, from inside the tool, the assistant rows
carrying a `toolCall` block in `$PI_SESSION_FILE` at the moment it executed.

| call k | tool executed at (epoch s) | toolCall rows in file at execution | verdict |
|--------|----------------------------|--------------------------------------|---------|
| 1 | 1790888192.059802693 | 1 | row present at execution |
| 2 | 1790888194.088105102 | 2 | row present at execution |
| 3 | 1790888196.168186862 | 3 | row present at execution |

Cross-check against the rows' own `timestamp`: the k-th assistant row is stamped
`20:56:32.048Z`, `20:56:34.083Z`, `20:56:36.165Z`; the tool ran at `32.0598`,
`34.0881`, `36.1682` (UTC seconds) — each after its row, and each before its
toolResult row (`32.064Z`, `34.091Z`, `36.170Z`).

**Result: pi writes the assistant row before the tool call it requested runs.**
`piDriverSnapshot` therefore does not set `MayUndercountInFlightTurn`.
