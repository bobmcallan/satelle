#!/bin/sh
# credguard.sh -- run a command and fail if it changed the operator's host
# credentials file (sty_18403814).
#
# Usage: credguard.sh -- <cmd> [args...]
#
# The file is ${XDG_CONFIG_HOME:-$HOME/.config}/satelle/credentials.toml, the
# per-user store `satelle login` writes. A test suite must leave it untouched;
# this records its sha256 (or "absent") before and after the command and exits 1
# with a message when they differ. When the command itself fails, its exit status
# is kept, so a red suite stays red and the guard only adds a failure of its own.

[ "$1" = "--" ] && shift
if [ "$#" -eq 0 ]; then
	echo "usage: credguard.sh -- <cmd> [args...]" >&2
	exit 2
fi

cred="${XDG_CONFIG_HOME:-$HOME/.config}/satelle/credentials.toml"

fingerprint() {
	if [ -f "$cred" ]; then
		{ sha256sum "$cred" 2>/dev/null || shasum -a 256 "$cred"; } | cut -d' ' -f1
	else
		echo absent
	fi
}

before=$(fingerprint)
"$@"
status=$?
after=$(fingerprint)

if [ "$before" != "$after" ]; then
	echo "credguard: host credentials file changed by tests: $cred" >&2
	echo "credguard:   before: $before" >&2
	echo "credguard:   after:  $after" >&2
	[ "$status" -eq 0 ] && status=1
fi
exit "$status"
