# earlier parity — matching the working reference servers title for title

Method (same as the MK8D live server): take each title's measured behaviour against
the working reference servers (Reference), compare it against what this
lineage serves, and close the gap so the answers match like for like. Every
"reference" fact below comes from the per-title provenance/design records in
`servers/nex/titles/<name>/docs/` — observations of games running and getting
online against the reference, not guesses. Wire shapes follow the public NEX
protocol tables (Pretendo nex-protocols-go, AGPL-3.0, facts only) and the
Switch byte-stream profile in SPEC.md.

Implementation: `protocols/` (one Env per title; dispatcher in
`protocols.go`), wired per title in `cmd/switch-nexd/main.go`.

## Shared behaviour (every title)

| What the games do against the reference | What switch-nexd answers |
|---|---|
| Login: TicketGranting method 2 (all NEX 4.0-4.3 titles) or method 6 (4.4+ titles — the measured live path on MPS/SMB35/Golf-era SDKs) | Both 0x0A.0x02 and 0x0A.0x06 answered with the byte-verified `ValidateAndRequestTicketResult` (result.go), v1 ticket, per-title source key size |
| Secure stage opens ticketless; SecureConnection::RegisterEx (0x0B.4) or plain Register (0x0B.1) on pre-5.19-Pia titles | 0x01/0x04 answered with (QResult, connection id, public station URL); 0x03 RequestURLs with the secure URL list; 0x05 TestConnectivity empty (MTA measured 23 calls in 6 h) |
| Utility::GetIntegerSettings (0x6E.7) called right after Register; an unparseable answer drops the connection ~1 s later | `u32 count`-led map (empty maps are a real answer); per-title tables override |
| Utility unique-ID methods (0x6E.1/0x5) during SSBU-style bring-up | u64 counter / associated id |
| Utility::AcquireTagId (0x6E.9) + UpdateCurrentUser (0x6E.10) on NPLN-adjacent titles (S2 measured 0-byte answer) | u64 PID-derived / empty success |
| NotificationService::ProcessNotificationEvent (0x0E.1) | empty success |
| Matchmaking family (0x15 / 0x32 / 0x6D): create → open participation → searches find it → join → P2P | Real in-memory sessions: GID assignment, owner/host stamping, participants, attribute/buffer/progress updates, flag-gated partial updates (0x2C), Browse lists, GetSimplePlayingSession/GetPlayingSession, FindByGatheringID(Detail)/ByOwner/ByParticipant |
| Unknown methods | Core::NotImplemented (0x00010002) + logged — the game tells us the backlog |

Per-title matchmaking rules measured on the reference and implemented here:

- **MPS** — `FindMatchmakeSessionByParticipant` caps at 300 PIDs (0x6D.0x33);
  `FindMatchmakeSessionByGatheringIDDetail` is answerable by anyone holding
  the gathering ID, so SessionKey/UserPassword are stripped from its answer;
  joiners poll that method ~1×/s (no notification dependency).
- **MTA** — sessions arrive closed and two searchers deadlock, so every
  session is forced `OpenParticipation=true` at create (the diagnosed fix).
- **Clubhouse Games 51** — sessions arrive closed too (same forced-open fix
  as MTA). Create-time normalisation measured on the reference: max
  participants 0→12, host counted as participant 1, fresh 32-byte session
  key, start time stamped, state 1 (active), password flags cleared,
  attributes padded to 6, `@SR`=true / `@GIR`=3 in the matchmake param.
  Join answers carry the full session with the **pre-join** participation
  count — the client builds its P2P mesh from that number. DataStore is
  called during online account setup, before any matchmaking: the measured
  "nothing saved yet" answer to GetPersistenceInfos (0x1E) is an empty
  persistence-info list plus one NotFound (0x80690004) per requested slot,
  which makes the game create a fresh profile; GetMeta is NotFound; every
  other method takes a bare empty list (this title never posts).
- **ACNH** — `0x6D.0x33` must carry the **full session including the 32-byte
  session key** (bare sessions stall island visits at 2618-0502); the Dodo
  Code rides `UpdateMatchmakeSessionPart` (0x6D.0x2C) with flags 0x4002 and is
  stored on the session.
- **S2** — title method 0x6D.0x3C answers an empty list (measured).

## Per-title protocol sets (reference-measured → implemented)

