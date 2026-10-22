# Server parity — gap report (2026-09-16)

Method-by-method comparison of every protocol `switch-nexd` serves against the
measured servers. Read for **facts only** per `docs/clean-room-policy.md`:
opcodes, wire field orders, error codes, response shapes. No outside code,
structure, naming or comments carried across.

Sources: earlier research notes for these titles (mario-party-superstars, mario-golf-super-rush, arms,
mario-kart-8-deluxe, super-mario-bros-35, mario-tennis-aces, animal-crossing-new-horizons,
luigis-mansion-3, mario-strikers, minecraft, splatoon-2, super-mario-maker-2, super-mario-odyssey,
super-smash-bros-ultimate). No notes exist for Clubhouse Games 51.

Live evidence: production host reached over `<ssh-agent-socket>`
(`<prod-host>`). `openpak-switch-nexd.service` active since
2026-09-16 05:18:38 UTC, 15/15 listeners up, **zero client traffic since
start** — the live `NotImplemented` backlog is empty, so nothing below comes
from logs. It is all static comparison.

---

## Status — closed 2026-09-16

All 43 findings are implemented, `GOWORK=off go build/vet/test ./...` is green,
and the per-title registry and `docs/legacy-parity.md` are corrected. Two
entries landed narrower than the heading suggests, and say so where they sit:

- **#32 (login-name to PID map)** is groundwork only. The table is populated at
  login, but nothing reads it yet: serving a profile means emitting the title's
  26-field user structure, whose unknown flags and stat-map keys are not pinned,
  and a partial one shifts every field after it. The current "no profile stored"
  answer stays correct until a real store exists.
- **#30 (Ranking 0x10/0x12)** answers the right shapes with no content, which is
  what a server holding no competitions has to say.

One defect not in the report surfaced while fixing #17 and is fixed with it: the
matchmake parameter map inside a session was also written in Go's randomised map
order, so re-encoding a session did not reproduce its own bytes and an echoed
session reordered its parameters on every call. It was an intermittent failure
in the existing round-trip test, about one run in three.

---

## What already matches (verified, no action)

- **Kerberos**: two-layer v1 ticket, `md5(pw)` → append PID u64 LE → `md5`
  derivation, RC4 + trailing HMAC-MD5, random 16-byte ticket key mixed into the
  target key, both blobs u32-length-prefixed. Byte-identical.
- **RMC framing**: u32 length prefix, protocol byte with 0x80 request bit,
  request `callID/method/body`, success `1/callID/method|0x8000/body`,
  error `0/code|0x80000000/callID`.
- **Byte-stream profile**: little-endian, String = u16 length *including* NUL,
  Buffer = u32, bool = u8, PID = u64, structure header = u8 version + u32 length.
- **LoginEx (0x0A.0x02) body**: retval, PID, ticket Buffer, RVConnectionData
  v1 (URL, list<u8> protocols, special station, DateTime), ReturnMsg, SourceKey hex.
- **Secure station URL params**: `address, port, CID=1, PID, sid=1, stream=10, type=2`.
- **MatchmakeSession / Gathering** field order and struct nesting.
- **Ranking2Info**: list, LowestRank, NumRankedIn, Season(i32).
- Access keys recorded in `docs/legacy-parity.md` match the measured server exactly.

---

## P0 — breaks a flow the moment a console connects

### 1. TicketGranting 0x06 answers the wrong structure
Measured `ValidateAndRequestTicketWithParam` returns, inside one v1 struct header:

    PID SourcePID | Buffer ticket | String ServiceNodeURL | DateTime CurrentUTCTime
    | String ReturnMsg | String SourceKeyHex

We wrap the **LoginEx** body instead (`cmd/switch-nexd/main.go:handleLoginEx`),
which adds a leading `u32 retval` and replaces the bare URL String with a whole
RVConnectionData structure. The measured server records this exact mistake producing
`Core::InvalidArgument` / **2306-0116** on the client.
Affects every title whose live login path is method 6 (4.4+: MPS, Golf, SMB35,
Clubhouse, SSBU).

### 2. SecureConnection 0x01 Register returns the wrong shape
The measured server answers 0x01 and 0x04 **identically**: `u32 retval, u32 pidConnectionID,
String urlPublic`. We answer 0x01 with a lone String (`protocols/secureconnection.go`).
Affects the pre-5.19 Pia titles that use plain Register: ARMS, MTA, Splatoon 2, Strikers.

