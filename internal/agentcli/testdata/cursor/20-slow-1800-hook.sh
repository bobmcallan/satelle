#!/usr/bin/env bash
in=$(cat)
printf '%s\t%s\n' "$(date -u +%T)" "$(printf '%s' "$in" | tr -d '\n' | head -c 300)" >> "/SCRATCH/20-slow-1800-hooks.log"
n=$(cat "/SCRATCH/20-slow-1800.count"); n=$((n+1)); echo $n > "/SCRATCH/20-slow-1800.count"
if [ $n -eq 1 ]; then sleep 75; echo '{"followup_message":"Reply with exactly SLOW-FOLLOWUP-SEEN and nothing else."}'; else echo '{}'; fi