| Title (base) | Reference protocol set | Our state |
|---|---|---|
| Mario Party Superstars (22110) | SC 0x0B, MM 0x15, MMX 0x6D, MME 0x32, NAT 0x03, UT 0x6E (+`MPS_INTEGER_SETTINGS`), DS 0x73 (empty-list stub), Ranking 0x70; `prudps`, legacy Pia | **implemented** incl. settings tables + detail-find sanitisation |
| Mario Golf: Super Rush (22130) | MM, MMX, MME, NAT, UT, Ranking; `prudps`, legacy Pia | **implemented** |
| ARMS (22140) | MM, MMX, MME, NAT, UT, Ranking, DS 0x73; `prudps`, legacy Pia | **implemented**; DataStore at stub level (see deviations) |
| Mario Kart 8 Deluxe (22150) | MM, MMX, MME, NAT, UT, Ranking 0x70; Pia 5.19 | **implemented** (live; plus `cmd/mk8d`), incl. room codes and tournaments |
| Super Mario Bros. 35 (22160) | MM, MMX, MME, UT (17-entry index-0 settings table), Ranking2 0x7A, Referee 0x78, MessageDelivery 0x1B; **no NAT, no Ranking 0x70**; 16-byte source key; legacy Pia | **implemented** behaviour-for-behaviour incl. round store, raw StartRoundParam echo, settings table |
| Mario Tennis Aces (22170) | MM (+open-participation fix, 0x33), MMX, MME, NAT, UT, Ranking, DS; NEX 4.3.5 + Pia 5.9.6 (SDK banners, update 3.1.1), served the 5.19 station shape as the reference did | **implemented**; create-time normalisation since v0.3.28 |
| Animal Crossing: New Horizons (22180) | MM + 0x33/0x2C, MMX, MME, NAT, UT, Ranking, DS; Pia 5.19 | **implemented**; the one title with find-by-participant serving real results |
| Luigi's Mansion 3 (22190) | MM, MMX, MME, NAT, UT, Ranking, presence 0x79; legacy Pia | **implemented**: 0x79 stub |
| Mario Strikers: Battle League (22300) | MM, MMX, MME, NAT, UT, Ranking, DS, plus a bring-up shape table on 0x6E/0x73/0x78/0x79/0x7A; Pia 5.19 | **implemented** incl. the bring-up table (`protocols/strikers.go`) |
| Minecraft (22310) | MM, MMX, MME, NAT, UT, Ranking | **implemented**; SNI `g28c77600-lp1.s.n.srv.nintendo.net` (from the emulator's own DNS redirect log, 2026-09-18 — recorded nowhere else public; the earlier server's console agent uses a `g2*` hosts wildcard instead) |
| Splatoon 2 (22320) | MM (+0x3C), MMX, MME, NAT, UT (+2/5), Ranking, DS (**empty success for every method, GetMeta included**); legacy Pia | **implemented** |
| Super Mario Maker 2 (22330) | MM, MMX, MME, NAT, UT, Ranking, DS + title methods | **implemented**: GetUsers answers (empty UserInfo list, per-PID NotFound) — see deviation 1 |
| Super Mario Odyssey (22340) | SC, UT, Ranking, DS — no matchmaking at all; balloon methods 0x73.0x2F-0x35 NotImplemented; `prudps` | **implemented** incl. balloon NotImplemented stance |
| Super Smash Bros. Ultimate (22350) | MM, MMX, MME, NAT, UT, Ranking, DS (empty-list stub); arena codes 0x37/0x38; the 0x3A pre-match probe; Pia 5.19 | **implemented** |
| Clubhouse Games 51 (22870) | MM, MMX, MME, NAT, UT, Ranking, DS (account-setup stub shapes); NEX 4.4.0, access key `8c3661ff`, secure PID 2, 32-byte session key, game server 2035BB00 | **implemented** (see the per-title rules above); new title, no nex-go predecessor |
| Super Mario 3D World + Bowser's Fury (22370) | MM, MMX, MME, NAT, UT, Ranking; Participate pushes delayed 100 ms; joins echo the pre-join count; NEX 4.6.4, `prudp` | **registered, not console-verified**: `ParticipateDelay` + `JoinEchoSession`. Game server 2B306A00 and key from our own dump |
| Kirby's Dream Buffet (22380) | no reference yet (the earlier server's repo is empty) | **registered, not console-verified**: MM, DS (empty-list), Ranking2, the components its binary links. Game server 25374500, key `6wVacwwe` from our own dump |

Corrected 2026-09-16 against the reference sources. The previous version of this
table claimed MPS, Golf, ARMS, LM3, Minecraft, SMO, SSBU and Strikers had **no
Ranking**, and that MPS deliberately had no DataStore. Both were wrong: the
reference registers Ranking 0x70 for every one of them. It also recorded SMB35's
secure PID as a retail constant; every reference title, SMB35 included, uses 2.
See `docs/legacy-gap-report.md` conflicts 37-43.

