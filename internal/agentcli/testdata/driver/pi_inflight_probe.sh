#!/usr/bin/env bash
# pi_inflight_probe.sh — does pi's session file already hold the assistant row that
# requested a tool call at the moment that tool executes? (sty_89768625)
#
# This decides agentcli.DriverSnapshot.MayUndercountInFlightTurn for pi:
#   row present at execution  -> the closing `satelle story set` turn is already in
#                                the file when satelle reads it: flag false.
#   row absent at execution   -> the turn is flushed only after the call returns:
#                                flag true (close/park rows are Pending).
#
# Method. Pi exports PI_SESSION_FILE to the tools it runs. The model is told to run
# the tool snippet below N times, one call per turn. At the k-th execution the tool
# counts the assistant rows in the file that carry a toolCall block:
#   count == k      the calling row was already persisted (flag false)
#   count == k - 1  the calling row was not yet persisted  (flag true)
# and stamps the wall clock, so the verdict can be checked against each row's own
# `timestamp` field afterwards.
#
# Usage:  PI_CMD="pi -p" ./pi_inflight_probe.sh <out-dir>
# Spends a few cheap model calls. Not run by `go test`. Redact prompt and tool text
# from the session file before checking it in beside pi_session.jsonl; keep ids,
# timestamps and usage verbatim.
set -euo pipefail

out=${1:?usage: pi_inflight_probe.sh <out-dir>}
mkdir -p "$out"
PI_CMD=${PI_CMD:-pi -p}
N=${N:-3}
probe_out="$out/probe.log"
: >"$probe_out"

# The snippet pi's bash tool runs. $PROBE_OUT and $PI_SESSION_FILE come from the
# environment; the call index is the number of lines already in the log plus one.
snippet='k=$(( $(wc -l <"$PROBE_OUT") + 1 )); t=$(date +%s.%N);
seen=$(grep -c "\"type\":\"toolCall\"" "$PI_SESSION_FILE" || true);
rows=$(grep -c "\"role\":\"assistant\"" "$PI_SESSION_FILE" || true);
echo "call=$k at=$t toolcall_rows_in_file=$seen assistant_rows_in_file=$rows" >>"$PROBE_OUT"; echo ok'

prompt="Run exactly $N separate bash tool calls, one per turn, never in parallel and never together. Each call runs this command verbatim, then you reply 'next' and make the next call. After call $N reply 'done'. Command: $snippet"

cwd=$(mktemp -d)
cd "$cwd"
PROBE_OUT="$probe_out" $PI_CMD "$prompt" >"$out/pi.stdout" 2>"$out/pi.stderr" || true

# The session file pi wrote for this cwd (newest wins).
dir="${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}/sessions/--$(printf %s "${cwd#/}" | tr '/\\:' '---')--"
session=$(ls -t "$dir"/*.jsonl | head -n1)
cp "$session" "$out/pi_inflight_probe.raw.jsonl"

{
  echo "# pi in-flight probe result"
  echo
  echo "session file: $(basename "$session")"
  echo
  echo "| call k | tool executed at (epoch s) | toolCall rows in file at execution | verdict |"
  echo "|--------|----------------------------|--------------------------------------|---------|"
  while read -r line; do
    k=${line#call=}; k=${k%% *}
    at=${line#*at=}; at=${at%% *}
    seen=${line#*toolcall_rows_in_file=}; seen=${seen%% *}
    if [ "$seen" -ge "$k" ]; then v="row present at execution"; else v="row ABSENT at execution (flush lag)"; fi
    echo "| $k | $at | $seen | $v |"
  done <"$probe_out"
  echo
  echo "Cross-check each call against the assistant rows' own \`timestamp\` in pi_inflight_probe.jsonl."
} >"$out/pi_inflight_probe.result.md"

cat "$out/pi_inflight_probe.result.md"
