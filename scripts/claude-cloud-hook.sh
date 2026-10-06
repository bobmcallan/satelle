#!/bin/sh
# claude-cloud-hook.sh — satelle's gates for a Claude cloud session (sty_3b112554).
#
# A Claude cloud session clones this repository from GitHub. .claude/ and
# .satelle/ are gitignored, so the clone carries none of satelle's hook wiring.
# The one tracked carrier is .claude/settings.local.json, which Claude Code
# loads at startup; it holds ONLY a top-level "hooks" key (personal keys belong
# in .claude/settings.json or ~/.claude/settings.json) and every entry runs this
# script:
#
#   session      SessionStart — install satelle, run `satelle init`, emit
#                `satelle hook context`; log to /tmp/satelle-cloud-bootstrap.log
#   gate         PreToolUse (edit tools)  — .satelle/hooks/satelle-hook.sh gate claude
#   commitgate   PreToolUse (Bash)        — .satelle/hooks/satelle-hook.sh commitgate claude
#
# Every mode exits 0 silently unless CLAUDE_CODE_REMOTE=true, so a local session
# is untouched (init's own .claude/settings.json gates it). PreToolUse fails
# closed: if the bootstrap did not produce the hook, the call is denied.
#
# This tracked wiring covers SessionStart and PreToolUse only. A cloud session
# lacks init's Stop (stopcheck) and UserPromptSubmit hooks unless Claude Code
# hot-loads the .claude/settings.json that `satelle init` writes mid-session.
#
# OPERATOR STEPS — only an operator can do these; none is assumed:
#   1. (optional) Register a claude.ai environment setup script that pre-installs
#      satelle, so SessionStart finds it on PATH and skips the download.
#   2. Allowlist satelle.dev in the environment network policy (the default cloud
#      proxy answers 403, probe sty_3b112554/session_01KDe674AwhRHiL9RAQBuHRK).
#      Without it: the session has only a fresh local store, so no story created
#      elsewhere can be engaged there; it runs the embedded-default substrate,
#      not this repo's authored workflows and principles (.satelle/ is
#      gitignored); and driving an epic child there requires this step.
#
# POSIX sh, no bashisms.

mode="$1"

[ "${CLAUDE_CODE_REMOTE:-}" = "true" ] || exit 0

root="${CLAUDE_PROJECT_DIR:-$(pwd)}"
log=/tmp/satelle-cloud-bootstrap.log
PATH="$HOME/.local/bin:$PATH"
export PATH

case "$mode" in
  session)
    cd "$root" || exit 0
    {
      printf '== %s session start in %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$root"
      ok=1
      if command -v satelle >/dev/null 2>&1; then
        echo "satelle already on PATH: $(command -v satelle)"
      else
        echo "installing satelle from the GitHub release"
        curl -fsSL https://github.com/bobmcallan/satelle/releases/latest/download/install.sh | sh || ok=0
      fi
      if [ "$ok" = 1 ]; then
        satelle init --harness claude --no-workspace || ok=0
      fi
      echo "bootstrap ok=$ok"
      [ "$ok" = 1 ]
    } >>"$log" 2>&1
    if [ $? -eq 0 ]; then
      satelle hook context
    else
      echo "satelle cloud bootstrap FAILED — gates are unavailable and edits will be denied. See $log."
    fi
    exit 0
    ;;
  gate | commitgate)
    h="$root/.satelle/hooks/satelle-hook.sh"
    if [ -f "$h" ]; then
      exec sh "$h" "$mode" claude
    fi
    cat >/dev/null
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"satelle cloud bootstrap absent — see /tmp/satelle-cloud-bootstrap.log"}}'
    exit 0
    ;;
  *)
    echo "claude-cloud-hook.sh: unknown mode '$mode' (session|gate|commitgate)" >&2
    exit 0
    ;;
esac