Reference NEX versions, for the record: MPS 40605, Golf 40605, ARMS 40305,
MTA 40305, SMB35 40600, every other title 40000. Session key length is 32
everywhere, SMB35 included; the 16 bytes that title carries is its SOURCE key.

Not served by this lineage: **MHGU (22120)** — kerberos v0 tickets and a raw
PRUDP-over-UDP *secure* stage, 3DS-lineage code under an AArch64 shim. Its
*auth* stage is wss like everything here, which is why it sits behind the same
Traefik SNI route; the secure half is what this lineage cannot serve. Stays on
nex-go (MIGRATION.md), where it shares its NEX code and access key with
Pretendo's Monster Hunter XX server.

## Known deviations (documented, deliberate)

1. **DataStore is a stub, not a store**: shapes match the reference answers
   the games were measured working against (NotFound on GetMeta, real u64
   data IDs from PostMetaBinary, empty lists elsewhere). The Postgres/S3
   store from `servers/nex` (ARMS/ACNH/S2/SMM2/SMO generation) is the
   port-ready upgrade when a title needs real object persistence.
2. **Server-initiated pushes: implemented 2026-09-16.** `prudplite` originates
   RMC requests on a client's own connection (`Connection.SendRMC`), and the
   secure endpoint is bound to each title's `Env`. Participation (3001),
   disconnect (3007), end-participation (3008), ownership (4000), gathering
   teardown (109000), host change (110000), referee round-started (116000) and
   added-to-gathering (122000) are all pushed, as is the NAT `InitiateProbe`
   relay. The SMB35 Eagle handoff (200000) is still not sent: the reference
   measured its push never honoured by the client, so there is nothing to
   reproduce.
3. **SMB35 listener shape**: the reference used a single listener with the
   secure stage on stream 2 (a nex-go limitation workaround). Here the secure
   stage is its own port (base+1) and the station URL carries `sid=1`, which
   the MK8D-verified station URL shape already covers (SPEC.md).
4. **Access keys are now set for every title** and the CONNECT Lite-signature
   check is enforced wherever one is present: `e915510f` MPS, `1cb8027c` Golf,
   `b6b34c51` ARMS, `09c1c475` MK8D, `0a69c592` SMB35, `29f267b9` MTA,
   `v43a10em` ACNH, `aab95246` LM3, `MAGNI5jG` Strikers, `f4983d87` Minecraft,
   `4eb18d39` S2, `fdf6617f` SMM2, `afef0ecf` SMO, `9587602b` SSBU,
   `8c3661ff` Clubhouse. They are public constants baked into the game
   binaries, not secrets. PRUDPLite DATA frames are unsigned by protocol
   design; TLS carries the channel.
5. **One unverified identity still stands in for accounts**: the PID derives
   from the login username, which is accepted without proof, and nx-baas never
   sees the player. This is the largest open item on the lineage and is written
   up separately in `docs/identity-gap.md` — it is not a parity gap against the
   reference (which binds to nx-baas) but a gap against our own `servers/nex`.

## Clubhouse Games 51 — fact provenance (2026-09-16)

First title whose reference facts were taken directly from a public
earlier network repository (clean-room: facts only, rewritten here —
`docs/clean-room-policy.md`):

- Title ID 010047700D540000, game server ID 2035BB00 (SNI
  `g2035bb00-lp1.s.n.srv.nintendo.net`), access key `8c3661ff`, NEX 4.4.0
  confirmed by a live 2-player match (their README's 40605 default is marked
  unconfirmed by them), secure PID 2, 32-byte session key — their README and
  published constants.
- Secure protocol set (0x0B, 0x15, 0x32, 0x6D, 0x03, 0x6E, 0x70, 0x73) and
  the DataStore-before-matchmaking account-setup flow with the
  GetPersistenceInfos empty+NotFound answer — their README ("Online account
  setup … shows Communication error") and published stub behaviour.
- Sessions arriving closed until a later call; create-time normalisation
  (max 12, count 1, 32-byte key, state 1, password flags cleared, attributes
  padded to 6, `@SR`/`@GIR` params); join answers echoing the full session
  with the pre-join count; partial updates (0x2C) writing through — their
  published per-title configuration for this game (open-participation flag,
  join-count and partial-update knobs) and the documented reasoning behind
  each.
- Unmapped here on purpose: their station-URL ordering knob (our matchmaking
  answers carry no station URLs — clients use the secure stage URL, as every
  other title on this lineage) and their account-gating/NSA integration
  (ours stays on the PID-from-username stand-in, deviation 5).
