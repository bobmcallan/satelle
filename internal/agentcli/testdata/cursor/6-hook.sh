#!/usr/bin/env bash
cat >/dev/null
env | grep -E '^(CURSOR_AGENT|CURSOR_INVOKED_AS|AI_AGENT|CURSOR_TRACE_ID|CURSOR_PROJECT_DIR|CURSOR_VERSION|CLAUDECODE|CLAUDE_PROJECT_DIR)=' | sort > "/SCRATCH/out/6-hook-env.txt"
env | grep -iE 'cursor' | sed 's/=.*//' | sort > "/SCRATCH/out/6-hook-env-names.txt"
echo '{}'
