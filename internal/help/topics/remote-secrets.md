# satelle remote secrets — how a remote gets the secrets it needs

A remote, personal or a team's, local- or satelle.dev-orchestrated, reads its
secrets the same way: **remote → satelle.dev ← local**. satelle.dev holds an
encrypted copy of each value; local satelle (or a team admin) puts it there; the
remote collects it when a step needs it, with a request signed by its own key and
bound to the one run it serves. The remote's definition declares every secret by
key, scope, source and lifetime; it never holds a value.

## Declaring a secret

```toml
remote_id = "rmt_…"          # copied from the remote's registration on satelle.dev

[identity]
label = "…"
key = "SATELLE_REMOTE_KEY"   # the host variable that holds the remote's private key

[[secret]]
key = "CLAUDE_CODE_OAUTH_TOKEN"
scope = "personal"           # personal | team (team also names workspace = "<id>")
from = "env"                 # env: read the key from .satelle/config/.env
expires = "2027-10-01"       # a date, or ttl = "36h" / "30d" — at most one

[[secret]]
key = "MINTED"
scope = "personal"
from = ["mint-token", "--short"]   # an argv that prints the value; run locally, no shell
ttl = "36h"
```

`satelle remote validate` refuses a literal value, an unknown scope, both
`expires` and `ttl`, and a team secret with no workspace, each naming the secret
as `secret[i] (KEY)`. `[vars]` is `${VAR}` expansion for bindings and is never a
secrets source: `from = "env"` reads `.satelle/config/.env` only.

`remote_id` is the id satelle.dev gave the remote when you registered it there;
satelle does not register remotes. Every call that addresses the remote on the
server takes it from the definition and refuses, naming the fix, when it is unset.

## Putting values on satelle.dev

- `satelle secret push <remote>` reads each declared value (from `.env`, or by
  running its argv), sends it under its scope with your satelle login, and grants
  it to the remote. A `ttl` secret goes up with `expires_at = now + ttl`.
- `satelle secret list [<remote>] [--workspace <id>]` shows key, scope, readers and
  expiry. It never shows a value; satelle.dev returns none to a person.
- Before `remote start` or `remote run` registers its run, a `ttl` secret whose copy
  on satelle.dev has lapsed (or is within a tenth of its ttl of lapsing) is obtained
  again and pushed again. A team's dynamic secrets are refreshed the same way, by a
  scheduled team remote that runs `satelle secret push`.
- `satelle remote check <remote>` reports a declared secret that is missing on
  satelle.dev, not granted to the remote, or within 14 days of its expiry, and a
  `host.story_command` that would not read secrets. It exits non-zero on all but
  the expiry warning.

## The remote's identity

An Ed25519 key per remote: `satelle remote key create|rotate|register <remote>`.
The private key is printed once for the host's secret store, under the variable
`identity.key` names; only the public key is registered, and satelle writes
neither half to any file. Instead of a key, a platform with workload identity
tokens can be bound with `[identity.oidc]` (issuer, subject, and `token`, the argv
that prints a token for `{aud}`) and `satelle remote identity set|clear`.

## What a run does

The run is `trigger_id`: the fire satelle.dev woke the remote for, or, for a start
the owner makes, the run `remote start` / `remote run` registers once under the
owner's login. For each declared secret the node sends one signed request
(`satelle-remote-read-v1`: method, path, trigger_id, key, scope, workspace, nonce,
timestamp) to `POST /api/v1/remotes/<remote_id>/runs/<run>/secrets/<key>`. The value
goes to the step performer's agent process environment alone — not to a reviewer, a
summariser, a file, the ledger or a log — and the remote's key variable is dropped
from every agent. A refusal is printed as satelle.dev gave it
(`secret KEY refused: <error>: <reason>`) and the agent is not started.

`SATELLE_SERVER_RUN_ID`, `SATELLE_REMOTE_ID`, `SATELLE_REMOTE_SERVER`,
`SATELLE_REMOTE_SECRETS` (names, scopes, workspaces) and `SATELLE_REMOTE_IDENTITY`
carry the run to a story command. They reach the host only if `host.run` forwards
the environment (run and wake); `remote start` hands the same carrier to the node in
its sealed setup instead. `SATELLE_RUN_ID` is still the local ledger run. Run the
story with `satelle story drive`, the command that reads the carrier.
