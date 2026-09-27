# hook fixtures (sty_719c4a7b)

Provenance is stated per file, following the convention in
`../testdata/usage/README.md` (sty_c8d45201) — a fixture is only "captured"
if it was.

| file | provenance |
|---|---|
| `claude_bash.json` | SYNTHETIC, but schema-verbatim: fields (`session_id`, `transcript_path`, `cwd`, `hook_event_name`, `permission_mode`, `tool_name`, `tool_input`, `tool_use_id`) match Anthropic's published Claude Code hooks JSON schema. |
| `claude_edit.json` | SYNTHETIC, schema-verbatim — same basis as `claude_bash.json`. |
| `codex_shell.json` | SYNTHETIC: modeled on Codex CLI's documented hook schema (snake_case, `turn_id` present — the field Claude Code's envelope never carries). codex is unauthenticated on this machine (see `../usage/README.md`), so no live hook capture exists. |
| `codex_apply_patch.json` | SYNTHETIC — same basis as `codex_shell.json`. |
| `grok_bash.json` | SYNTHETIC: camelCase keys (`sessionId`, `hookEventName`, `toolName`, `toolInput`) match the casing convention confirmed by this repo's REAL grok ACP captures (`../usage/grok_acp.jsonl`, `grok_ask_user_question.request.json`, both `sessionId`/`toolCallId`) and by the real hook captures below. Kept as an extra PreToolUse (bash-shaped tool call) variant alongside the real `grok_pre_tool_use.json` capture. |
| `grok_edit.json` | SYNTHETIC — same basis as `grok_bash.json`; an extra PreToolUse (edit-shaped tool call) variant. |
| `grok_session_start.json` | REAL — captured 2026-09-27 from a live headless `grok -p` session in this repo (session `01a0e2c7-5729-7a80-bf94-da3779d16c73`), by a temporary hook that wrote each event's raw stdin to a file before the hook was removed. Attached to sty_719c4a7b as the `grok-hook-captures` document. |
| `grok_prompt_submit.json` | REAL — same capture session as `grok_session_start.json`. This is the AC3 regression payload: it carries `permission_mode`/`hook_event_name` (Claude-compatible snake_case aliases Grok's shim echoes) alongside its own `sessionId`/`hookEventName`/`workspaceRoot`. `HarnessFromHookEvent` must classify it grok, not claude, even with those aliases present. |
| `grok_pre_tool_use.json` | REAL — same capture session. Carries both `toolInput` and `tool_input` (and both `sessionId`/`session_id`), so grok's own fields (`hookEventName`, `workspaceRoot`) are what decide it, not the ambiguous tool_input pair alone. |
| `grok_stop.json` | REAL — same capture session. |

**Provenance note:** every real grok payload above carries Claude-compatible
snake_case aliases (`session_id`, `hook_event_name`, `permission_mode`,
`transcript_path`, `tool_input`) *alongside* grok's own camelCase keys
(`sessionId`, `hookEventName`, `workspaceRoot`, `permissionMode`,
`transcriptPath`, `toolInput`). `HarnessFromHookEvent` treats any of grok's
own camelCase-only field names as decisive regardless of which snake_case
aliases ride alongside them — a snake_case alias is never, by itself, claude
evidence. This closes the gap the previous round of this fixture set left
open (grok's hook envelope had not yet been captured live); the claude and
codex fixtures above remain synthetic-but-schema-verbatim, since no attended
session was available in this rework relay to capture those live (see the
account below).

**Why the claude fixtures are synthetic too, and what this coding session
tried before accepting that:** this session runs inside a real Claude Code
hook chain (`.claude/settings.json` in this repo fires PreToolUse, Stop and
UserPromptSubmit against this very conversation), so capturing a genuine
claude payload looked like it should need no live-session dogfood at all —
just tee the real stdin to a file. Three concrete attempts, in this session,
all blocked on infrastructure this coding session cannot grant itself:
1. Editing `.claude/settings.json` to pipe each hook's stdin through `tee`
   into a capture file before the real command ran: the edit was refused —
   "Claude requested permissions to write to
   `/home/bobmcallan/Development/satelle/.claude/settings.json`, but you
   haven't granted it yet." No approver is present in this rework relay to
   grant it.
2. Reading `~/.grok` for any log of a real past grok hook firing on this
   machine: blocked — "ls in '/home/bobmcallan/.grok' was blocked. For
   security, Claude Code may only list files in the allowed working
   directories for this session:
   '/home/bobmcallan/Development/satelle'."
3. Reading `~/.claude` for the same reason (transcripts, hook debug logs):
   blocked with the identical sandbox message.
4. `git log --all` over every `*hook*.json` path in this repo's history
   turned up no prior real capture to reuse.

So the gap is symmetric across claude and grok: both are blocked by this
sandbox's restriction to the repo working tree plus the lack of an approver
to grant a settings edit, not by any missing code. Spawning a nested `claude`
or `grok` process from here to force a real firing was considered and
rejected — it would spend real tokens/API cost and run the live gate scripts
against this repo unattended, which is a materially different risk than
editing a fixture and needs a human's go-ahead, not a coding session's. A
real capture needs either an approver present to grant the settings.json
edit for one turn, or the AC7 dogfood run itself (which runs attended, on a
real grok/claude session, outside this sandbox).
