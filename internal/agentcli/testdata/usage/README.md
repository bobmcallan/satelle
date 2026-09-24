# usage fixtures (sty_c8d45201)

Provenance is stated per file — a fixture is only "captured" if it was.

| file | provenance |
|---|---|
| `grok_json.json` | REAL, verbatim: `grok -p --output-format json` (grok 1.0.41, 2026-09-24). snake_case disjoint `usage`; `modelUsage` keyed `grok-4.5-build`. |
| `grok_streaming.jsonl` | REAL: `grok -p --output-format streaming-json`. The capture's 3 repeated `available_commands` lines were collapsed to one; the `text`, `usage` and `end` lines are verbatim. |
| `grok_acp.jsonl` | REAL, verbatim: `grok agent stdio` — `response_completed` / `turn_completed` notifications and the `session/prompt` result with `_meta.usage` (camelCase; `inputTokens` includes `cachedReadTokens`). |
| `claude_result.json` | Anthropic `--output-format json` envelope shape, as already asserted by the existing claude tests. |
| `grok_json_nousage.json` | SYNTHETIC: a grok `{"text": ...}` envelope with no `usage`, to exercise the unavailable path. |
| `codex_exec.jsonl` | SYNTHETIC: `codex exec --json` `turn.completed` shape from Codex's documented event stream. codex is unauthenticated on this machine, so no live capture carries usage. |
