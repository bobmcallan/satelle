#!/usr/bin/env bash
# deploy-check.sh — the functional check behind the satelle-deploy-review gate.
#
# It deploys the service LOCALLY and validates it with a health check on both
# surfaces — the web UI and the CLI — then tears it down. Local-first: this is a
# throwaway deploy (a fresh binary served on a temp port, bound like the real
# service), so it proves "it deploys and the UI works" with no effect on any
# installed service. Exit 0 only if every check passes; non-zero (with the reason
# on stderr) otherwise, which the gate turns into a reject.
#
# Repoint the gate at `satelle service install` for a persistent deploy if you
# want the real systemd service restarted — the check is authored substrate.
set -euo pipefail

PORT="${SATELLE_DEPLOY_PORT:-8902}"
ADDR="${SATELLE_DEPLOY_ADDR:-0.0.0.0}"
BASE="http://127.0.0.1:${PORT}"
TMP="$(mktemp -d)"
BIN="${TMP}/satelle"
SRV_PID=""

cleanup() {
  [ -n "${SRV_PID}" ] && kill "${SRV_PID}" 2>/dev/null || true
  rm -rf "${TMP}"
}
trap cleanup EXIT

fail() { echo "deploy-check: FAIL — $*" >&2; exit 1; }

# 1. Build the binary (deploy artifact).
echo "deploy-check: building…"
go build -o "${BIN}" ./cmd/satelle || fail "build failed"

# 2. Deploy: serve the built binary locally, bound like the real service.
echo "deploy-check: serving ${BASE} (addr ${ADDR})…"
"${BIN}" serve --addr "${ADDR}" --port "${PORT}" >"${TMP}/serve.log" 2>&1 &
SRV_PID=$!

# 3. Health check — web UI: /healthz comes up, then the project page renders.
ok=""
for _ in $(seq 1 30); do
  if curl -fsS "${BASE}/healthz" 2>/dev/null | grep -q "ok"; then ok=1; break; fi
  kill -0 "${SRV_PID}" 2>/dev/null || fail "server exited early:\n$(cat "${TMP}/serve.log")"
  sleep 0.5
done
[ -n "${ok}" ] || fail "web /healthz never returned ok:\n$(cat "${TMP}/serve.log")"

page="$(curl -fsS "${BASE}/" || true)"
for marker in 'data-panel="stories"' 'data-panel="workflow"' 'class="tabs"'; do
  case "${page}" in
    *"${marker}"*) : ;;
    *) fail "web UI did not render expected element: ${marker}" ;;
  esac
done
echo "deploy-check: web UI healthy (healthz ok, project page renders)."

# 4. Health check — CLI: status reports the store cleanly.
"${BIN}" status >"${TMP}/status.log" 2>&1 || fail "CLI 'satelle status' failed:\n$(cat "${TMP}/status.log")"
echo "deploy-check: CLI healthy (satelle status ok)."

echo "deploy-check: PASS — deployed and healthy (web + CLI)."
