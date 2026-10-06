#!/bin/sh
# claude-cloud-hook.sh — the edit and commit guard for a Claude cloud session
# that performs a satelle step (sty_82cffd60, interface = "cloud").
#
# A Claude cloud session clones this repository from GitHub. The one tracked
# carrier of hook wiring is .claude/settings.local.json, which Claude Code loads
# at startup; it holds ONLY a top-level "hooks" key and every entry runs this
# script. The session has no satelle binary, store or network route to satelle,
# so the guard is the branch, not a story:
#
#   gate         PreToolUse (edit tools)  — edits only on a claude/satelle-* branch
#   commitgate   PreToolUse (Bash)        — git commit / git push only on one
#
# The dispatch tells the session to create its branch (claude/satelle-<story>-
# <nonce>) before it edits and to push it once, at the end; satelle collects it
# into the story worktree and its local gates judge the diff. Any other branch —
# main, or the auto-named branch a session starts on — may be read, built and
# tested on, but not edited, committed or pushed from.
#
# Every mode exits 0 silently unless CLAUDE_CODE_REMOTE=true, so a local session
# is untouched. POSIX sh, no bashisms.

mode="$1"

[ "${CLAUDE_CODE_REMOTE:-}" = "true" ] || exit 0

root="${CLAUDE_PROJECT_DIR:-$(pwd)}"
branch=$(git -C "$root" symbolic-ref --short -q HEAD 2>/dev/null)

on_dispatch_branch() {
  case "$branch" in
    claude/satelle-*) return 0 ;;
  esac
  return 1
}

deny() {
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"%s"}}\n' "$1"
  exit 0
}

case "$mode" in
  gate)
    cat >/dev/null
    on_dispatch_branch && exit 0
    deny "satelle cloud session: edits are only allowed on the dispatch branch claude/satelle-<story>-<nonce> (create it with git checkout -b first); current branch: ${branch:-detached}"
    ;;
  commitgate)
    payload=$(cat)
    on_dispatch_branch && exit 0
    # git's global options precede the subcommand: bare flags (-p, --no-pager,
    # --git-dir=x) and flags that take the next word (-C <dir>, -c k=v), whose
    # value may be quoted (the payload is JSON, so a double quote arrives as \").
    arg='(\\?"[^"]*"|'"'"'[^'"'"']*'"'"'|[^[:space:]]+)'
    opts='(-C|-c|--git-dir|--work-tree|--namespace|--super-prefix|--config-env)'
    pat="(^|[^[:alnum:]_-])git([[:space:]]+${opts}[[:space:]]+${arg}|[[:space:]]+-[^[:space:]]*)*[[:space:]]+(commit|push)([^[:alnum:]_-]|\$)"
    if printf '%s' "$payload" | grep -Eq "$pat"; then
      deny "satelle cloud session: git commit and git push are only allowed on the dispatch branch claude/satelle-<story>-<nonce>; current branch: ${branch:-detached}"
    fi
    exit 0
    ;;
  *)
    echo "claude-cloud-hook.sh: unknown mode '$mode' (gate|commitgate)" >&2
    exit 0
    ;;
esac
