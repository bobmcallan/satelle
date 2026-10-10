#!/usr/bin/env bash
ev="$1"; in=$(cat)
printf '%s\t%s\n' "$ev" "$(printf '%s' "$in" | tr -d '\n' | head -c 400)" >> "/SCRATCH/out/11a-hooks.log"
tn=$(printf '%s' "$in" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("tool_name",""))' 2>/dev/null)
case "$ev:$tn" in
  preToolUse:Read|preToolUse:Grep|preToolUse:Glob) echo '{"permission":"allow"}';;
  preToolUse:*) echo '{"permission":"deny","reason":"denied by probe"}';;
  *) echo '{}';;
esac
