# Account erasure

Card openpak/prds#120. When an account is deleted, the website finaliser calls this server before the core
drops the account's links. Every title container answers the same route on its healthz port.

```
DELETE http://<container>:<base+2>/internal/accounts/{account_id}/data
Authorization: Bearer $ACCOUNT_ERASE_KEY
-> 200 {"service":"switch-nex","rows":N}
```

- `ACCOUNT_ERASE_KEY` unset: the route answers 503. Wrong or missing key: 401. `account_id` is not a UUID: 400.
- The route needs the account core to turn the account into PIDs: `SWITCH_NEX_CORE_ADDR` + `SWITCH_NEX_CORE_KEY`
  (already set on a title that uses the core leaderboards) or `MK8D_CORE_ADDR` + `MK8D_CORE_KEY`. Without one the route answers 502.
  It lists the account's `switch` links (subject = PID as 16 hex digits).
- Idempotent: a second call removes 0 rows. Other accounts are untouched. One line is logged per erasure:
  `account erased account=<uuid> rows=<n> service=switch-nex`.
- **One container is one title.** The score store (`SWITCH_NEX_DATABASE_URL`) is shared, so any container erases the scores
  and Ranking records of all titles; but the SMM2 and MK8D stores live only in their own unit, and each unit holds its own
  in-memory state and its own reconnect-table row. So the website must call **every** title's healthz port (the list that
  `VOICE_GATHERING_URLS` already carries, with the path replaced). The `rows` of a repeat call are 0.
- There is no `account_deleted` event consumer here: login is refused for a deleting account by nx-baas (403), and the route is the only erase path.

## What is erased

| Where | What | Decision |
| --- | --- | --- |
| `scores.entries` | every score row of the PIDs, all titles, including rows queued for import into the core | deleted |
| `scores.blobs` `r2board`, `r2common`, `rkcommon`, `festvote` | Ranking2 boards and profiles, Ranking common data, Splatfest votes (key ends in the PID in hex) | deleted |
| `scores.blobs` `festsum` | Splatfest team sums | kept: an aggregate with no player in it |
| `scores.blobs` `recall` | address to PID reconnect table (24 h) | the PID is removed from the unit's table, memory and database copy |
| `smm2.users` | the maker profile (name, Mii, clothes) | deleted |
| `smm2.courses` unpublished/deleted + `smm2.relations` + bucket `course/<id>.bcd`, `relation/<id>/<typ>` | | deleted |
| `smm2.courses` published (status 1) | | **kept**, owner set to 0, credited to nobody (other players have them saved) |
| `mk8d.common_data`, `score_packs`, `scores`, `competition_scores` | profile and the legacy score rows | deleted |
| `mk8d.objects` + bucket `ghost/<id>.bin` | ghosts and data objects | deleted |
| `mk8d.tournaments` | tournaments the player created | **kept**, owner 0 (players joined them) |
| process memory | friends, notification data, title version, NAT class, Ranking records, Splatfest votes, SMM2 profile cache, tournaments, nx-baas proof and voice records | dropped |

## What is not here

- Scores uploaded for players with an account link live in the **core leaderboards** service; that service erases them.
- The rewards outbox (events carrying the account) belongs to the rewards service.
- A live connection of the account is not cut by this route; nx-baas answers 403 on the next login and the ban sweep drops the session.
