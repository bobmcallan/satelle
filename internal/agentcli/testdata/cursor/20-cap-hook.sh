#!/usr/bin/env bash
in=$(cat)
printf '%s\t%s\n' "$(date -u +%T)" "$(printf '%s' "$in" | tr -d '\n' | head -c 300)" >> "/SCRATCH/20-cap-hooks.log"
n=$(cat "/SCRATCH/20-cap.count"); n=$((n+1)); echo $n > "/SCRATCH/20-cap.count"
if [ $n -le 12 ]; then printf '{"followup_message":"Reply with exactly LOOP-%s and nothing else."}\n' "$n"; else echo '{}'; fi
