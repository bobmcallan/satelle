---
type: document
title: Dispatched claude env precedence — settings env block overrides shell/toml env
description: Empirical finding (claude 2.1.204) that .claude/settings.local.json's `env` block overrides shell/inline/toml env for vars it lists (incl. ANTHROPIC_* auth), contradicting the common "env vars are highest priority" claim; consequence for [retrospective]'s GLM toml env, and the fix.
tags:
- document
- agent-dispatch
- env
- claude
timestamp: '2026-07-08T09:31:47Z'
---

# Dispatched claude env precedence — settings `env` block overrides shell/toml env

A findings doc from the `sty_d4360e90` follow-on investigation into why a satelle
binding's `agents.toml` `env` (notably `[retrospective]`'s GLM provider) is silently
ignored. Verified empirically and reproducibly; it contradicts the plain reading of
Claude Code's own docs and at least one LLM's (Gemini/z.ai) summary of them.

## TL;DR

- In this environment (claude 2.1.204, repo `/home/bobmc/development/satelle`),
  `.claude/settings.local.json`'s **`env` block overrides the launching shell's env**
  for every var it lists — including `ANTHROPIC_*` auth AND non-auth vars.
- This is the **opposite** of the common claim ("environment variables are highest
  priority; a shell export overrides a settings `env` value; settings.json doesn't
  store API keys"). That claim holds for settings **fields** (`model`,
  `autoConnectIde`, …), **not** for the `env` **block**.
- Consequence: satelle sets a binding's `agents.toml` `env` as the dispatched
  subprocess's process env, but when `claude -p` runs in the repo dir it reads
  `settings.local.json`, whose `env` block **overwrites** that process env. So
  `[retrospective]`'s GLM `env` is clobbered — retrospective actually runs on
  **openrouter** (the repo's `settings.local.json` auth), not z.ai/GLM. It has likely
  never run on GLM.

## The claim this refutes

> "Environment variables take the highest precedence. A shell export at launch
> overrides an `env` value from settings.json. settings.json files don't store API
> keys (security best practice). `ANTHROPIC_API_KEY=… claude -p` forces that key,
> ignoring settings."

Tested directly. It does **not** hold here:

1. `settings.local.json` **does** store auth — its `env` block contains
   `ANTHROPIC_BASE_URL` (openrouter), `ANTHROPIC_AUTH_TOKEN` (set), `ANTHROPIC_API_KEY`
   (empty). (Verified: `python3 -c "import json;print(json.load(open('.claude/settings.local.json'))['env'])"`.)
2. A shell-exported bogus `ANTHROPIC_*` is **ignored** when `settings.local.json` is
   present (see evidence below).

The Anthropic docs' own rule — *"where the same behavior has both an environment
variable and a settings field, the environment variable takes precedence"* — uses
**settings-field** examples (`ANTHROPIC_MODEL` vs the `model` field). The user's auth
lives in the `env` **block**, not a field, so that rule doesn't apply; the `env` block
wins.

## Evidence (reproducible)

### 1. Auth: settings `env` block wins over a shell export (decisive)

`http://127.0.0.1:1` is an instant connection-refused. If the shell env won, claude
would fail fast against it. It succeeded — so it used `settings.local.json`, not the
shell env:

```bash
cd /home/bobmc/development/satelle
env ANTHROPIC_BASE_URL=http://127.0.0.1:1 ANTHROPIC_AUTH_TOKEN=sk-bogus-glm ANTHROPIC_API_KEY=bogus \
  timeout 40 claude -p --output-format json --model sonnet "Reply with exactly: OK"
# → {"is_error":false, "result":"OK"}   (success via settings.openrouter, NOT the bogus env)
```

Run three times (bogus.invalid endpoint, `127.0.0.1:1`, and a re-confirm) — always
succeeds via settings.

### 2. Non-auth: settings `env` block wins (not auth-specific)

Shell says `from-shell`; a settings `env` block says `from-settings`. Claude printed
`from-settings`:

```bash
env SATELLE_TEST_VAR=from-shell \
  timeout 60 claude -p --setting-sources user,project \
  --settings '{"env":{"SATELLE_TEST_VAR":"from-settings"}}' \
  --allowedTools "Bash(printenv:*)" --model sonnet \
  "Run: printenv SATELLE_TEST_VAR — reply with ONLY its stdout."
# → from-settings
```

### 3. Excluding the `local` source makes the shell env win again

`--setting-sources user,project` drops the `local` source
(`settings.local.json`). With it excluded, the process env becomes authoritative:

- bogus env + exclude local → fails (process env used, bogus endpoint hit).
- real env + exclude local → succeeds.

So the `local` source's `env` block is exactly what clobbers the toml `env`.

### 4. `--settings <json>` OVERRIDES `settings.local.json` (the clean lever)

Unlike the shell env (which local clobbers), a settings payload passed via `--settings`
sits at CLI-arg tier (tier 2) — **above** `settings.local.json` (local, tier 3) — so it
wins. This is the mechanism that makes a per-agent settings file authoritative:

```bash
# bogus GLM env via --settings, local INCLUDED (no --setting-sources):
timeout 50 claude -p --settings '{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:1","ANTHROPIC_AUTH_TOKEN":"sk-bogus-glm","ANTHROPIC_API_KEY":"bogus"}}' --output-format json --model sonnet "Reply: OK"
# → Terminated (124)  ← claude used the bogus --settings endpoint, NOT local's openrouter

# valid openrouter env via --settings (pulled from settings.local.json), local included:
timeout 50 claude -p --settings "$SETJSON" --output-format json --model sonnet "Reply: OK"
# → {"is_error":false,"result":"SETTINGS_AUTH_OK"}  ← end-to-end auth via --settings
```

So the precedence is: **`--settings` (CLI) > `settings.local.json` (local) > shell/process env.**
The shell env cannot override local; `--settings` can. This is why a per-agent settings
file injected via `--settings` is the clean fix (see Fix (d)).

## Mechanism

- The `env` **block** in a settings file sets env vars at startup, **overwriting**
  inherited process values. After startup the var holds the settings value, so
  "env var takes precedence" is technically true (claude uses the env var) — but the
  *value* came from settings, not the shell.
- Settings **fields** (a top-level `apiKey`/`model`) are the thing an env var
  overrides per the docs. Auth stored in the `env` block is not a field, so the
  override doesn't apply.
- Verified on claude 2.1.204. Other versions may differ; re-run the `127.0.0.1:1`
  repro to confirm.

## Implication for satelle

- A binding's `agents.toml` `env` (e.g. `[retrospective]`'s
  `ANTHROPIC_BASE_URL=z.ai`, `ANTHROPIC_AUTH_TOKEN=${GLM_API_KEY}`) is set by
  `agentcli` as the subprocess process env. `agentstep` dispatches `claude -p` with
  `cmd.Dir = repoRoot`, so claude reads the repo's `settings.local.json`, whose `env`
  block overwrites the GLM values. `[retrospective]` runs on openrouter, not GLM.
