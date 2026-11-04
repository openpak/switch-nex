# Mario Kart 8 Deluxe: rankings, competitions, ghosts

`switch-mario-kart-8-deluxe` stores time-trial rankings, tournament leaderboards and ghosts durably when
`MK8D_DATABASE_URL` is set. Unset, it behaves as before: empty boards, every upload acknowledged,
no DataStore.

Sources (facts only, no code): the NintendoClients wiki by Kinnay, MIT: *Ranking Protocol*,
*Ranking Protocol (MK8D)*, *Matchmake Extension Protocol (MK8D)*, *Data Store Protocol*
(github.com/Kinnay/NintendoClients/wiki). The Wii U title in this workspace
(`platforms/wiiu-3ds/games/nex/titles/mario-kart-8`) has the same field types and is the shape of the read API.

## Configuration

| Variable | Meaning |
| --- | --- |
| `MK8D_DATABASE_URL` | Postgres DSN (database `mk8d`, schema `mk8d`, created on start) |
| `MK8D_S3_ENDPOINT` | bucket endpoint this process dials; unset = no DataStore ghosts |
| `MK8D_S3_PUBLIC_URL` | base URL the console is told (SigV4 binds the host); default the endpoint |
| `MK8D_S3_BUCKET` | default `nex-mk8d`, created if missing |
| `MK8D_S3_ACCESS_KEY`, `MK8D_S3_SECRET_KEY` | MinIO credentials |
| `MK8D_API_ADDR` | read API listener (production `:22854`) |
| `MK8D_LEADERBOARDS_ADDR`, `MK8D_LEADERBOARDS_KEY` | core leaderboards gRPC (`127.0.0.1:20211` on the box) and `$LEADERBOARDS_INTERNAL_KEY`; required |
| `MK8D_CORE_ADDR`, `MK8D_CORE_KEY` | account core gRPC (`127.0.0.1:20001`) and `$ACCOUNT_INTERNAL_KEY`; PID to account links; required |
| `RANKINGS_ADDR` | no longer served for this title; the website reads the core's contract (`/t/mario-kart-8-deluxe`) |

Values go through `os.ExpandEnv`, so the quadlet names `$PGPASSWORD` and the shared `.env` supplies it.

## Where things live

| What | Where |
| --- | --- |
| Scores | the core leaderboards service, title `mario-kart-8-deluxe` (Switch id `0100152000022000`): board `<course>` (ascending ms) and `t<competition>s<season>` (descending points, last upload wins); account = the PID's link in namespace `switch`; columns `uid`, `param` / `team`, `team_score`; info = group bytes / competition metadata. A PID with no account link is acknowledged and not stored. |
| Legacy scores | `mk8d.scores`, `mk8d.competition_scores`: no longer written. `MigrateLegacy` copies every row into the core at start (oldest first) and sets `migrated`; rows are never deleted, a row whose PID has no link is retried every ten minutes |
| Player common data (132 bytes on MK8D) | `mk8d.common_data`, key pid |
| Score packs (UploadScorePack) | `mk8d.score_packs` |
| Tournaments (matchmaking descriptors; the season counter) | `mk8d.tournaments` |
| Ghost metadata | `mk8d.objects`, ids from `mk8d.object_ids` (start 1000000) |
| Ghost body | bucket `nex-mk8d`, key `ghost/<dataId>.bin` |

## Methods

Ranking (protocol 0x70). The method numbers are the MK8D wiki's decimal ids in hex.

| Method | Behaviour |
| --- | --- |
| 0x01 UploadScore | stores; still answers bool `true` (measured with Mario Golf) |
| 0x04 UploadCommonData | kept in memory and in Postgres |
| 0x09 GetRanking, 0x0C ByPIDList, 0x0D ByUniqueIDList | real `RankingResult` (rows, total, sinceTime) |
| 0x10 GetCompetitionRankingScore | seasons current-N..current, top 20 per season, highest score first |
| 0x11 UploadCompetitionRankingScore | stores; answers ONE zero byte (kept as measured) |
| 0x12 GetCompetitionInfo | competitions by participants |
| 0x13 UploadScorePack | stores the metadata blob and the score; answers ONE zero byte |
| 0x14 GetScorePack | one qBuffer per requested PID, in order |
| 0x16 GetCommonDataByPIDList | unchanged: one entry per PID, in order, struct + QBuffers |

Update rules (`UploadScore`): update mode 1 always overwrites; any other mode keeps the best, and the board
is ascending (lowest time wins). Reads: mode 0 global (offset/length, nothing past rank 1000), mode 1 a window
centred on the caller's own entry, mode 4 the caller's entry only; the by-PID and by-unique-id lists return
the listed players with their GLOBAL rank. Rank calculation 0 is standard (1224, ties share a rank), 1
ordinal (1234). The group filter (`groupIndex`/`groupNum`) selects entries whose `groups[groupIndex]`
equals `groupNum`; index 0xFF means no filter. The time scope is ignored (boards never reset).

Matchmake Extension tournaments (0x6D, ids 0x39 search, 0x42 create, 0x43 delete as already measured): with a
store the descriptor is persisted, gets a durable id (written into the descriptor's leading object id when the
console sent 0), and name / attributes / start / end are parsed best-effort for the site. The raw descriptor
is the source of truth and is always stored.

