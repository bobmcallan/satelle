# Acting as the signed-in user without a browser (session token)

A session with no browser — a Claude cloud session, a CI job, a headless box —
cannot complete `satelle login` (an OAuth flow that needs a loopback callback to
the machine running the browser). It acts as the signed-in user with a **session
token** the user grants for that purpose instead.

## Mint a token

The user mints it on the hosted server; it is shown once.

- In the web UI: **Account → Sessions** (lists each token's projects, expiry and
  last use, with a Revoke button).
- Or over the API: `POST /api/v1/me/tokens` with
  `{"name": "...", "session_projects": ["<project>", ...], "expires_in": <seconds>}`.

## Use it

Set it in the environment of the session:

```bash
export SATELLE_TOKEN=sat_pat_…
```

While `SATELLE_TOKEN` is non-empty every hosted call satelle makes — REST and
gRPC — sends it as the bearer, in place of any stored login. There is no browser,
no `credentials.toml`, and no refresh: a stored login is ignored while the token
is set, and `satelle logout` only forgets the stored login (the token stays
active until the variable is unset).

## Who the session acts as

A session token cannot call `GET /api/v1/me`. satelle learns the user id from the
location registration the token is allowed to make, and caches only that id —
keyed by the server and a one-way SHA-256 fingerprint of the token, in
`session-identity.toml` beside `credentials.toml`. The token itself is never
written to disk, a log or the ledger.

- `satelle whoami` resolves it (a network call) and prints the user; if it cannot,
  it prints `session token, user not yet resolved` and the reason, and exits
  non-zero.
- Until it is known the footer and `satelle project status` say `session token,
  user not yet resolved`, and engaging a story is refused — it names `satelle
  whoami` as the way to resolve — rather than passing the wrong-holder check
  unnoticed.
- Once known, the assignee stamp, ledger attribution, footer, `satelle project
  status` and `satelle whoami` all report that user and the session.

## What the token reaches

Story holds, location registration and workstate sync (gRPC `Sync.Apply` and
`Sync.Snapshot`), on its granted projects only. A project outside the token is
reported as `project not in this session token's scope`; every other route
refuses the token.

A session token is never refreshed. When the server refuses it (expired, revoked,
or a route it does not cover) the call fails at once, telling you to mint a new
one.

## Lifetime

At most 30 days; the user can revoke it at any time under Account → Sessions.

## Not covered

UI snapshot push: the server has no snapshot ingest endpoint yet, so a session
token cannot push a snapshot. It needs its own grant once that endpoint exists.
