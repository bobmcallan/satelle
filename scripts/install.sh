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

# --- CLI (always releases/latest) ---
tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
	| grep -m1 '"tag_name"' | cut -d'"' -f4)
[ -n "$tag" ] || { echo "satelle install: could not resolve latest release" >&2; exit 1; }

name="satelle-$tag-$os-$arch"
url="https://github.com/$REPO/releases/download/$tag/$name"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

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
serve_tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=30" \
	| grep -oE '"tag_name": "serve-v[^"]+"' | head -1 | cut -d'"' -f4 || true)
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

echo
echo "Next:"
echo "  cd <your-repo>"
echo "  satelle init             # scaffold .satelle/ (config, db, authored dirs)"
echo "  satelle service install  # always-on web project page (uses satelle-serve when present)"
