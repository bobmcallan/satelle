---
type: document
title: 'solidsafe-engine-v0 — brief list of changes'
description: 'Short summary of the backup-framework port, solidsafe_engine rename, scripts packaging, and xArchive of legacy frontend/k8s/container paths.'
tags:
- document
- solidsafe
- solidsafe-engine
- changelog
- packaging
timestamp: '2026-07-10T00:00:00Z'
---

# solidsafe-engine-v0 — brief list of changes

Summary of what landed in this repo relative to the source `backup-framework` port.
Also mirrored at repo root as `CHANGES.md`.

## Port and auth seam

- Ported the backup engine and six live connectors (Google, Mailchimp, Pipedrive, Smartsheet, Xero, Xero PM).
- Added a **trusted-gateway identity seam**: gateway sends `X-SolidSafe-User` (and optional shared-secret header); controlserver scopes work by user groups.
- Documented architecture boundary with `solidsafe-app` (HTTP gateway, not a pip dependency).

## Package and layout rename

- Renamed engine package **`rainbow_wizard/` → `solidsafe_engine/`**.
- Updated README and `.gitignore` paths to match.
- Repo title/docs refer to **solidsafe-engine-v0**.

## Packaging

- **New** `scripts/` — Dockerfile + Compose (postgres, kafka, minio, controlserver, workers); no frontend in the image.
- Role selection still via `SERVICE` env (`controlserver`, `taskrunner`, `jobrunner`, etc.).
- Host controlserver port defaults to **5000**.

## Archived (not on the active path)

Moved under **`xArchive/`** (kept for history, not used by `scripts/`):

- Internal admin `frontend/`
- `k8s/` Helm chart and deploy scripts
- `rainbow_wizard_container/` Podman/Harbor build
- `fileserver/` Azure SMB storage ARM template
- Scratch notes (`DESIGN.md`, `dump_zone.py`, etc.)

## Known carry-overs (still open)

- `crud.get_user()` id filter is commented out; auth path uses an explicit id query instead.
- Hardcoded connector/OAuth secrets remain in `solidsafe_engine/settings.py` — rotate and move to env before treating as production-safe.
