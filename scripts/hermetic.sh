#!/bin/sh
# hermetic.sh -- run a command in a clean room: an empty test-owned HOME and XDG
# dirs and a minimal PATH, so a test verdict does not depend on the operator's
# real machine (sty_ec30f859).
#
# Usage: hermetic.sh -- <cmd> [args...]     (test flags go after the cmd, e.g.
#        hermetic.sh -- go test -run X ./...; in make, through TESTFLAGS)
#
# The command runs under `env -i`. It gets:
#   - HOME and XDG_{CONFIG,DATA,CACHE,STATE}_HOME as fresh empty dirs inside one
#     mktemp dir, removed on exit; the command's exit status is kept;
#   - PATH=$(go env GOROOT)/bin:/usr/local/bin:/usr/bin:/bin -- the Go toolchain
#     and the system bin dirs, no version-manager shims;
#   - GOMODCACHE, GOCACHE and GOPATH read from `go env` BEFORE HOME is replaced,
#     so the wrapped run uses the real module and build caches (no network, CI's
#     setup-go cache still hits);
#   - the caller's real host, for the few checks that must still watch it, as
#     SATELLE_TEST_HOST_HOME, SATELLE_TEST_HOST_XDG_CONFIG_HOME (the caller's
#     XDG_CONFIG_HOME, else $HOME/.config) and SATELLE_TEST_HOST_SATELLE_HOME
#     (only when the caller has SATELLE_HOME set);
#   - of the caller's other variables, ONLY the allowlist below plus every
#     SATELLE_TEST_PROBE_* variable. Anything else is dropped.
# The wrapper does not change which tests exist; operator-config checks stay
# behind the `operatorconfig` build tag (make operator-check).

ALLOW="TERM TMPDIR SATELLE_BIN GOFLAGS GOPROXY GOTOOLCHAIN GONOSUMDB GOPRIVATE GONOPROXY GOSUMDB"

[ "$1" = "--" ] && shift
if [ "$#" -eq 0 ]; then
	echo "usage: hermetic.sh -- <cmd> [args...]" >&2
	exit 2
fi

if ! goroot=$(go env GOROOT) || [ -z "$goroot" ]; then
	echo "hermetic: go not found on PATH; failing closed" >&2
	exit 2
fi
gomodcache=$(go env GOMODCACHE) || exit 2
gocache=$(go env GOCACHE) || exit 2
gopath=$(go env GOPATH) || exit 2

work=$(mktemp -d "${TMPDIR:-/tmp}/hermetic.XXXXXX") || exit 2
trap 'rm -rf "$work"' HUP INT TERM
mkdir -p "$work/home" "$work/xdg/config" "$work/xdg/data" "$work/xdg/cache" "$work/xdg/state" || {
	rm -rf "$work"
	exit 2
}

# Assignments are prepended to the command, so "$@" ends up as
# NAME=value ... <cmd> [args...] for `env -i`.
names=$(env | sed -n 's/^\(SATELLE_TEST_PROBE_[A-Za-z0-9_]*\)=.*/\1/p')
for name in $ALLOW $names; do
	eval "isset=\${$name+x}"
	[ -n "$isset" ] || continue
	eval "val=\${$name}"
	set -- "$name=$val" "$@"
done

host_xdg=${XDG_CONFIG_HOME:-${HOME:-}/.config}
if [ -n "${SATELLE_HOME+x}" ]; then
	set -- "SATELLE_TEST_HOST_SATELLE_HOME=$SATELLE_HOME" "$@"
fi
set -- \
	"SATELLE_TEST_HOST_HOME=${HOME:-}" \
	"SATELLE_TEST_HOST_XDG_CONFIG_HOME=$host_xdg" \
	"HOME=$work/home" \
	"XDG_CONFIG_HOME=$work/xdg/config" \
	"XDG_DATA_HOME=$work/xdg/data" \
	"XDG_CACHE_HOME=$work/xdg/cache" \
	"XDG_STATE_HOME=$work/xdg/state" \
	"PATH=$goroot/bin:/usr/local/bin:/usr/bin:/bin" \
	"GOMODCACHE=$gomodcache" \
	"GOCACHE=$gocache" \
	"GOPATH=$gopath" \
	"$@"

env -i "$@"
status=$?
rm -rf "$work"
exit "$status"