DataStore (protocol 0x73, `DSMK8D`, only with a store and a bucket): 0x18 PreparePostObject (presigned S3 POST),
0x1A / 0x25 CompletePostObject(s), 0x19 PrepareGetObject (presigned GET), 0x08 GetMeta, 0x09 GetMetas,
0x24 GetMetasMultipleParam, 0x0C / 0x2E SearchObject(Light), 0x1C GetSpecificMeta, 0x1D / 0x1E
GetPersistenceInfo(s), 0x2D GetObjectInfos, 0x04 DeleteObject. Everything else falls back to the stub.
A persistence slot keeps only its newest object. Owner-only objects (permission 3) are served to the owner only.
Ghost size cap 4 MiB.

## FACT vs INFERRED

FACT (the wiki documents it):
- Layouts of RankingScoreData, RankingOrderParam, RankingRankData, RankingResult, the three board reads,
  ranking modes 0/1/4 and the rank calculation values.
- The MK8D ranking method ids (16..20, 22) and their parameter types; the structure field TYPES of the
  competition structures; SimpleSearchObject and the DataStore structures.
- Time-trial scores are milliseconds and rank ascending (Wii U MK8D page, `ranking_mk8d.py`).

INFERRED (no Deluxe console capture exists yet; the first capture must confirm each):
1. **Course id.** The Ranking `category` is the course (and the ghost's DataStore `dataType` is the same
   number). Deluxe has no published category table. The read API calls this number `course`; its meaning is
   whatever the console sends. The site should show it as "course N" until a mapping is captured.
2. `orderBy` and `updateMode` values. Treated as: update mode 1 overwrites, anything else keeps the best;
   the board is always ascending. Both bytes are logged on every upload (`ranking: ... upload category=`).
3. The competition upload field order (id, season, unknown, score, team, team score, first-upload flag,
   metadata) and that the competition id equals the tournament id. The wiki lists only types. Unknown
   competition ids create a placeholder tournament row so scores are never lost.
4. `UploadCompetitionRankingScore` answering one zero byte (a bool false per the wiki) is kept from the
   measured server; whether `true` is wanted is unknown.
5. Ghost objects: that a ghost uses a persistence slot, that `name`/`tags`/`metaBinary` carry nothing the
   server needs. Every `PreparePostObject` body is logged in hex (`mk8d: PreparePostObject ... hex=`) and the
   raw parameter is stored in `mk8d.objects.raw`, with the meta binary shown as `meta_hex`.
6. The player's display name inside the 132-byte common data: the wiki documents no Deluxe layout, so the
   API shows the PID and no `name`.
7. The tournament name is read from the metadata chunk format of the Wii U title (chunk 2 name, chunk 4
   description, UTF-16BE); it stays empty if the Deluxe blob differs.
8. Friends boards: the console is assumed to ask with `GetRankingByPIDList` and a friends PID list.

## Read API

JSON, container network only, never published. Listener `MK8D_API_ADDR` (production `:22854`), no auth header,
the same shape as the Mario Kart 8 (Wii U) API: lists are `{total, offset, limit, items}` (limit default 20, cap
100), PIDs are strings, errors `{"error": "..."}` with 400 / 404 / 503 / 501.

| Endpoint | Returns |
| --- | --- |
| `GET /api/v1/courses` | `{items: [{course, ranked, ghosts}]}` |
| `GET /api/v1/courses/{n}/rankings` | `{rank, player, time_ms, time, updated, ghost?}`, fastest first |
| `GET /api/v1/courses/{n}/ghosts?owner=` | ghost Objects with `course`, `time_ms`, `time` (the owner's score) |
| `GET /api/v1/objects/{id}` / `/body` | one ghost / its bytes as an attachment (501 without a bucket) |
| `GET /api/v1/players/{pid}/rankings` | `{items: [{course, rank, of, time_ms, time}]}` |
| `GET /api/v1/tournaments` | tournaments, newest first |
| `GET /api/v1/tournaments/{id}?season=` | one tournament with the top 100 of a season (current by default) |

Only finished, public (permission 0) ghosts are listed or served.

## Tests

`go test ./protocols ./internal/mk8dstore`. The Postgres tests (update semantics, rank modes, ties, groups,
competitions, ghosts, the read API, the legacy migration, a restart) run against an in-memory fake of the core when `MK8D_TEST_DATABASE_URL` points at a scratch database.
The group filter and the unique-id list have no index in the core: the adapter reads the board (at most 10000 entries) and filters.

## The console test that is needed

With the console on OpenPak and a fresh Mario Kart 8 Deluxe save:
1. Run one time trial on one course to the end and upload the ghost (finish a lap run, accept "upload").
   Then watch the title log: `ranking: pid=~... upload category=<N> score=<ms> order=<o> update_mode=<m>`
   and `mk8d: PreparePostObject ... hex=...`. Confirm `category` is a per-course id, `score` equals the
   time in ms, and note `order` / `update_mode`.
2. Open the same course's worldwide ranking: it must show your time at rank 1. Run a second, slower attempt:
   the board must keep the first. Run a faster one: it must improve.
3. Download the ghost from the ranking (Download Ghost): the console must fetch the S3 URL; check
   `GET /api/v1/courses/<N>/ghosts` lists it with `time_ms`.
4. Create a tournament, play a round, and check `GET /api/v1/tournaments` and
   `mk8d: UploadCompetitionRankingScore ... hex=` in the log; confirm the competition id equals the
   tournament id returned by the API.
5. Report the logged hex lines; they settle every INFERRED item above.
