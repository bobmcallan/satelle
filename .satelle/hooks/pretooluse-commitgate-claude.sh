#!/bin/sh
#satelle-failvisible
b=""; for c in "$HOME/.local/bin/satelle" ".satelle/satelle" satelle; do
  if [ -x "$c" ]; then b="$c"; break; fi
  if command -v "$c" >/dev/null 2>&1; then b=$(command -v "$c"); break; fi
done
p=$(cat)
infra='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"satelle unavailable in this hook shell env — INFRASTRUCTURE failure, NOT a policy denial. The satelle binary could not be resolved or did not produce a decision. Try: which satelle; satelle version; satelle init. Non-mutating bash stays allowed so you can diagnose."}}'
docase(){ case "$p" in *git\ commit*|*git\ push*) printf '%s\n' "$infra"; exit 2;; *) exit 0;; esac; }
if [ -z "$b" ]; then docase; fi
o=$(printf '%s' "$p" | "$b" hook commitgate 2>/dev/null); code=$?
if [ -n "$o" ]; then printf '%s\n' "$o"; fi
if [ "$code" -eq 0 ]; then exit 0; fi
if [ -z "$o" ]; then docase; fi
exit 2