### 3. Register returns the server's URL, not the client's public station
The measured server reads the client's reported station list, splits LAN vs public by
address (RFC1918 + RFC6598 CGNAT, parsed not prefix-matched), synthesises the
public station by **copying the LAN one** and repointing address/port at the
observed remote endpoint, then stamps `PID`, `RVCID`, `type`, and (Pia 5.19)
`Pa`. We return `env.SecureStationURL()` — our own secure endpoint — so the
console never learns its own public endpoint and Pia cannot build its mesh.
The measured server notes a bare station here yields **2618-562 SessionKeepFailed**.

### 4. Connection id is hardcoded to 1
`protocols/secureconnection.go` writes `w.U32(1)` for RegisterEx's connection id,
and `stationurl.go` hardcodes `CID=1`. The measured server assigns a per-connection id
and puts it in both `RVCID` and the response. Two consoles on one title collide.

### 5. `DataStore::NotFound` constant has transposed digits
`rmc/types.go`: `ResultDataStoreNotFound = 0x00069004` → goes out as
**0x80069004**. The correct value is `0x00690004` → **0x80690004**, confirmed in
three independent measured places (`datastore_smm2.go` citing kinnay's
errors.py, and the MPS/SSBU/Strikers stubs hardcoding `0x80690004`). Our own
comment on that line already states 0x80690004 — the constant contradicts it.
Affects every `GetMeta` answer, SMM2 `GetUsers` per-PID results and Clubhouse
`GetPersistenceInfos` per-slot results.

### 6. 0x6D.0x3C answered with an empty list
Measured `CustomGetSimplePlayingSession`: request `list<PID> + bool includeSelf`,
response `list<SimplePlayingSession{PID u64, gid u32, gameMode u32, attr0 u32}>`
(struct-framed). This is how the friend list learns which gathering to join.
We answer a hardcoded empty list (`protocols/matchmaking.go` case 0x3C), citing
"S2 measured empty". The measured server states plainly that the empty answer makes
**every friend-join fail** because the joiner never learns the session id.

### 7. NAT 0x03.0x06 GetRelaySignatureKey answered empty
The measured server returns six fields:
`i32 relayMode=0 | DateTime now | String "" | u16 0 | i32 0 | u32 0`
— a well-formed "no relay offered" so the game proceeds on its direct mesh.
We answer an empty body (`protocols/nattraversal.go` lumps 0x01–0x07 together).
The measured server records the unparseable answer as the cause of Mario Strikers
connecting but never rendering (black screen).

### 8. 0x6D.0x33 request is a structure — we parse it as a bare list
The measured server reads `FindMatchmakeSessionByParticipantParam` as a NEX Structure, so
with struct headers on the body starts with **u8 version + u32 content length**
before `list<PID>`, `u32 options`, `MatchmakeBlockListParam`. Our handler calls
`readPIDList` straight off `msg.Body` (`protocols/matchmaking.go` case 0x33), so
it reads the version byte and part of the length prefix as the list count — it
will hit the 300 guard or short-read and answer `InvalidArgument` for every real
request. ACNH's island visits (which our own notes say depend on this method
carrying the full session) cannot work today.

---

## P1 — wrong bytes or wrong method number

| # | Where | Measured | Ours |
|---|---|---|---|
| 9 | Ranking2CommonData (0x7A.2/3) | `StringVideZero(userName)`, **QBuffer** mii, **QBuffer** binaryData | `String` (1+NUL when empty), `Buffer` u32 ×2 → 5 bytes of drift |
| 10 | Ranking2CategorySetting (0x7A.7) | ResetMonth **u16**; ResetMode 0; MaxSeasonsToGoBack 0; ScoreOrder **true** | ResetMonth u32 (+2 bytes, desyncs the rest); 2; 3; false |
| 11 | Ranking2 GetEstimateMyRank | method **11 (0x0B)**, SamplingRate **1** | we serve 0x0A, SamplingRate 0 |
| 12 | Ranking2 GetCommonData miss | returns an **empty structure** (zero bytes overflows the client → 2306-0116) | returns `Ranking2::InvalidArgument` |
| 13 | Referee (0x78) | 3 EndRound, 4 EndRound**PartialReport**, 5 EndRoundWithoutReport | our 0x04 is labelled WithoutReport; 0x05 unhandled |
| 14 | Utility 0x02 | `UniqueIDInfo{u64 id, u64 password}` — measured server: unanswered ⇒ Splatoon 2 shows **2306-0103** | empty success |
| 15 | Utility 0x05 | same `UniqueIDInfo` structure | bare u64 from a counter |
| 16 | Utility 0x01 | the caller's **PID** (stable across restarts and sessions) | process-local incrementing counter |
| 17 | Utility 0x07 map | client reads **positions**, so key order is load-bearing | `writeU16U32Map` ranges a Go map — randomised order every call |
| 18 | LoginEx SpecialStation | renders `"prudp:/"` | we write the literal `"prudp"` |
| 19 | Ranking 0x70 method set | 0x04 UploadCommonData, 0x10 CompetitionRanking, 0x11 SubmitScore, 0x12 GetCompetitionInfo, 0x13 SubmitTime, 0x16 CommonDataByPIDs — everything else NotImplemented. SubmitScore/Time answer **one byte `00`**, not empty | we serve 0x01/0x02/0x03/0x05/0x06/0x07/0x08/0x09/0x0A/0x0C/0x0D/0x12/0x13/0x19 with different semantics, and none of 0x10/0x11/0x16 |
| 20 | RMC protocol byte | escape via 0x7F for protocol ≥ **0x80** | we escape only at ≥ 0x100 — latent, no served protocol is ≥0x80 yet |

