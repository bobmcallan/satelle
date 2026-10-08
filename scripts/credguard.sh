#!/bin/sh
# credguard.sh -- run a command and fail if it changed the operator's host
# credentials file in a way a test could have (sty_18403814, sty_5ba68e2c), or
# the top level of the real ~/.satelle or the installed satelle/satelled
# binaries (sty_1b739a74).
#
# Usage: credguard.sh -- <cmd> [args...]
#
# Host surface (internal/hostguard, via scripts/credfp host-snapshot/host-diff):
# a new, removed or content-changed top-level entry under the real ~/.satelle,
# or a created, removed or content-changed ~/.local/bin/satelle or satelled,
# exits 1 naming it. Names a live satelled rewrites on its own, and the contents
# of directories, are outside it; mtimes never count. The home is
# SATELLE_TEST_HOST_HOME when set, otherwise the real HOME -- never SATELLE_HOME
# or XDG_*, which a suite points at a sandbox. If the host snapshot cannot be
# taken the guard fails closed (exit 2).
#
# The credentials file is ${XDG_CONFIG_HOME:-$HOME/.config}/satelle/credentials.toml, the
# per-user store `satelle login` writes. A test suite must leave it untouched.
# The guard compares the file's IDENTITY before and after the command, not its
# bytes: a satelle process outside the suite may refresh a hosted token during
# the run, which rewrites tokens and timestamps but keeps every credential's
# server_url, display_name, email and principal_id. That passes. Adding or
# removing a credential, changing an identity, creating the file from absent,
# deleting it, or leaving it unparseable exits 1 with a message.
#
# The fingerprint and diff are hosted.CaptureIdentity / hosted.DiffIdentity (via
# scripts/credfp), the same code the integration suite's host-surface guard
# uses, so the two guards agree. When the command itself fails, its exit status
# is kept, so a red suite stays red and the guard only adds a failure of its own.
# If the helper cannot be built or run the guard fails closed (exit 2).

[ "$1" = "--" ] && shift
if [ "$#" -eq 0 ]; then
	echo "usage: credguard.sh -- <cmd> [args...]" >&2
	exit 2
fi

root=$(cd "$(dirname "$0")/.." && pwd) || exit 2
cred="${XDG_CONFIG_HOME:-$HOME/.config}/satelle/credentials.toml"

work=$(mktemp -d "${TMPDIR:-/tmp}/credguard.XXXXXX") || exit 2
trap 'rm -rf "$work"' EXIT HUP INT TERM

if ! (cd "$root" && go build -o "$work/credfp" ./scripts/credfp); then
	echo "credguard: cannot build scripts/credfp; failing closed" >&2
	exit 2
fi
if ! "$work/credfp" snapshot "$cred" >"$work/before"; then
	echo "credguard: cannot snapshot host credentials: $cred" >&2
	exit 2
fi
if ! "$work/credfp" host-snapshot "$work/host-before"; then
	echo "credguard: cannot snapshot host surface; failing closed" >&2
	exit 2
fi

"$@"
status=$?

# Credentials block.
if ! "$work/credfp" snapshot "$cred" >"$work/after"; then
	echo "credguard: cannot snapshot host credentials: $cred" >&2
	[ "$status" -eq 0 ] && status=2
else
	reasons=$("$work/credfp" diff "$work/before" "$work/after")
	rc=$?
	if [ "$rc" -ne 0 ]; then
		if [ "$rc" -eq 1 ]; then
			echo "credguard: host credentials file changed by tests: $cred" >&2
			printf '%s\n' "$reasons" | sed 's/^/credguard:   /' >&2
			[ "$status" -eq 0 ] && status=1
		else
			echo "credguard: credfp diff failed (exit $rc); failing closed" >&2
			[ "$status" -eq 0 ] && status=2
		fi
	fi
fi

# Host block: the top level of the real ~/.satelle and the installed binaries.
# Like the credentials block it can only raise a passing status.
if ! "$work/credfp" host-snapshot "$work/host-after"; then
	echo "credguard: cannot snapshot host surface; failing closed" >&2
	[ "$status" -eq 0 ] && status=2
else
	reasons=$("$work/credfp" host-diff "$work/host-before" "$work/host-after")
	rc=$?
	if [ "$rc" -eq 1 ]; then
		echo "credguard: host ~/.satelle or installed binaries changed by tests" >&2
		printf '%s\n' "$reasons" | sed 's/^/credguard:   /' >&2
		[ "$status" -eq 0 ] && status=1
	elif [ "$rc" -ne 0 ]; then
		echo "credguard: credfp host-diff failed (exit $rc); failing closed" >&2
		[ "$status" -eq 0 ] && status=2
	fi
fi
exit "$status"
