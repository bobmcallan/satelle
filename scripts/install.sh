#!/bin/sh
# satelle bootstrap installer — the `curl | sh` first-touch:
#
#   curl -fsSL https://github.com/bobmcallan/satelle/releases/latest/download/install.sh | sh
#
# (Published as a release asset on the CLI release, which remains GitHub "latest".)
#
# CLI and serve carry independent versions (sty_19ff03f4):
#   - CLI: tag v<X>, asset satelle-v<X>-<os>-<arch>, always latest
#   - serve: tag serve-v<Y>, asset satelle-serve-v<Y>-…, not latest
#
# Next: `satelle init` in a repo, then `satelle service install`.
set -eu

REPO="bobmcallan/satelle"
INSTALL_DIR="${SATELLE_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) echo "satelle install: unsupported arch '$arch'" >&2; exit 1 ;;
esac
case "$os" in
	linux | darwin) ;;
	*) echo "satelle install: unsupported OS '$os' (use the .exe asset on Windows)" >&2; exit 1 ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# --- CLI (always releases/latest) ---
# Fetch the API body to a FILE, then grep the file. Never `curl | grep -m1`:
# grep exits on its first match, curl takes EPIPE mid-body and prints
# `curl: (23) Failure writing output to destination` — a cosmetic error that
# reads as a failed install (sty_87b5d4bc). No `2>/dev/null` and no `|| true`
# here: this lookup is HARD-fail, so `set -e` aborts and curl's own diagnostic
# (`curl: (6) Could not resolve host`, a 404, …) still reaches the operator.
curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" -o "$tmp/latest.json"
tag=$(grep -m1 '"tag_name"' "$tmp/latest.json" | cut -d'"' -f4)
[ -n "$tag" ] || { echo "satelle install: could not resolve latest release" >&2; exit 1; }

name="satelle-$tag-$os-$arch"
url="https://github.com/$REPO/releases/download/$tag/$name"

echo "satelle install: fetching $name ..."
curl -fsSL "$url" -o "$tmp/satelle"
curl -fsSL "$url.sha256" -o "$tmp/satelle.sha256"

want=$(cut -d' ' -f1 "$tmp/satelle.sha256")
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/satelle" | cut -d' ' -f1)
else
	got=$(shasum -a 256 "$tmp/satelle" | cut -d' ' -f1)
fi
[ "$want" = "$got" ] || { echo "satelle install: sha256 mismatch (want $want, got $got)" >&2; exit 1; }

chmod +x "$tmp/satelle"
mkdir -p "$INSTALL_DIR"
mv "$tmp/satelle" "$INSTALL_DIR/satelle"
echo "satelle install: installed $INSTALL_DIR/satelle ($tag)"

# --- serve (independent serve-v* tags; soft-fail if none) ---
# Newest-first releases list; first serve-v* tag wins. Asset name uses v<Y>
# (strip serve- prefix), never the full tag.
# De-piped for the same reason as the CLI lookup above, so `head -1` truncates a
# grep reading a local file rather than a live curl. Unlike that one this branch
# is deliberately SOFT-fail: `|| true` on both statements (and `2>/dev/null` on
# the grep, for the case where the fetch left no file) so a missing serve release
# falls through to the fallback branch below and still exits 0.
curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=30" -o "$tmp/releases.json" || true
serve_tag=$(grep -oE '"tag_name": "serve-v[^"]+"' "$tmp/releases.json" 2>/dev/null \
	| head -1 | cut -d'"' -f4 || true)
if [ -n "$serve_tag" ]; then
	serve_ver=${serve_tag#serve-}
	serve_name="satelle-serve-$serve_ver-$os-$arch"
	serve_url="https://github.com/$REPO/releases/download/$serve_tag/$serve_name"
	if curl -fsSL "$serve_url" -o "$tmp/satelle-serve" 2>/dev/null \
		&& curl -fsSL "$serve_url.sha256" -o "$tmp/satelle-serve.sha256" 2>/dev/null; then
		swant=$(cut -d' ' -f1 "$tmp/satelle-serve.sha256")
		if command -v sha256sum >/dev/null 2>&1; then
			sgot=$(sha256sum "$tmp/satelle-serve" | cut -d' ' -f1)
		else
			sgot=$(shasum -a 256 "$tmp/satelle-serve" | cut -d' ' -f1)
		fi
		if [ "$swant" = "$sgot" ]; then
			chmod +x "$tmp/satelle-serve"
			mv "$tmp/satelle-serve" "$INSTALL_DIR/satelle-serve"
			echo "satelle install: installed $INSTALL_DIR/satelle-serve ($serve_tag)"
		else
			echo "satelle install: satelle-serve sha256 mismatch — skipped (use satelle serve fallback)" >&2
		fi
	else
		echo "satelle install: could not fetch $serve_name — service install will fall back to satelle serve"
	fi
else
	# Pre-split releases may still carry both under v*; try same tag once.
	serve_name="satelle-serve-$tag-$os-$arch"
	serve_url="https://github.com/$REPO/releases/download/$tag/$serve_name"
	if curl -fsSL "$serve_url" -o "$tmp/satelle-serve" 2>/dev/null \
		&& curl -fsSL "$serve_url.sha256" -o "$tmp/satelle-serve.sha256" 2>/dev/null; then
		swant=$(cut -d' ' -f1 "$tmp/satelle-serve.sha256")
		if command -v sha256sum >/dev/null 2>&1; then
			sgot=$(sha256sum "$tmp/satelle-serve" | cut -d' ' -f1)
		else
			sgot=$(shasum -a 256 "$tmp/satelle-serve" | cut -d' ' -f1)
		fi
		if [ "$swant" = "$sgot" ]; then
			chmod +x "$tmp/satelle-serve"
			mv "$tmp/satelle-serve" "$INSTALL_DIR/satelle-serve"
			echo "satelle install: installed $INSTALL_DIR/satelle-serve ($tag, combined release)"
		fi
	else
		echo "satelle install: no satelle-serve release yet — service install will fall back to satelle serve"
	fi
fi

case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*) echo "satelle install: add $INSTALL_DIR to your PATH (e.g. export PATH=\"$INSTALL_DIR:\$PATH\")" ;;
esac

# Installing and upgrading are the same command, and this script cannot reliably
# tell which one it just did. So it names BOTH cases rather than guessing
# (sty_0f471251): a fresh install ignores the second block, and an upgrade — which
# has just invalidated the scaffolding of every repo already on this machine —
# finally gets told.
echo
echo "Next (a new repo):"
echo "  cd <your-repo>"
echo "  satelle init             # scaffold .satelle/ (config, db, authored dirs)"
echo "  satelle service install  # always-on web project page (uses satelle-serve when present)"
echo
# This script INFORMS about the running service; it never restarts it
# (sty_a7b2cd3c). `satelle update` restarts without asking because it is a verb
# the operator invoked interactively about satelle's own installed state, with a
# --no-restart opt-out, and it reports the outcome. This is a curl-to-shell
# script: often non-interactive, may run under provisioning or CI, offers no
# opt-out the operator saw before piping it to a shell, and one system unit can
# serve a whole estate of repos. Cycling shared always-on infrastructure is a
# side effect nobody asked for — the consent given here was "install a binary".
echo "Repos you already have (if this was an upgrade):"
echo "  satelle doctor --all     # read-only: scaffolding written by an older satelle is stale"
echo "  satelle init --all       # dry-run: what healing them would change (--yes applies)"
echo "  satelle service restart  # a running service stays on the OLD binary until restarted"