---

## P2 — subsystems the measured server has and we do not

21. **Server-initiated push.** The measured server sends `ProcessNotificationEvent` as an RMC
    *request* on the client's own connection (`callID = 0xFFFE0000 + counter`,
    NotificationEvent = struct v0 `{PID, u32 type, u64, u64, String, u64}`, no MapParam).
    Event types: 3001 Participate, 3007 Disconnected, 3008 EndParticipation,
    4000 OwnershipChanged, 109000 GatheringUnregistered, 110000 HostChanged,
    116000 RefereeRoundStarted, 122000 AddedToGathering. We have no outbound DATA path.
22. **NAT 0x03.0x03 RequestProbeInitiationExt** must push InitiateProbe to each named
    target carrying the caller's station, so both NATs open together. We ack and drop.
23. **NAT classification + lobby gating.** The measured server buckets peers from
    ReportNATProperties (`natMap`/`natFilter`) into open/moderate/strict and refuses to
    seat strict×strict and strict×moderate together (**2618-0510**). Absent here.
24. **Host migration.** MatchMaking 0x24 MigrateGatheringOwnershipV1, 0x2B
    UpdateGatheringOwnership (→bool), 0x2C MigrateGatheringOwnership, plus 0x28/0x2A
    UpdateSessionHost. All NotImplemented for us.
25. **Room / arena codes.** 0x6D.0x44 create + 0x45 resolve (MK8D), 0x6D.0x37 + 0x38
    (SSBU, same structures). All NotImplemented.
26. **0x6D.0x3A is two methods on one number.** SSBU pre-AutoMatchmake probe expects a
    bare `u32 = 2`; MK8D sends `u32 count + count×u32` (body exactly 4+4n) and expects a
    tournament list. The measured server disambiguates by body length. NotImplemented for us.
27. **Tournaments**: 0x6D.0x39 search, 0x3F join, plus create/delete.
28. **0x6D.0x41 CustomFriendsQuery** → empty list. NotImplemented for us.
29. **Ranking 0x16 CommonDataByPIDs** — ordered one entry per requested PID, empty entry
    for unknowns, never an error. The measured server records the unanswered method as the wall for
    MK8D worldwide mode (**2618-0006**).
30. **Ranking 0x10 / 0x12** competition ranking and info.
31. **SecureConnection 0x07 ReplaceURL** must *store* the re-reported CID-bearing LAN
    station (dedupe by address, new wins). Dropping it breaks friend-join probes. We
    answer empty success and discard.
32. **Login-name → PID map.** SMM2 asks DataStore for profiles by NSA id (the login
    username), not by our internal PID. Without the table it never finds anything.
33. **Auth recall is not persisted.** The measured server writes the IP→PID table to disk
    (debounced, 0600, atomic rename) because a restart cost 40 refusals mid-session on MK8D.
    Ours is memory-only.
34. **Strikers init table.** The measured server answers 0x6E/0x73/0x78/0x79/0x7A per method with
    one of three shapes (void ack / zero-struct / empty list) from a reversed decoder
    table: void on 0x7A.13 and 0x6E.14/15; `u64 0` on 0x78.1; struct on 0x6E.10/11,
    0x7A.9/11/12/14, 0x78.2/3/4/7/12/14; empty list on 0x6E.7/9 and 0x78.9/16/17.
    We answer NotImplemented across 0x78/0x79/0x7A for Strikers.
35. **Referee stats family** 6–14 (GetRoundParticipants, GetNotSummarizedRound, GetRound,
    GetStatsPrimary/Primaries/All, Create/GetOrCreate/ResetStats).
