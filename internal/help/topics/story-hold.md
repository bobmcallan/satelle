# satelle story hold — who holds a story, and taking it over

A story engaged on one machine is held there: the hosted server records the
holding location, its label and when it was last seen. Other machines see the
hold and cannot engage the story. Holds do not expire on their own.

## How a hold shows up

Every work-state pass (`satelle sync workstate snapshot`, which satelled runs,
and `satelle sync workstate pull`) records each story another location holds, and
clears a hold that was released or whose story finished. The record is local
state; nothing reads the server to show it.

- `satelle story get` and `satelle story list` mark the story
  `held: remote <label or location>, last seen …`, and `presumed dead` when the
  last-seen is older than `[sync] hold_stale_after` (default 2h; unlike
  `stale_after`, which judges the age of the local mirror).
- A session start names the held stories, and the stories whose local and hosted
  copies differ, on one line.
- Engaging a held story is refused. The refusal says whether the holder looks
  live or presumed dead.

A story whose local and hosted copies differ is a conflict. A pass records it and
leaves both copies alone; it is never resolved silently.

## Taking over

`satelle story hold takeover <id>` moves the hold here only when all three hold:

1. **liveness** — the holder released the hold, the story is terminal on the
   hosted copy, or the holder's last-seen is older than `[sync] hold_stale_after`.
2. **status** — a scoped snapshot of the story records no conflict for it.
3. **code** — after a fetch, every commit the story's synced ledger claims (the
   one it was engaged at, and the one its work ended at) is in this repository,
   and a story whose work started on the holder has a post-work commit here.
   When work started but none is reachable, the code may exist only in the
   holder's tree.

A refused takeover names each failing check and its fix. `--force --reason
<text>` is the only override; it records the reason and the failed checks in the
story's ledger.
