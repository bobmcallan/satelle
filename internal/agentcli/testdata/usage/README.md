# usage fixtures (sty_c8d45201)

Provenance is stated per file — a fixture is only "captured" if it was.

| file | provenance |
|---|---|
| `grok_json.json` | REAL, verbatim: `grok -p --output-format json` (grok 1.0.41, 2026-09-24). snake_case disjoint `usage`; `num_turns`; `modelUsage` keyed `grok-4.5-build`. Captured with the bare `-p` form, not the reviewer preset's tool/turn flags; `--output-format json` is the form the shipped grok presets now use (sty_5ebcd6f9), pinned by `TestGrokPresets_MeasureTurnsAndTokensFromRealCapture`. |
| `grok_streaming.jsonl` | REAL: `grok -p --output-format streaming-json`. The capture's 3 repeated `available_commands` lines were collapsed to one; the `text`, `usage` and `end` lines are verbatim. |
| `grok_acp.jsonl` | REAL, verbatim: `grok agent stdio` — `response_completed` / `turn_completed` notifications and the `session/prompt` result with `_meta.usage` (camelCase; `inputTokens` includes `cachedReadTokens`). |
| `claude_result.json` | Anthropic `--output-format json` envelope shape, as already asserted by the existing claude tests. |
| `grok_json_nousage.json` | SYNTHETIC: a grok `{"text": ...}` envelope with no `usage`, to exercise the unavailable path. |
| `claude_json_turns.json` | REAL, captured 2026-10-22 from Claude Code 2.1.306 with `claude -p "Reply with the single word ok." --output-format json --max-turns 3` run in a scratch directory; reports `num_turns` 1. |
| `claude_max_turns.json` | SYNTHETIC (sty_46e950ab): a `subtype:"error_max_turns"` result envelope — usage and `num_turns`, no `result` — NOT a capture. Replace with a real one: run `claude -p` with a prompt that needs several tool calls and `--max-turns 1`. |
| `grok_max_turns.txt` | SYNTHETIC (sty_46e950ab): grok `--output-format plain` output that ends at the turn cap with grok's `Max turns reached` line (the wording sty_e5fb3626 measured) — NOT a capture. Replace with a real one: `grok -p "<a prompt needing several tool calls>" --output-format plain --max-turns 1`. |