36. **Ranking (0x70) is not registered for 8 of our titles** — see conflict 38.

---

## Conflicts: `docs/legacy-parity.md` contradicts the measured server

Recorded, not resolved — per the clean-room policy two facts that disagree are a
finding, not a blocker.

37. **SMB35 secure PID.** We set `257049437023956657` and call it "the retail
    secure-principal constant". Every measured title, SMB35 included, uses
    `securePID = 2`. Internally consistent for us (same value on both sides), but the
    station URL `PID=` we hand out differs from the measured server's.
38. **"No Ranking" claims are wrong.** The parity table says MPS, Golf, ARMS, LM3,
    Minecraft, SMO and SSBU have no Ranking, and lists Strikers without it. The
    measured server registers Ranking (0x70) for **all** of them, and MPS additionally
    registers a DataStore stub the table says it deliberately lacks.
39. **0x6D.0x3C.** Table says "S2 measured: the measured server answered an empty list".
    The measured server implements it for real and documents the empty answer as the
    friend-join failure. See P0 #6.
40. **0x6D.0x33 FindByParticipant.** Measured gates real results behind a per-title
    flag enabled **only for ACNH**; everywhere else it deliberately returns an empty
    list, which SSBU's startup depends on. We always return real results — a likely
    SSBU regression. Our 300-PID cap has no counterpart; the measured server instead has an
    ACNH flag making an *empty* PID list mean "any open session", answers with the
    **host's** PID rather than the requested one, and strips `UserPassword` (keeping
    the session key, which ACNH needs).
41. **Station scheme.** MPS, Golf, ARMS and SMO configure `prudps`, not `prudp`.
    `stationurl.go` hardcodes `prudp` for every title.
42. **Per-title Pia shape.** Public station `type` is 0x03 with no `Pa` for MPS, Golf,
    ARMS, SMB35, Splatoon 2 and LM3; 0x0B with `Pa` for ACNH, MK8D, MTA, Strikers,
    Minecraft, SMM2, SMO, SSBU. We send neither parameter.
43. **DataStore stub shape is per title, not generic.** MPS: 0x73.8 → NotFound,
    everything else → `u32 0`. Splatoon 2: **every** method → empty success, 0x73.8
    included. SSBU and Strikers: 0x73.8 → NotFound, rest → `u32 0`. Our single `DSStub`
    mixes empty-success, empty-list and minted u64 data IDs and matches none of them.

---

## Per-title protocol registry delta

| Title | The measured server registers | Missing here |
|---|---|---|
| MPS 22110 | SC, MMX, MM, MMExt, NAT, UT, **DS stub**, **Ranking** | DataStore, Ranking |
| Golf 22130 | + **Ranking** | Ranking |
| ARMS 22140 | + **Ranking** | Ranking |
| MK8D 22150 | SC, MMX, MM, MMExt, NAT, Ranking, UT | — (but Ranking method set is wrong, #19) |
| SMB35 22160 | SC, MMX, MM, MMExt, Referee, MessageDelivery, Ranking2, UT — **no NAT** | we additionally register NAT (harmless) |
| MTA 22170 | Ranking, DS replay | — |
| ACNH 22180 | SC, MMX, MM, MMExt, NAT, DS, UT, Ranking | — |
| LM3 22190 | + **Ranking**, 0x79 | Ranking |
| Strikers 22300 | SC, MM, MMExt, MMX, NAT, **Ranking**, 0x79, 0x73, 0x6E, 0x7A, 0x78 | Ranking, 0x79, 0x7A, 0x78 (+ the init table, #34) |
| Minecraft 22310 | + **Ranking** | Ranking |
| S2 22320 | Ranking, DS | — |
| SMM2 22330 | Ranking, DS(SMM2), 0x6E replay | — |
| SMO 22340 | SC, UT, **Ranking**, DS | Ranking |
| SSBU 22350 | SC, MMX, MM, MMExt, NAT, **Ranking**, 0x73+0x6E replay | Ranking |
| Clubhouse 22870 | no local measured server | n/a |

Ours additionally registers Notification (0x0E) and Messaging (0x17) for every
title. The measured server registers neither: 0x0E is **server→client only** there
(see #21) and 0x17 does not exist in it. Harmless, but not parity.

Measured NEX versions, for the record (we track none): MPS 40605, Golf 40605,
ARMS 40305, MTA 40305, SMB35 40600, everything else 40000. Session key length is
32 for every title including SMB35.
