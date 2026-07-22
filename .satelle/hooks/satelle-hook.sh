#!/bin/sh
#satelle-failvisible
# args: $1=gate|commitgate  $2=claude|grok
sub="$1"
harness="$2"
case "$harness" in
  grok) infra='{"decision":"deny","reason":"satelle unavailable in this hook shell env — INFRASTRUCTURE failure, NOT a policy denial. The satelle binary could not be resolved or did not produce a decision. Try: which satelle; satelle version; satelle init. Non-mutating bash stays allowed so you can diagnose."}' ;;
  *)    infra='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"satelle unavailable in this hook shell env — INFRASTRUCTURE failure, NOT a policy denial. The satelle binary could not be resolved or did not produce a decision. Try: which satelle; satelle version; satelle init. Non-mutating bash stays allowed so you can diagnose."}}' ;;
esac
# Prefer harness project pin so binary probe works even if invocation cwd drifted.
root=""
for d in "$CLAUDE_PROJECT_DIR" "$SATELLE_PROJECT_DIR"; do
  if [ -n "$d" ] && [ -d "$d" ]; then root="$d"; break; fi
done
b=""
for c in "$HOME/.local/bin/satelle" ${root:+"$root/.satelle/satelle"} ".satelle/satelle" satelle; do
  [ -z "$c" ] && continue
  if [ -x "$c" ]; then b="$c"; break; fi
  if command -v "$c" >/dev/null 2>&1; then b=$(command -v "$c"); break; fi
done
p=$(cat)
if [ "$sub" = "commitgate" ]; then
  docase(){ case "$p" in *git\ commit*|*git\ push*) printf '%s\n' "$infra"; exit 2;; *) exit 0;; esac; }
  if [ -z "$b" ]; then docase; fi
  o=$(printf '%s' "$p" | "$b" hook commitgate 2>/dev/null); code=$?
  if [ -n "$o" ]; then printf '%s\n' "$o"; fi
  if [ "$code" -eq 0 ]; then exit 0; fi
  if [ -z "$o" ]; then docase; fi
  exit 2
fi
if [ -z "$b" ]; then printf '%s\n' "$infra"; exit 2; fi
o=$(printf '%s' "$p" | "$b" hook gate 2>/dev/null); code=$?
if [ -n "$o" ]; then printf '%s\n' "$o"; fi
if [ "$code" -eq 0 ]; then exit 0; fi
if [ -z "$o" ]; then printf '%s\n' "$infra"; fi
exit 2