- The satelle-side fix already made (`internal/agentcli/agentcli.go`):
  `cmd.Env = composeEnv(os.Environ(), req.Env)` — inherit the shell env + toml `env`
  overlay, **zero filtering** (no denylist, no allowlist). This is correct for
  satelle's part. The clobber is **claude's**, not satelle's — so do NOT re-add env
  filtering in satelle (the "found and fixed twice" recurrence — `stripInheritedProviderEnv`
  then a `cleanSessionEnv` allowlist — was the filter itself being the bug).
- `agents.toml` `env` still works for vars `settings.local.json` does NOT list.

## Fix (pick one)

- **(a) Auth out of settings.** Remove `ANTHROPIC_*` from
  `.claude/settings.local.json`'s `env` block; put the operator's interactive-claude
  auth in the shell profile (`export ANTHROPIC_*`). Then nothing clobbers the toml
  `env`; `[retrospective]`'s GLM env wins; no satelle change; no `--setting-sources`.
- **(b) Surgical — isolate one binding.** Keep auth in `settings.local.json` and add
  `--setting-sources user,project` to `[retrospective]`'s harness only. Verified: the
  toml GLM `env` then wins. `[reviewer]`/`[worker]` keep using `settings.local.json`
  auth (openrouter).
- **(c) Purest via shell env.** Every binding gets `--setting-sources user,project`
  and declares its full provider in its toml `env` (secrets via
  `satelle.local.toml [vars]` + `${VAR}`). `settings.local.json` becomes only the
  user's interactive-claude auth. Biggest change.
- **(d) Per-agent subprocess file via `--settings` — RECOMMENDED.** Replace the central
  `.satelle/agents.toml` with one file per agent under `.satelle/agents/<name>.toml`
  (or `.json`). Each file is the COMPLETE subprocess definition: a `command` (the CLI
  invocation — `claude -p`, `codex exec`, …; claude-code-first but multi-CLI capable),
  plus the payload `model` / `permissions` / `env` (secrets as `${VAR}`) and a `role`
  (e.g. `reviewer` = `readonly-agent`). At dispatch, satelle resolves `${VAR}` from
  `satelle.local.toml [vars]`, materialises the payload into a claude settings JSON,
  and substitutes it into the `command` template via a `{settings}` placeholder —
  e.g. `command = "claude -p --settings {settings} --append-system-prompt {system}
  --output-format json"`. Because `--settings` is CLI tier, it **overrides**
  `settings.local.json` (verified §4) — so each claude agent's
  provider/auth/permissions are authoritative, the `[retrospective]` GLM clobber is
  fixed structurally, and `{tools}`/`{model}` CLI-arg substitution is no longer needed
  (they live in `{settings}`). Non-claude CLIs (codex, …) simply use a `command`
  template without `{settings}` and their native config mechanism — the
  `settings.local.json` clobber is claude-specific, so it doesn't arise for them.
  Workflows still reference agents by name (`agent=worker`). No satelle env filtering,
  no `--setting-sources`, no removing auth from `settings.local.json`. This is the
  cleanest no-magic end-state. It is a real satelle change (agent loader + command
  template materialisation + `${VAR}`→`{settings}` + migration + tests), so scope it
  as its own story/epic.

Recommendation: **(d)** as the target architecture (it alone fixes the clobber
structurally and is the most transparent). **(b)** is a one-line stopgap if you want
`[retrospective]` on GLM before (d) lands. Either way, the satelle-side no-magic fix
(`composeEnv(os.Environ(), req.Env)`) stays — the clobber was never satelle's to fix
by filtering.

## Related

- [[sty_d4360e90]] — seeded the `tsk_substrate-audit` task; unrelated substrate
  change, but this investigation was triggered while driving it.
- The `glm-env-breaks-sonnet-dispatch` memory (deleted 2026-07-08) carried a stale
  "nested claude -p hangs (lock detection)" claim that steered this work wrong; the
  real issue was always the settings `env` block clobber, not nested dispatch.
