# Changelog — switch-nex

> **Current Switch client (2026-10-13): Harbor**, at
> `~/REPOS/Openpak/emulators/harbor`. Harbor is an independent
> OpenPak project based on Eden. Citron, Ryujinx (desktop and Android), and
> the former OpenPak Eden checkouts have been removed. References to those
> forks below record historical evidence only: do not run their commands,
> reinstall them, or select them for current work. Use Harbor's README and
> current handoff for builds and testing; old results do not establish Harbor parity.

## Unreleased — Duck relay investigation

- Resolve shared relay source rewriting through production host networking;
  migrate and reverify all 18 NPLN API paths without changing coturn or binaries.
  External exact-destination/STUN mapping checks prove bidirectional UDP
  forwarding and reject an unbound third source.
- Add a Duck-only operator probe mode that advertises an inactive relay socket
  without binding reported addresses. Retail relay use and crash resolution
  remain unverified; the existing permissive TLS fake is not real-service proof.
  Evidence and rollback locations are recorded in docs/duck-game.md.

## Unreleased — shared participant endpoint replies

- MatchMaking 0x15/0x10 now accepts a gathering ID and returns a flat
  list of StationURL strings, as specified by the NEX IDL. The old nested
  lists were not RVConnectionData and were unparseable by a standard client.
- MatchMakingExt 0x32/0x04 now accepts 32-bit gathering IDs and returns
  GatheringURLs structures instead of interpreting inputs as player IDs.
- Initial public station fallback preserves the reported game port instead
  of substituting the unrelated TLS/TCP source port. Client-reported public
  mappings and per-console LAN endpoints remain intact.
- StationURL updates retain one dictionary entry when switching between
  string and integer representations; parsed fields no longer duplicate.
- Regression coverage exercises station identity/ports across all 29 title
  profiles and reads flat two-console endpoint replies over real TLS for
  Castle Crashers, Nobody Saves the World and PICO PARK.
- Real-game household acceptance remains pending. This change does not
  establish external UDP mappings or implement a relay.


## 2026-10-12 — v0.3.45: joins answer per the NEX IDL

- The plain (0x07) and Ex (0x1E) joins answer the session key as a Buffer.
  The NEX IDL reads both replies as session_key (NintendoClients'
  IDL-generated matchmaking client); only the 0x27 WithParam join takes the
  full session. PICO PARK joins with 0x1E, which explains every failed run:
  the bare bool has no length prefix to read a buffer from and the session
  parses as a garbage length — both rejected game-side in under two seconds
  with no follow-up, whatever the notification pairing. This is the first
  profile change that predicts a *different* console behavior rather than
  another identical failure.
- Clubhouse join test now pins the key buffer for 0x07/0x1E; new
  end-to-end pico test walks create/browse/0x1E and checks the returned
  key equals the created session key. Full suite and vet pass. Awaiting
  the console verdict: station fetch and P2P should follow the join.

- The shape/presence matrix so far: echo+recap, bool+recap, echo+default and
  echo+silent all fail identically (RPC accepted, game-side rejection under
  two seconds, no station fetch). bool+silent is the remaining cell: if the
  NEX IDL shape is right and the pushes were the poison, this proceeds.
- First-batch test expects the bare bool for pico-park again. Full suite
  and vet pass. Awaiting the console verdict.

- PICO PARK seats explicit joiners without pushing anything: neither the
  recap nor the default pairing survived console runs, and the pushes are
  the last join-time element never isolated — every failure looks identical
  (RPC accepted, game-side rejection in under two seconds, no station fetch
  ever) across both reply shapes and both pairings. If the joiner proceeds
  now, the pushes were the poison and the pairing gets bisected next; if it
  fails identically, pushes are innocent and the reply content is next.
- New `JoinMutePushes` Env flag with a regression test; `-dump` lists it.
  Full suite and vet pass.

## 2026-10-12 — v0.3.42: Heroes of Hammerwatch initial profile

- Registered Heroes of Hammerwatch — Ultimate Edition
  (`heroes-of-hammerwatch`) on 23110/23111/23112 with game server
  `2075E100` and access key `1203b5eb`. The pair is adjacent-config
  evidence in the updated main (ID built at 0x7c7d44/0x7c7d48, key
  literal at 0xa615b3); the derived SNI is DNS-confirmed by the
  2026-10-12 retail trace (`g2075e100-lp1` dialled 21:58:02 UTC,
  websocket upgrade, catch-all 404 while unrouted), and the game uses
  the websocket carriage the server already speaks per connection.
  CONNECT signatures still need the first routed boot. Ticketed
  `prudps` stations, modern Pia shape (Pia 5.24.0: type 0x0B with Pa),
  independent matchmaking, normalized creation, full-session joins and
  participant recaps; only the linked
  MatchmakeExtension/NATTraversal/SecureConnection set is registered
  (the Ranking hits are a LAN job name and DDL boilerplate).
  Provenance and the deployment bundle:
  `docs/heroes-of-hammerwatch.md`. No version exception needed: the
  installed v524288 (`00080000`) equals the gated latest.
- The same image carries the Duck Game scaffold and the Full Metal
  Furies profile (both still undeployed; their sections below stand).
- TLS checks cover both login methods, distinct identities behind one
  address, modern station registration, browsing, joining, participant
  notifications and the explicit-join recap. These are not retail
  gameplay results.

## Unreleased — Full Metal Furies initial profile (console-verified)

- Registered Full Metal Furies (`full-metal-furies`) on
  23120/23121/23122 with game server `2C82EE00` and access key
  `c093ff57`. The pair was adjacent-config evidence in the available
  (outdated, content 131072) main — ID built into w8 at 0x2d95c/0x2d960
  and stored at 0x2d964, key literal at rodata 0x40a9f47 loaded by
  adrp/add at 0x2d968/0x2d96c — and is now console-measured (below).
  Ticketed `prudps` stations, legacy Pia shape (Pia 5.11.3:
  type 0x03 with no Pa), independent matchmaking, normalized creation,
  full-session joins and participant recaps; only the linked
  MatchmakeExtension/NATTraversal/SecureConnection set is registered
  (the `NEX_R2`/`NEX_RK` banners have no protocol clients behind them;
  the Ranking hit is a LAN job name). Provenance and deployment:
  `docs/full-metal-furies.md`. Deployed 2026-10-12 ~22:15 UTC as
  `openpak-switch-full-metal-furies` (local arm64 binary mount,
  Traefik SNI `g2c82ee00-lp1`, secure TCP 23121 admitted and
  persisted).
- Console-verified 2026-10-13: Harbor and Switch logged in proved
  (title `010079300AD54000`, version `00050000`), created/joined gid
  914033 and completed the NAT punch (`rtt=102ms`); the owner played
  the game between the two, with a clean leave/idle teardown. A
  sustained full co-op run and leave/rejoin are still unverified.

## 2026-10-12 — v0.3.41: Pico Park default join pairing

- PICO PARK drops the recapped join for the default pairing (3001 about the
  joiner to the room, 122000 to the joiner). The recap was applied from the
  pattern, never console-measured — and it is the one join-time element
  constant across every failed console run: with either reply shape the join
  is accepted at the RPC layer and rejected game-side in under two seconds,
  with no station fetch ever following. Static RE of the updated client
  (0x6D call sites, notification dispatcher range, NEX spec reply shapes)
  constrained everything else; the pairing is what a clean two-console run
  now decides.
- TestRoomJoinRecapsTheHost no longer lists pico-park; the first-batch
  browse/join regression still covers its echoed session. Full suite and vet
  pass.

## 2026-10-12 — v0.3.40: Pico Park back on session-echo joins

- PICO PARK's boolean join reply (v0.3.36) was accepted at the RPC layer in
  four console runs but the game never followed up: no station fetch, no P2P,
  "failed to connect to the lobby" on screen, and the abandoned seat lingering
  as a ghost owner. A bare bool carries no session key and no stations, so the
  joiner has nothing to start P2P with; the 3x retry that motivated the bool
  predates the gid-only participants-URLs fix and may have been the station
  fetch failing rather than the echo. Joins answer the full session again, as
  every other P2P title does.
- The join regressions cover the echoed session for all three first-batch
  profiles once more. Full suite and vet pass. Awaiting one clean two-console
  run: join, station fetch, P2P.

## Unreleased — Duck Game scaffold (trace pending)

- Registered Duck Game (`duck-game`) on 23100/23101/23102 with an
  intentionally empty access key: the game server ID and key are still
  unknown (no ASCII/UTF-16 key literal and no same-call login pair in the
  117 MB updated main; the auth hostname formats `g%08x` at runtime), so
  CONNECT signatures go unverified and no route, quadlet or catalogue
  entry exists yet. Ticketed `prudps` stations, legacy Pia shape
  (Pia 5.12.1), independent matchmaking, normalized creation,
  full-session joins and participant recaps; only the linked
  MatchmakeExtension/NATTraversal/SecureConnection set is registered.
  Provenance and the reserved (not deployed) bundle:   `docs/duck-game.md`.
- TLS checks cover both login methods, distinct identities behind one
  address, legacy station registration, browsing, joining, participant
  notifications and the explicit-join recap. These are not retail
  gameplay results.
- Deployed 2026-10-12: the trace recorded console-measured game server
  `25E42700` (`g25e42700-lp1.s.n.srv.nintendo.net` at 21:12:43 UTC, one
  second after Duck Game's aauth). Traefik SNI route, `duck-game`
  container (local arm64 binary mount), host firewall for secure 23101
  and public login/register probes pass. The access key is still unknown;
  retail gameplay validation is pending.

## 2026-10-12 — v0.3.38: one lobby at a time

- A console that creates or joins a gathering now leaves every other room it
  is still seated in. Game-side a console is in one lobby; server-side the old
  seat survived — a failed join the game never backs out of, a new room hosted
  while the old one still lists its host — and the abandoned room stayed
  browsable under a ghost owner (PICO PARK 2026-10-12: gid 870838 outlived its
  players while both sat in 870839).
- Seating moves through the shared leave helper, so counts, notifications,
  ownership and empty-room deletion stay consistent on the move.
- leave_test.go replays the trace (host leaves, ghost hosts anew, join
  follows) and the mirrored join path. Full suite, race detector and vet
  pass.

## Unreleased — Monopoly for Nintendo Switch

- Registered Monopoly for Nintendo Switch (`monopoly`) on 23090/23091/23092
  using the same-call ID/key pair from the owner's updated client
  (`2E041D00` / `65c92799` at key load `0x560370`, call `0x560394`,
  NEX 4.3.1 / Pia 5.9.0). Ticketed `prudps` stations, legacy Pia shape,
  independent matchmaking, normalized creation, full-session joins and
  participant recaps; Ranking, Ranking2, MatchmakeReferee and an empty-list
  DataStore are registered because the binary links those modules.
  Ubisoft's own HTTPS (`public-ubiservices.ubi.com`) stays out of scope.
  Profile choices still require a game trace.
- Added a Traefik routing fragment (`deploy/nex-monopoly.yml`) and
  per-title provenance notes (`docs/monopoly.md`). The existing quadlet
  generator picks up the new unit automatically.
- TLS checks cover both login methods, distinct identities behind one
  address, legacy station registration, browsing, joining, participant
  notifications and Ranking2 common-data. These are not retail gameplay
  results.

## 2026-10-12 — v0.3.37: leave paths keep the full bookkeeping

- Participate/CancelParticipation (0x15.0x0B/0x0C) only flipped the roster map:
  Cancel left the count up with nobody told and ownership never moving, while
  Participate added an uncounted seat. Both now share the EndParticipation
  bookkeeping (count, notify, ownership migration, delete-when-empty), and a
  duplicate leave is a no-op instead of a second decrement.
- EndParticipation and connection teardown delete the gathering once the last
  participant is gone, whatever path they left by; ownership pointing at an
  already-departed player is repaired while someone remains to hand to.
- Console-observed on PICO PARK (gid 870055 stayed browsable after its host
  left): the host's exit migrated ownership to a joiner whose game had already
  failed the join, so the room outlived everyone actually in it. Rooms now tear
  down reliably when the last server-side participant leaves; a failed join
  that never leaves server-side still holds its seat until that console
  disconnects.
- New leave_test.go pins the Cancel-then-EndParticipation exit sequence and
  the all-disconnected teardown. Full race suite and vet pass.

- Pico Park's first console trace showed JoinMatchmakeSessionEx succeeding at
  the RPC layer but being retried three times, with no subsequent request for
  participant stations. Its full-session join reply was an unverified profile
  assumption; Pico Park now gets the standard boolean result while keeping its
  participant recap notifications.
- Repeated explicit joins no longer inflate participation count. A fresh
  two-console gameplay run is still needed to confirm the handoff.
- The explicit-join regression now checks Pico Park's boolean reply while
  continuing to verify the participant recap notifications.
- The browse/join regression checks the boolean result and continues through
  participant station lookup.

## 2026-10-12 — v0.3.33: Castle Crashers gid-only participants URLs

- Castle Crashers Remastered calls MatchMaking GetParticipantsURLs (0x15/0x10)
  with only the gathering ID and no PID list (4-byte body). The server answered
  Core::InvalidArgument and the console dropped with "lost connection to
  Nintendo Switch Online" right after creating its room. A missing or empty PID
  list now answers every current participant's stations instead of refusing.
- Regression test replays the retail call shape (gid only, req=4B) and fails
  with 0x1000a without the fix. Full race suite and vet pass. Gameplay
  verification on console/Harbor remains pending.

## 2026-10-12 — v0.3.32: first new NEX batch

- Registered Castle Crashers Remastered, Nobody Saves the World and PICO PARK
  on 23060/23070/23080 using ID/key pairs traced in the owner's updated clients.
  Each gets ticketed secure stations, independent matchmaking, normalized
  creation, full-session joins and participant recaps. Castle Crashers also
  gets Ranking2. Profile choices still require a game trace.
- Added a Traefik routing fragment and per-title deployment/provenance notes.
  The existing quadlet generator picks up the three new units automatically.
- TLS checks cover both login methods, distinct identities behind one address,
  station registration, browsing, joining, participant notifications and
  Castle Crashers' Ranking2 endpoint. These are not retail gameplay results.
- Deployed: all three new quadlets active on the published 0.3.32 image; auth
  SNI routes and host firewall installed. External CA-verified probes pass both
  login methods, ticketed secure connects and Register, plus Castle Crashers'
  Ranking2 call. Internal health checks pass; voice URLs are loaded and catalogue
  state is alpha/NEX. Actual game-client acceptance remains pending.

## 2026-10-12 — v0.3.31: SMB35 accepts the winner's end-of-round report

- The owner confirmed Switch/Harbor gameplay after v0.3.30, but whichever
  device placed first received `2306-1807` at the end of the round. Production
  logs show the eliminated player's EndRound succeeded and deleted the shared
  round; the winner's later EndRound failed with MatchmakeReferee::RoundNotFound
  (`0x006F0007`). Eagle reports now retain the shared round so all participants,
  partial reports and retries can complete. The existing one-hour pruning on
  new round creation limits retention. Other titles keep their lifecycle.
- TLS NEX regression tests exercise both finishing orders, partial/final/no-report
  completion, retries, late parameter lookup and genuine unknown-round errors.
  The test reproduces the failure before the fix.
- Full-room tests cover 35 authenticated WebSocket players, membership bitmaps
  and notices, StartMatch broadcast, shared counters and selective updates across
  byte boundaries. Referee tests accept all 35 players' partial/final/retry
  reports with the winner last, and verify old rounds still expire.
- Deployed 09:10 UTC. The running image matches the published `0.3.31` digest;
  NEX and CA-verified Eagle health pass. The public two-client NEX/Eagle probe
  accepts both 81-byte EndRound reports in both finishing orders and retains
  the shared parameters. Retail end-of-round confirmation remains pending.

## 2026-10-12 — v0.3.30: SMB35 gets its Eagle gameplay relay and handoff

- Super Mario Bros. 35 could match players and relay lobby state but never leave
  the countdown: the Eagle gameplay listener and its handoff were absent.
  The title now sends each participant a revision-1 notification with the room's
  relay URL and a signed, player-specific ticket. A separate TLS/WebSocket
  listener on 22163 shares the NEX gathering's lifetime and admission state.
- Eagle v2.0.4 login, fragmented tickets, readiness and node notices, ping/pong,
  addressed and broadcast game RPC routing, and the 32 shared counter RPCs are
  implemented. Departures revoke admission; gathering destruction closes the
  relay room. Session counts include the creator, detailed participants carry
  their protocol names, and the Eagle referee names the round ID in its start
  notification to all participants, including the initiator.
- Two-client WebSocket tests cover login, shared counters, JoinMatch/StartMatch
  and game-packet forwarding, room isolation, forged/expired/wrong-room tickets,
  revocation and cleanup. TLS NEX tests cover per-player handoffs, session counts
  and the referee notification. The full suite passes with the race detector;
  retail Switch/Harbor gameplay still needs the owner's retest.
- Protocol and license sources: [Eagle provenance](eagle/provenance.md).
  `github.com/coder/websocket` v1.8.15 is pinned with its ISC notice in the image.
- Deployed 08:46 UTC; CA-verified external health and a live synthetic two-client
  NEX-to-Eagle probe pass (handoffs, relay login/readiness, shared counters and
  StartMatch broadcast). Retail-game acceptance still requires the owner's test.

## 2026-10-12 — v0.3.29: Minecraft can resolve a friend's hosted world

- Minecraft: Nintendo Switch Edition discovered a friend's world but selecting
  it failed with a network error in both hosting directions. The joiner's
  FindBySingleID (`0x15/0x15`) always received `RendezVous::SessionVoid`
  (`0x80030073`), even with the host's gathering alive: the handler was an
  unconditional error stub. It now looks up the gathering and returns a true
  result plus a length-framed `MatchmakeSession` holder with the current world
  data. Missing gatherings still fail; malformed requests answer InvalidArgument.
- Response layout follows the public [Match Making protocol, method 21](https://github.com/kinnay/NintendoClients/wiki/Match-Making-Protocol#21-findbysingleid).
  Protocol and TLS integration tests cover live-world lookup, its holder framing,
  current application data, missing worlds and malformed input.
- **Owner-verified after deployment:** a retail Switch hosts with Harbor joining,
  and Harbor hosts with the Switch joining; both directions work (2026-10-12).

## 2026-10-11 — v0.3.28: Mario Tennis Aces sessions get the measured server's create-time shape

- **Mario Tennis Aces** online Free Play: matchmaking, the NAT punch (16 ms and 128 ms) and the
  Pia mesh all completed, the host closed participation, and then both consoles sat on the match
  loading screen indefinitely with no server call and no error (2026-10-11, a real Switch and
  Harbor on the ROG 3, both roles, gid 741123 and 741124). Tennis was one of five titles without
  `FinalizeCreateSession` (with Mario Party Superstars, Super Mario Bros. 35, Strikers and
  Odyssey), and none of the five has been seen playing peer-to-peer here; every title that does
  play has it. Its sessions therefore kept the creating client's own count and key, and a join
  only added one to that count. The measured server Tennis was measured working against normalises
  every session at create: host counted, 32-byte session key, start time, attributes padded to
  6, `@SR`/`@GIR`. Tennis now gets `FinalizeCreateSession` + `FinalizeParams`, and
  `JoinEchoSession` + `JoinRecapParticipate` for friend joins.
- Tennis links NEX 4.3.5 and Pia 5.9.6 (its SDK banners), not Pia 5.19 as docs/parity.md
  had it. The public station keeps the 5.19 shape the measured server served it; the punch works.
- Not console-verified at release: the retest is the same Switch + ROG 3 Free Play match.

## 2026-10-11 — v0.3.27: simultaneous "join random" meets in one room

- Kirby's Dream Buffet: two players pressing join random together ended in two rooms
  (37 ms apart). The title creates its room closed and opens it itself ~2 s later, and
  searches only see open rooms, so the second search found nothing and created its own.
  A search that fits a room an earlier search just created, still closed, now waits for
  the host's OpenParticipation (`Env.HoldForOpening`, 4 s, Kirby's Dream Buffet and
  Risk of Rain 2) and joins it once open, so the joiner still gets the host's real station.
  A room that never opens releases the searcher after 4 s to create its own. Rooms made by
  an explicit create, and titles without the setting, behave as before.
- Find-or-create is atomic under the store's matchmaking lock, and searches pick the
  oldest fitting room first (were: map order), so searches arriving in the same
  millisecond cannot both create. The participation flag is read and written under the
  store lock.

## 2026-10-10 — v0.3.26: no personal data in logs

- Logs no longer carry pids, NSA ids, nicknames, login names, tokens or client
  addresses. Where two lines have to be matched, they carry a short per-process tag
  (`redact.Tag`: keyed HMAC prefix, key random per start, so tags cannot be compared
  across restarts or reversed).
- RMC bodies (names, ids, tickets inside) are not logged any more; the `rmc` line keeps
  sizes only. `NEX_TRACE_BODIES=1` brings the hex back for a local debug run.
- Room codes, code words, session descriptions and matchmake parameters are logged as
  presence or length only. Read errors drop the `net.OpError` addresses.
- `TestLogsCarryNoPersonalData` runs a proven login and a secure session and fails if a
  known id, name or token reaches the log.

## 2026-10-10 — v0.3.25: Mario Party's settings, Super Mario Maker 2's profile and Course World

(v0.3.24 carried the same changes; its CI failed on a startup race in `serve`, fixed here: `Endpoint.Listen` runs inline, so a `Close` right after `serve` no longer finds a nil channel.)

- **Mario Party Superstars** logged in, got an empty GetIntegerSettings map and went
  back to its menu. Its v1.1.0 main parses a real map and looks up keys 0-5 of index 0
  (an empty map: give up; a missing key: abort). The default is now the game's own
  built-in table: `0=1000` (peer-drop RTT, ms), `1=1000 2=500 3=250` (RTT-icon
  thresholds), `4=20` (rejoin host wait, s), `5=1` (rejoin on). `MPS_INTEGER_SETTINGS`
  still overrides it, and the box sets the same six values.
- **Super Mario Maker 2**: GetUsers (0x73.0x30) carries a `GetUsersParam` structure and
  failed its parse on every boot ("Couldn't retrieve uploaded courses", Course World
  communication error). Now: GetUsers answers UserInfo v3 or NotFound per PID (an empty
  list is the caller), RegisterUser stores the Mii picker's profile, SyncUserProfile
  answers `SyncUserProfileResult`, and the Course World hub's lists, endless mode,
  battle rating and event status answer valid empties. The station is `prudps`, so the
  CONNECT carries the ticket.

  **Still missing for course sharing** (shapes in kinnay's `datastore_smm2`):
  - profiles persist only in memory (a restart sends a maker back to the Mii picker);
  - upload: PreparePostObjectCourse (0x42) and PreparePostRelationObject (0x84,
    thumbnails and the clear-check replay) need an object store and presigned POSTs,
    then CompletePostObjectsCourse (0x44);
  - download: PrepareGetObject (0x19, now NotFound) with real URLs, and
    GetReqGetInfoHeadersInfo (0x86);
  - a course table (9-character codes, metadata, stats, CourseInfo with thumbnail
    relations, 32 slots per maker) behind every search: latest, popular/point
    ranking, advanced, pick-up, endless, and the Network Play battle pools
    (SearchCoursesBattleMode 0x4D/0x4E), which an empty catalogue probably leaves
    unable to start a versus or co-op match;
  - play-side writes: PostPlayResult*, ratings, death positions, comments, follows,
    super worlds, events; StartBattleMode/EndBattleMode (0x76/0x77) answers;
  - methods with no public shape (0x7D, 0x7F, 0x81, 0x82, 0x97, 0x98, 0xA7, 0xA8)
    still answer NotImplemented; the request bytes are logged if a console calls them.

## 2026-10-10 — v0.3.23: four titles from the multiplayer sweep

(v0.3.22 carried the same changes; its CI run failed on a racy relay test and published nothing.)

- **Mario Golf: Super Rush** private rooms and **Super Mario 3D World + Bowser's Fury**
  listed rooms: the joiner left exactly 10 s after a clean join (`Unable to enter the
  room.` / `An error has occurred.`). An explicit join (0x6D.0x27) now sends the recap
  (`JoinRecapParticipate`): 3001 about the joiner to everyone, then one 3001 per
  existing participant to the joiner. 3D World also echoed `count=0` (a created room
  counting nobody, no session key) and gets `FinalizeCreateSession`. Golf keeps
  `FinalizeParams`: its ranked lobbies were measured working with it.
- **Mario Tennis Aces** 2306-0116 after login: the empty-list DataStore profile answered
  PostMetaBinary (0x73.0x15) with a bare u32; it now answers the u64 data id the client
  reads. Data ids start from the clock, so a restart never reissues one.
- **Super Mario Bros. 35** matchmaking never completing (host 2/2, joiner 1/1):
  DeliverMessage (0x1B.0x1) is now a relay, as on the measured server: no RMC answer, the
  message stamped with sender, sender name and reception time and pushed to the
  recipient principal or every participant of the gathering, the sender included.

## 2026-10-10 — v0.3.21: a ticketless secure connection binds to its own login or none

A title whose station URL is `prudp` (MK8D, Tennis, Risk of Rain, SoR4, DST and
the rest without `prudps`) opens its secure connection with an empty CONNECT: no
ticket, nothing naming the player. It used to take **the title's latest login in the
last 30 s**, so two consoles logging in in the same second were both handed the
second one's PID (multiplayer sweep 2026-10-10, MK8D note 1). Now (`recall.go`):

- every login is claimable **once**, for 30 s, by a CONNECT for the same title, and
  only from the login's own address when that address is known;
- when more than one login could be meant, the CONNECT is **refused** (the console
  retries) instead of guessed; only a ticket (`prudps`) separates two consoles behind
  one address that log in in the same second;
- a re-CONNECT with no login in flight is answered only by an address that has played
  the title as exactly one PID (persisted, 24 h; the old one-PID-per-address file is
  ignored on load, its addresses were all Traefik's).

The auth stage reaches us through Traefik's TCP passthrough, so the login only ever saw
Traefik's address. The title listeners now read a **PROXY protocol** header (v1 or v2)
from a private/loopback peer, so with `proxyProtocol` on the `nex-*` Traefik services
the login is recorded under the console's address and two households never compete for
each other's login. Without the header the listener behaves as before. The bind log
lines carry no PID or address.

Deployed 2026-10-10 with `proxyProtocol: version 2` on all 22 `nex-*` services. Traefik
itself only sees rootless podman's 10.89.x forwarder address, so for now the header names
10.89.x and pairing is by title (claim once, refuse when ambiguous); see docs/deploy.md 2e.

## 2026-10-09 — v0.3.20: latest version only, and versions never share a match

aauth (nx-baas) already turns an outdated title away with 2124-3001. The login now
checks too: the token's `nintendo.ai`/`av` go to nx-baas's `/internal/title-version`
(the same table), and a version below the latest is refused with
`Authentication::ApplicationVersionIsOld` (0x00680013, 2306-0819), which carries the
console's own update-required dialog. That catches a token cached from before an update
was published. An nx-baas that cannot answer lets the player in.

Matchmaking keeps versions apart: a gathering takes its creator's version, and a player
of another version is not auto-matched into it, not shown it by any browse (ACNH's code
browse included) and not let in by its id (SessionVoid). An unknown version (no token,
or not 8 hex digits) meets everyone, as before. Every auto-matchmake now logs its search
criteria and the searcher's version.

## 2026-09-28 — v0.3.18: Don't Starve Together gets the measured server's whole create-time shape

Hosting got as far as a created gathering, its own Participate push, both shards
running — and then each shard reported "no available shard [Master|Caves] connected"
forever, with no socket call behind it. DST's RakNet on Switch is backed by Pia (the
binary links the whole `nn::pia::transport` stack), so a shard link that never opens is
a netgroup that never opened: the Risk of Rain failure with the crash filed off.

`FinalizeCreateSession` was already on, so the session was bounded, keyed, timestamped
and occupied. What it stops short of is the half the measured server applies **unconditionally
to every session of every title**: attributes padded to 6 and `@SR`/`@GIR` stamped. DST
was reading a session with short attributes and neither param. Both halves are on for it
now, `State = 1` included.

Not console-verified yet; this is the next thing a host run tests.

## 2026-09-28 — v0.3.17: Don't Starve Together (game server 251BB200)

One registry entry, ports 23050–23052, access key `c8e4be5d`. Both the id and the
key were read out of the title's own executable rather than guessed: its
`nxInternetStartup` passes `0x251BB200` in `w2` beside the literal key at
`main.nso 0x227dc4`, in the call that logs "nxInternetStartup: Performing NGS
login...". Neither value appears in Kinnay's Switch game-server list.

DST is a **two-stack title** — the first one here. Klei's own HTTPS backend
(`servers/klei`) carries the account and the server browser; NEX carries the
session and NAT traversal. Hosting runs Klei login, TokenPurpose, NGS login,
start NAT session, then a RakNet UDP server, and when any of it is missing the
player sees the same "Server failed to start".

NEX 4.6.7 with Pia 5.45.0, so the default 5.19 station shape applies. It links
MatchmakeExtension, NATTraversal, SecureConnection, Ranking, Ranking2,
MatchmakeReferee, DataStore and Utility; which it calls is the first boot's
NotImplemented log. Not console-verified.

## 2026-09-26 — v0.3.16: CI, one container per title, and auto-update

No server code change; `v0.3.15`'s `GetInt` fix is still the newest behaviour.

- **`release.yml`, at last.** switch-nex was the only OpenPak repo with no
  workflow — nothing matching `.github/**` in its whole history — so every image
  through v0.3.15 was cross-compiled and `podman build`-ed by hand. A `v*.*.*`
  tag now builds and publishes `:X.Y.Z`, `:X.Y` and `:latest` for both arches on
  the self-hosted runner, from a `Dockerfile` that compiles from source in a
  cross-compiled build stage (no QEMU). `Dockerfile.deploy` stays for the
  by-hand path.
- **One container per title.** 21 quadlets running the same image with
  `-only <title>`, as the Wii U/3DS lineage has always done. A panic, a hang or
  a deploy now touches one title instead of 21. Generated by
  `deploy/make-quadlets.sh` from the registry in `main.go`, so a title is named
  in one place and no hand-written file can forget the per-title recall volume —
  one shared `recall.json` across 21 titles is how two of them could hand each
  other a PID.
- **`AutoUpdate=registry` on `:latest`**, now that a restart costs one title
  rather than all of them. Watch the trap this walked into first: the registry's
  `:latest` was still v0.3.14 while production ran v0.3.15, because hand-built
  releases had only pushed version tags. Enabling auto-update against it would
  have reverted the Kirby fix within five minutes.
- Two things the split needed, both easy to miss: `VOICE_GATHERING_URLS` must
  list all 21 healthz ports (the lookup answers for a *process*, which used to
  mean every title), and the INPUT chain must admit `10.89.2.0/24` to the title
  ports. The latter had never existed — only MK8D's 22152 was ever opened, so
  the status page had been reporting every other Switch title down.

## 2026-09-26 — v0.3.15: GetSessionURLs stops burning its 2.5s wait (Kirby regression)

- **`StationURL.GetInt` never read a parsed station.** `Parse` puts every field
  in `Params` as a string; only `SetInt` fills `IntVals`, and `GetInt` (added
  with the wait on 2026-09-20) read `IntVals` alone — so it returned 0
  for RVCID, CID and port on every station that came off the wire.
  `hasValidReplaceURL` therefore never fired, and
  `stationURLListWithHostWait` slept the whole 2500 ms on **every** join before
  returning the same stations it already had. It now reads the string side too,
  the way `Get` does.
- **Kirby's Dream Buffet is what this broke.** Its host opens its room for only
  ~13 s after the search and then closes it to fill CPUs, so the joiner has to
  finish its mesh inside that window. Journal 2026-09-26 19:06: the joiner
  auto-joined at +8 s, spent 2.5 s of dead wait in `0x15/0x29`, punched
  successfully at +13 s — the same second the host sent CloseParticipation —
  and sent `EndParticipation` one second later. The same pair on 2026-09-19
  (before the wait existed) went join → probe → punch in 2 s and played for
  7 minutes. Every other NEX title paid the 2.5 s too; Kirby is just the one
  with no slack to pay it from.

## Unreleased — risk-of-rain-2 pairs and meshes; the game crashes on its own null

No code change. `v0.3.14` was measured on a console 2026-09-26 04:21 UTC (Ryujinx hosting as
<user-a>, a real Switch joining as <user-b>) and **every server-side gate passed**:

- `FindByParticipant` worked — the friend lookup found the host and `matchmake: joined gid=454173
  pid=12297829382472990722 count=1` at 04:21:33, where before the fix both sides got an empty list.
- The recap landed: a `3001` per participant to both PIDs.
- NAT: both `moderate` (mapping=1 filtering=2), probes relayed in both directions, and
  **`nat: punch to cid=1 ok (rtt=48ms detail=0)`** at 04:21:38.
- **The Pia mesh formed and carried game data both ways.** Ryujinx's own BSD log shows 186
  packets sent to the Switch's LAN address (144×80B, 20×336B, one 1008B) and 174 successful
  `RecvFrom` in the same window. This is further than either other Risk of Rain title has gone:
  RoR2 links the whole Pia 5.45 stack (PiaSession/Sync/Clone/Reckoning/Transport/Local), so it
  needs a *sustained* mesh, not just a successful probe.
- Zero `NotImplemented` across the run. The host's last call is `0x6D.0x01` CloseParticipation
  at 04:22:00 — the normal "we are starting" — and the joiner's is `0x6D.0x29`
  FindMatchmakeSessionByGatheringIDDetail at 04:21:39, answered with the full 437-byte session.

**What fails is past our edge, and the order matters.** The Switch gave up at 04:22:12 showing
"the host player has ended the game" — 34 s after the punch, 12 s after the host closed
participation — and the host crashed **33 s later**, at 04:22:45. So the joiner left first and
the host's crash follows it rather than causing it.

The crash is the game's own null dereference, not an emulator fault: Ryujinx logged
`Invalid memory access at virtual address 0x0000000000000000` with a guest stack trace entirely
inside `SwitchPlayer.nss` (PC `0x1cfeea0`, LR `0x182a84c`, 20 frames down to `nnMain`), then
aborted — SIGABRT, coredump truncated but not needed. Same class as `risk-of-rain`'s v0.3.7 host
crash: a method called on an object that was never built.

**Next experiment, one variable: let the Switch host and Ryujinx join.** That separates "Ryujinx
cannot host RoR2 gameplay" from "the pairing is wrong", and it is free. Also unknown because we
have no log for it: why the Switch decided the host was gone while the host was demonstrably
still sending it packets. Both clients share one public address (37.228.205.45, Switch
10.87.0.10, Ryujinx 10.87.0.2) — the hairpinning variable the playbook names for MK8D — though
risk-of-rain-returns plays in exactly this topology, so same-NAT is not on its own an
explanation.

## 2026-09-25 — v0.3.14: risk-of-rain-2 finds your friend's game

- **`risk-of-rain-2` gets `FindByParticipant`.** Console-measured (a real Switch as <user-b>,
  Ryujinx as <user-a>, both directions): the title hosts correctly — login, register, create, NAT
  and its own `OpenParticipation` all succeed — but "join your friend's game" found nothing. The
  friend lookup is MatchmakeExtension `0x33` (`FindMatchmakeSessionByParticipant`) and both
  clients send their friends' real PIDs in it; with the flag off we answered an empty list to
  both, so neither ever received a gathering id. First title in this server to need `0x33`.
- **The access key `8bac191f` is CORRECT** — this run settles it. CONNECT carried a valid Lite
  signature on both clients (`prudplite: CONNECT bad signature` never appeared, and the secure
  stage reached `calls=12`), so the metadata string-literal guess was right after all even
  though the game server id found the same way was not.
- **Quickplay opening two rooms is a race, not a server fault — measured, and deliberately not
  "fixed".** This title creates its session closed and opens it itself ~2 s later, after the NAT
  check and station update: `0x6D.0x02 OpenParticipation` arrived at 21:40:26 for a gathering
  created at 21:40:24. `autoMatchmake` only considers `Match.open()`, so two players pressing
  quickplay inside that window each create their own room — precisely the log
  (451906 at :24, 451907 at :26, neither open yet). `ForceOpenParticipation` would paper over it
  and Kirby's Dream Buffet measured the price: a joiner gets in before the host's station update
  and probes a `port=1` placeholder (2618-0502). Staggering the two searches is the test.
- Carries the unreleased `-dump` fix, so `JoinRecapParticipate`, `FinalizeParams` and
  `MatchModeAttribute` finally print. `risk-of-rain-2` now reads
  `behaviour: FindByParticipant, FinalizeCreateSession, JoinEchoSession, JoinRecapParticipate`.
- **Tagged, unlike v0.3.11-v0.3.13.** Those three shipped images with no git tag; this one has
  one.

## Unreleased — shipped in v0.3.14 above

- **`risk-of-rain-2`'s game server id was wrong, and it cost a boot: 2855FF00, not 23C34600.**
  Console-measured 2026-09-25 on a real Switch *and* on Ryujinx, both failing identically with
  **2306-0502**: the title resolves `g2855ff00-lp1.s.n.srv.nintendo.net` and nothing else. With
  no Traefik router for that name the console's TLS landed on the catch-all, got an HTTP answer
  where NEX expected a carriage, and gave up without ever spending its ticket — the same
  signature Minecraft showed with 2306-0502 on 2026-09-18. `switch-nexd` logged nothing at all
  for the title, which is the tell: a routing failure, not a protocol one. Router repointed, no
  restart needed (Traefik watches the file), backup
  `dynamic.before-ror2-gsid-fix-20260925.yml`. **Fixed in config only — the code change here is
  comments.**
- **`gsid.py` could not have found it, and this is the lesson for the next Unity title.**
  `2855FF00` is absent from `main.bin` entirely: not a little-endian word, not ASCII, not a
  movz/movk pair at any distance up to 24 instructions — and not in `global-metadata.dat`
  either. Every candidate the scan *did* offer (`23C34600`, `2E040301`, `20104000`, `2E090300`)
  was wrong. Picking `23C34600` on shape ("ends in 00, sits in the right era") was reasoning
  over a candidate set that never contained the answer, which no amount of care about the
  shortlist would have caught. **For a Unity IL2CPP title, boot it and read the DNS line before
  scanning anything** — that is one run, and it is decisive.
- **The access key `8bac191f` is now doubtful and should be treated as unconfirmed.** It was
  picked as "the only 8-char hex-shaped literal in global-metadata.dat", but it sits in a long
  alphabetical run of 32-char Unity asset GUIDs, so hex-shapedness does not separate an access
  key from an asset id in this game — and the game server id, reached by the same style of
  argument, did not survive contact. It is left in place for exactly one test because a wrong
  key announces itself: `prudplite: CONNECT bad signature got=… want=…`. If that appears, empty
  the key (how `risk-of-rain-returns` runs, and it plays), and recover the real one offline from
  the logged signature — HMAC-MD5(md5(key), md5(key) || connSig) over 32 bytes.
- **`risk-of-rain-2` is missing from the status page's checks.** Gatus on Draughts probes
  `streets-of-rage-4`, `risk-of-rain` and `risk-of-rain-returns` every minute from
  145.241.228.207 and has never probed 23041. Not this repo's file, but nobody would have
  noticed the title was unroutable from the monitoring either.

- **`-dump` was hiding a third of each title's behaviour.** Its list covered
  `FinalizeCreateSession` and `JoinEchoSession` but not `JoinRecapParticipate`, `FinalizeParams`
  or `MatchModeAttribute`, so a title with all three of the session-lifecycle flags printed
  exactly the same two as a title with two of them. `risk-of-rain` has printed
  `behaviour: FinalizeCreateSession, JoinEchoSession` all evening while demonstrably recapping,
  and `risk-of-rain-2` on the deployed v0.3.13 prints it too. Found by `openpak-56` while
  verifying v0.3.13. The three missing flags are now in the list.
- This matters beyond tidiness: "`behaviour: none` on a matchmaking title is a defect" is the
  rule that predicted the `streets-of-rage-4` and `risk-of-rain-2` fixes, and `-dump` is how it
  gets checked. It was only ever able to confirm two thirds of the cure. Until a build carries
  this, the runtime check for the recap is the join pushing a `3001` per participant rather
  than one `AddedToGathering`.
- Diagnostic only, no behaviour change. **Not built or deployed** — the box is mid-console-test
  and a diagnostic does not earn a restart. Rides the next build.

## 2026-09-25 — v0.3.13: risk-of-rain-2 gets the three flags after all

Shared release: `openpak-56` folded this in with its own `streets-of-rage-4` recap (c9d0e0c,
tagged v0.3.12 and pushed but never deployed) so one restart covered both; that session writes
its own bullets. v0.3.11 was skipped — I reserved it, then stayed off the build.

- **`risk-of-rain-2` gets `FinalizeCreateSession`, `JoinEchoSession` and `JoinRecapParticipate`**
  (commit b6673db, written by `openpak-56` at this title's request). Predicted, not measured: the
  trio went 4-for-4 across the matchmaking titles touched on 2026-09-25 — Clubhouse Games before
  them, then `risk-of-rain`, `risk-of-rain-returns` and `streets-of-rage-4`, each arriving at it
  from a different symptom. Shipping the title without them in v0.3.10 was the wrong call and
  reversing it cost one restart.
- `FinalizeParams` stays off. It is the one member of the family that has actually hurt a title
  (ACNH, 2306-0116, foreign keys in a strict parse), and nothing about this title has been
  measured yet.
- **Still not console-verified**, and the two static findings still stand unconfirmed: game
  server `23C34600` and access key `8bac191f`. The first boot settles both for free — a wrong id
  shows as the console resolving some other `g<hex>-lp1` host, a wrong key as a CONNECT
  signature that does not verify, and the fix for that is to empty the key, which is how Returns
  runs and plays.
- **No git tag exists for v0.3.11, v0.3.12 or v0.3.13** — origin carries tags only up to
  `v0.3.10`. The deployed image is therefore not reproducible from a tag, and CI (which builds
  on `v*.*.*` tags only) never saw these three. Worth tagging `v0.3.13` at b6673db retroactively
  so the running image has a name in git.

## 2026-09-25 — v0.3.10: Risk of Rain 2

Shared release: `openpak-56`'s `streets-of-rage-4` flags (commit 47db0d1) are in the same
unreleased build and that session writes its own bullets here.

- **New title `risk-of-rain-2`** (010076D00E4BA000, update v1.4.1, NEX 4.6.7 + Pia 5.45.0) on
  port block 23040 (auth 23040, secure 23041, healthz 23042). 23040 rather than 23010 so the
  block collides with nothing in ports.md's development table — 23000/23001 already sit on two
  Postgres reservations and a third strikethrough was avoidable.
- **The access key is a C# literal, not an ARM64 immediate.** `keys.py` finds no 8-char
  immediate in `main.bin` and the key is not a string there either, the same dead end Returns
  hit. It is in the Unity metadata: `8bac191f` is the only hex-shaped 8-char string literal in
  all 29 230 literals of `Data/Managed/Metadata/global-metadata.dat`, which is where the
  managed side keeps the `accessKey` it hands the SDK. Returns has no metadata to read
  (GameMaker), so the same search there had nowhere to go.
- **Game server `23C34600`, chosen from `gsid.py`'s candidates on shape.** It is the only value
  in the Switch range the binary builds as a movz/movk pair that also ends in `00`, as every
  allocated Switch id does; the higher-frequency candidates (`2EDBE6FE`, `24924925`,
  `2AAAAAAB`) are division magic and hash constants. Not console-measured — the first boot's
  DNS log settles it, and a wrong guess shows as the console resolving some other `g<hex>-lp1`
  host.
- Registers matchmaking, Ranking, Ranking2 and an empty-list DataStore, from the SDK banners
  the binary carries (`NEX_MM`, `NEX_RK`, `NEX_R2`, `NEX_DS`, `NEX_UT`, `NEX_SS`, `NEX_CO`).
  **No `MatchmakeReferee`** — there is no `NEX_MR` banner, which is where this title differs
  from both Risk of Rain and Returns.
- **No session-lifecycle flags.** `JoinEchoSession`, `JoinRecapParticipate` and
  `FinalizeCreateSession` were each a response to a named console symptom in the other two
  titles; setting them here pre-emptively would change what the first boot's failure means.
  **Reversed the same evening in v0.3.13 — the argument was wrong.** This boot's job is
  confirming the game server id and the access key, and both are settled at DNS and handshake
  time, nowhere near matchmaking; the flags could not have blurred it.
  Pia 5.45 is well past 5.19, so the default public station shape applies (not `LegacyPia`).
- No new flags or environment variables. Not console-verified.

## 2026-09-25 — v0.3.9: session fixes for both Risk of Rain titles

Shared release: `openpak-56` took this number for `risk-of-rain: JoinEchoSession` in the same
build. Both titles' bullets belong here.

- **`risk-of-rain-returns` gets `FinalizeCreateSession`.** The v0.3.7 recap worked — the joiner
  now receives a `3001` per participant instead of only `AddedToGathering` about itself — and it
  still timed out, with the join echo logging `count=0`. `ParticipationCount` is only ever set to
  1 (the host) inside `finalizeCreatedSession`, and `createSession` never generates the 32-byte
  `SessionKey`, so a title that leaves this off hands its joiner a session that reports nobody in
  it and carries no key. ACNH's provenance records Pia using that key to seed the P2P session
  *before* JOIN, which matches the measurement: the probe and its reply cross the LAN fine, three
  times, and nothing follows them.
- `FinalizeParams` stays false, so the normalisation returns before touching attributes or the
  param map — foreign keys and padded attributes are what broke ACNH with 2306-0116.
- Reached independently from the host side by `openpak-56` for `risk-of-rain` in v0.3.8: create is
  the same code for both titles, so two sessions converging on it from opposite ends of the
  session lifecycle is the better part of the evidence.
- No new flags or environment variables.
- **Console-verified playable 2026-09-25**, `ghcr.io/openpak/switch-nex:v0.3.9`. Ryujinx hosted
  `gid=450172` and a real Switch joined it: `matchmake: joined … count=1`, the recap delivered a
  `3001` per participant, and the session held — no joiner disconnect, where every previous
  attempt dropped 15-95 s after the join. Four fixes were needed end to end, v0.3.5 through
  v0.3.9: the title itself (gsid 45559E00, NEX not NPLN), `JoinEchoSession`,
  `JoinRecapParticipate` and `FinalizeCreateSession`. The access key is still unrecovered, so
  CONNECT signatures go unverified; nothing else about the title depends on it.

## 2026-09-25 — v0.3.7: Risk of Rain gets the legacy station shape (does NOT fix its crash)

- **`risk-of-rain` moves to `LegacyPia()`.** The title is bare NEX 4.5 with no Pia, and
  `Register` was writing its public station in the Pia 5.19 shape (`type=0x0B` plus `Pa=`),
  which its station parser predates. First user of `LegacyPia()`, unused since it was written.
  Correct on its own terms — the register line now reads `type=3` with no `Pa=`.
- **It does not fix the hosting crash, which is still open.** Console-measured on a real Switch
  and on Ryujinx alike, before and after this change: the gathering is created and NAT
  reported, then the game dies ~2 s later. Its own log reads `netgroup: OpenSocket` /
  `Open failed` with not one socket call behind it, and it then calls a method on the object
  `Open` never built (null `this`, PC `RiskOfRain_NX.nss:0x184838`, LR `0x16bc90`). A failure
  decided before any syscall is a parse or precondition on something already in hand; the
  station shape was the candidate and it was wrong. Next step is disassembling the title at
  those offsets — the remaining alternatives are guesses.
- Risk of Rain Returns is NEX 4.6 and keeps the 5.19 shape. It creates, browses and joins on
  the same daemon; it does **not** yet play — see its own bullet below. That contrast is what
  made the station shape look like the answer.
- **`risk-of-rain-returns` gets `JoinRecapParticipate`** (shipped in this same image, commit
  3b955d6). With the join answer fixed in v0.3.6, a quick lobby showed two players to the host
  and counted down while the joiner's screen stayed empty: the default pairing tells everyone
  already in the gathering about the joiner, and tells the joiner only about itself
  (`AddedToGathering`), so it never hears the host exists. The recap is the same cure
  `risk-of-rain` needed in v0.3.4. Measured working — the joiner now receives a `3001` per
  participant — and the title still times out afterwards, so it is not the last problem.
- **The Pia punch is not what is wrong with Risk of Rain Returns.** `tcpdump` on the LAN caught
  the probe and its reply crossing in both directions between the console and the emulator,
  three attempts, each succeeding and then going quiet (25-byte request, 27-byte reply). Host
  firewall, routing and NAT were all ruled out this way.
- No new flags or environment variables.

## 2026-09-25 — v0.3.6: Risk of Rain Returns joins

- **`risk-of-rain-returns` gets `JoinEchoSession`.** First two-client contact (Ryujinx
  hosting, Eden joining) got through login, register, create, NAT and browse: the host's
  lobby was listed on the joiner's screen, `browse code="" -> 1 session(s)`, and the join
  was accepted server-side (`matchmake: joined gid=443384`). The joiner then failed on
  screen with **2306-0116** and sat for 95 s before closing. That code is
  `Core::InvalidArgument` — a parse rejection, not a timeout — and JoinMatchmakeSession
  (`0x6D.0x27`) had answered a bare `Bool(true)`, one byte where the client reads a whole
  session. Same shift ACNH documents for the same code. The join family now echoes the
  session, with the pre-join participation count as Clubhouse Games measured.
- Not the recap: `risk-of-rain`'s cure (`JoinRecapParticipate`) addresses a joiner that
  joins cleanly and then never learns of the host. This joiner never got that far — it
  rejected the join answer itself. Left off until a run needs it.
- No new flags or environment variables.
- Deployed to quilted-waffle 2026-09-25 (`ghcr.io/openpak/switch-nex:v0.3.6`, quadlet previous
  saved as `.before-v0.3.6-20260925`). **Confirmed**: the join is accepted on screen,
  2306-0116 is gone and `matchmake: joined … count=0` shows the echo path ran. The game then
  reaches the NAT probe exchange and both peers exchange InitiateProbe.
- **Still not playable together, and not a server fault.** Both clients ran on one machine
  behind one NAT, both classified `moderate` (mapping=1 filtering=2). Each advertises its
  public pair plus a LAN path in `Ntrpa`/`Ntrpp`, and reaching the public pair from inside the
  same NAT needs router hairpinning. Same wall Mario Kart 8 Deluxe hit on 2026-09-20; see the
  comment in `protocols/nattraversal.go`. The relay itself is correct: the prober's own `self`
  station, with its real port, is what gets pushed — not the `port=1` placeholder the clients
  register before Pia binds its socket. Two separate networks, or a relayed P2P path through
  the existing coturn, is what would settle it.
- Emulator-side note: both emulators defaulted to the same host NIC, so both advertised
  `10.87.0.2` and probed themselves. Eden is now pinned to `enp5s0` (`10.87.0.3`).

## 2026-09-25 — v0.3.5: Risk of Rain Returns

- **New title `risk-of-rain-returns`** (0100D1801A0F4000, game server 45559E00) on port
  block 23000 (auth 23000, secure 23001, healthz 23002) — a new range, because
  22700–22899 filled up with `risk-of-rain` and every earlier NEX range was already full.
  ports.md now carries 23000–23499 for Switch NEX and leaves 23500–23999 unallocated.
- **The game server id is console-measured, not read out of a config file.** The title
  resolves `g45559e00-lp1.s.n.srv.nintendo.net` and nothing else, on three separate
  attempts in one Ryujinx run, and never asks for a `t-*-lp1` NPLN tenant. This corrects
  `research-switch-dump-stacks.md`, which had it as NPLN on the strength of the
  `switch_npln_*` symbols its GameMaker runner exports: the runner links both stacks in
  every build it produces, so the symbols prove nothing and the
  `SDK MW+Nintendo+NEX-4_6_7` banner was right.
- **Access key unknown, and empty on purpose.** Unlike `risk-of-rain` this title ships no
  `nx/config.csv` (its RomFS has none), the key is not in `game.win`, and no 8-hex string
  sits in `main`/`subsdk0`/`subsdk1`/`sdk` — it is built as an ARM64 immediate. Empty
  means CONNECT signatures go unverified, which costs nothing here: the server never signs
  its own CONNECT-Ack, so the client has nothing of ours to check. Recovering it later is a
  brute force over one logged handshake, since the signature is
  HMAC-MD5(md5(key), md5(key) || connSig) over 32 bytes.
- Registers matchmaking, Ranking, Ranking2, MatchmakeReferee and an empty-list DataStore —
  the same set the binary links. The game drives `switch_matchmaking_*` and
  `switch_leaderboard_{get_scores,post_score,post_common_data}`, and calls
  `session_open_participation`/`session_close_participation` itself, so participation is
  left alone rather than forced open. Not console-verified.
- No new flags or environment variables.

## 2026-09-25 — v0.3.4: Risk of Rain joins

- **`risk-of-rain` gets `JoinRecapParticipate`.** First console contact (Eden hosting,
  Ryujinx joining) got all the way through login, browse and join: the key `61b8f3a6` and
  game server `29C91300` read out of the game's own `nx/config.csv` are correct, and nothing
  answered NotImplemented. The joiner then sent MatchMakingExt EndParticipation two seconds
  after the join without ever asking for participant URLs — the ACNH shape: it heard only
  `AddedToGathering` (122000) about itself and never learned the host was there. The recap
  notification set is the measured cure.
- Measured protocol use, narrower than v0.3.3 assumed: MatchmakeExtension `0x34`/`0x27`,
  MatchMakingExt `0x32`, Secure and NAT traversal. Ranking, Ranking2, MatchmakeReferee and
  DataStore were never called. They stay registered — the binary links them, and a single
  matchmaking session is not the whole game.
- No new flags or environment variables.
- Deployed to quilted-waffle 2026-09-25: image `ghcr.io/openpak/switch-nex:v0.3.5` pushed and
  pinned in the quadlet (previous saved as `.before-v0.3.5-20260925`), Traefik SNI router
  `g45559e00-lp1.s.n.srv.nintendo.net` → `host.containers.internal:23000` (delta backup
  `dynamic.before-risk-of-rain-returns-20260925.yml`), INPUT ACCEPT for 23001/tcp inserted
  before the REJECT and persisted to `/etc/iptables/rules.v4`, OCI ingress opened for
  23000–23999/tcp. Verified: `[risk-of-rain-returns] serving auth :23000 secure :23001
  healthz :23002`, all three bound, in-container binary sha256 `a1d6b913…` equal to the
  released `switch-nexd-arm64`, and both stages serve the OpenPak leaf from outside the host
  (23001 direct, 443 via SNI). Not console-verified.

## 2026-09-25 — v0.3.3: Risk of Rain

- **New title `risk-of-rain`** (0100AAA00ACBE800, game server 29C91300, nnSdk 4.5.0, no Pia)
  on port block 22890 (auth 22890, secure 22891, healthz 22892). Game server id and access
  key `61b8f3a6` come from the game's own RomFS config (`nx/config.csv`), not from the
  binary's defaults — those are the NEX sample demo's 0x27851C01 / `12345678`, which would
  have been wrong. Registers matchmaking, Ranking, Ranking2, MatchmakeReferee and an
  empty-list DataStore, matching the protocols the binary links. Not console-verified yet.
- No new flags or environment variables.

## 2026-09-24 — v0.3.2: Streets of Rage 4

- **New title `streets-of-rage-4`** (0100EC9010258000, game server 27C90000, NEX 4.6.3,
  Pia 5.26.2) on port block 22880 (auth 22880, secure 22881, healthz 22882). Access key
  read from our v0 dump. Matchmaking only: the binary links NAT traversal and the
  notification protocols and nothing else. Not console-verified yet — the game reached
  the auth host and got a 404 from the catch-all before this.
- No new flags or environment variables.

## 2026-09-23 — v0.3.1: voice-chat gathering lookup

- **`GET /internal/voice/gathering?account_id=<core id>`** on every title's healthz port
  (`X-Internal-Key` = `NX_INTERNAL_KEY`): the gathering that account is playing in on any
  title this process serves, and its participants with their OpenPak accounts. The website's
  voice chat (phone app) uses it to put a gathering's players in one voice room. 404 when the
  account is in no gathering.
- A proven login now remembers the core `account_id` nx-baas already returns with the
  identity. Unproven (name-fallback) logins have no account and show with an empty one.
- `protocols.Env.GatheringOf(pid)`: the newest gathering a PID participates in, copied
  under the title lock.
- No new flags, ports or environment variables.

## 2026-09-23 — v0.3.0: bans

- **A banned account is locked out** (website/docs/ban-lookup.md). nx-baas answers
  `/internal/switch/identity` with 403 `account_banned`/`account_deleted` for it; the login is
  refused with `RendezVous::AccountDisabled` (0x00030067), the NEX result for a disabled
  account, whether or not `-require-identity` is set (the name fallback would otherwise let
  a banned player straight back in).
- **Live sessions end within a minute.** Every 30 s the secure endpoint re-asks nx-baas (by
  the nx-baas pid recorded at login) about each connected player and drops the connection of
  one now banned. nx-baas caches an active answer for at most 30 s. An nx-baas outage keeps
  sessions. New `prudplite.Endpoint.Connections` and `Connection.Drop`.
- No new flags or environment variables.

## 2026-09-22 — v0.2.41: Switch principal ids are the NSA id; friend visits

- **A proven login's PID is its network service account id** (the hex
  `baas_user_id` as a number), not nx-baas's minted pid. The game logs in
  with that number as its username, its web API names the user by it, and
  friends-only islands compare the host's PID against the visitor's friend
  NSA ids: with a minted pid a friend never matched (console could not see
  <user-a>'s friends-only island, 2026-09-22). The friend list nx-baas
  returns with the identity is kept per login.
- **Friend notification data (0x6D/0x09, 0x0A, 0x0D) implemented** the
  measured way: a host's published data goes to its online friends as
  type*1000 at once, and a friend asking gets the open gates (Param2 != 0)
  back, each pushed again at type*1000 after the answer. This is the "visit
  a friend" menu; it was an empty list.
- Gathering ids seed from the clock so ACNH's Dodo Codes (derived from the
  id) stop repeating after every deploy.

## 2026-09-22 — v0.2.40: ACNH visitors get the join recap

- **`JoinRecapParticipate` for ACNH.** On an explicit join the measured server
  pushes Participate (3001) about the joiner to every participant, joiner
  included, then one Participate per pre-existing participant to the joiner.
  We sent 3001 to the others and 122000 to the joiner; ACNH's visitor waited
  ten seconds for the recap, sent EndParticipation and showed "A
  communication error has occurred" (Ryujinx visiting the console's NMT18).
  Auto-match titles keep their measured pair.
- Dodo-code visits now reach the join (v0.2.37–v0.2.39). Friends-only
  islands are still invisible to a friend: the console does not see the
  host PID as a friend. Open.

## 2026-09-22 — v0.2.38: ACNH islands carry @SR/@GIR again

- **`FinalizeParams` back on for ACNH.** With the 0x34 answer parsed and
  code-matched (v0.2.37), the visitor still refused the island ("no island
  I can take you to right now"). The measured server stamps `@SR`/`@GIR` and pads
  attributes on every ACNH island and its visits work; v0.2.36 had removed
  them blaming a 2306-0116 that was in fact the ResultRange prefix.

## 2026-09-22 — v0.2.37: ACNH Dodo codes work

- **UpdateMatchmakeSessionPart (0x6D/0x2C) reads the whole param.** The
  wire body carries every field of UpdateMatchmakeSessionParam and the
  modification flags only say which to apply; reading just the flagged
  fields took the attribute count as the open flag and the zeroed
  attributes as the codeword, so every Dodo Code was stored as six NULs and
  the island stayed closed. Console-measured (host flags 0x4002, 93B).
- **BrowseMatchmakeSessionNoHolderNoResultRange (0x6D/0x34) answers a raw
  session list filtered by codeword.** The visitor's Dodo-code lookup
  carries the code in the criteria and expects `u32 count + bodies` — no
  ResultRange, no holder. The ResultRange prefix was the 2306-0116 on
  "locate island by code". The session key stays in the answer: the code
  is the pass.
- nx-baas v0.12.54 (same session): Orville's "Passport violates the Code
  of Conduct" refusal was a verdict the game remembered from an earlier
  failed passport upload, cleared by a changed passport being re-uploaded.
  `profile_status` values do not gate it either way.

## 2026-09-21 — v0.2.36: ACNH sessions carry no foreign keys

- **Param-map normalisation is per-title (`FinalizeParams`).** The shared
  create-time finalise stamped Clubhouse-measured `@SR`/`@GIR` and padded
  attributes to 6 on every title that opts into it. ACNH's binary knows
  `@SR` and has never heard of `@GIR`: the foreign key broke its parse of
  Dodo-code browse results (2306-0116, KYRWJ visit). Golf, MK8D, Kirby and
  Clubhouse keep the measured behaviour unchanged; ACNH (and everyone else)
  now keeps exactly the parameters its client sent, plus key/count/time.

## 2026-09-21 — v0.2.35: ACNH identity + joinable islands

- **A numeric login username is used verbatim as the NEX PID.** ACNH logs
  in with its numeric account id (`"12297829382472990721"`), and hashing it
  minted a second, unrelated identity: `FindMatchmakeSessionByParticipant`
  looked for the number the game knows and found only the hash, so every
  island visit answered empty ("no island to visit"). Opaque names keep the
  stable hash; `0`/empty never become PID 0.
- **ACNH sessions are created joinable and joins echo the session.**
  `FinalizeCreateSession` (host counted, 32-byte key, timestamps) plus
  `JoinEchoSession`: the Dodo-code browse result for NMT18 carried a keyless
  session the visitor could not start P2P with. Same treatment MK8D,
  Clubhouse Games and Kirby already get.

## 2026-09-21 — v0.2.34: Golf explicit joins echo the session

- **Golf-only `JoinEchoSession`** (same flag MK8D and Clubhouse carry).
  Ranked joins go through auto-match, which already echoes the full session
  — this changes nothing there. It covers explicit joins (friend/private
  rooms) only. No mesh effect expected; the mesh join is now a client-side
  question (tap evidence: transport up both ways, join payloads
  retransmitted unanswered ~3.5 min, Pia error 2219-0021 both directions).

## 2026-09-21 — v0.2.33: match Golf rooms on the primary option

- **Golf-only `MatchModeAttribute`:** auto-match rooms compare the search
  criteria's GameMode plus the FIRST attribute string only. The later
  attributes are per-player settings (console-measured: constant per player
  across every mode and round) that never agree between two players, so
  matching them — v0.2.32 following the measured server's exact-match rule —
  stranded every pair in private rooms. The criteria decoding itself
  (AutoMatchmakeParam tail) stays, verified against a captured console body.
- Other titles are untouched: without the flag, matching is password + NAT
  as before.

## 2026-09-21 — v0.2.32: auto-match follows the search criteria

- **Rooms are matched against the search criteria, as the measured server matches
  them.** `AutoMatchmakeWithParam` carries an `AutoMatchmakeParam` past the
  source session: a block-list, option, join message, and the search-criteria
  list. Each criterion names a GameMode and attribute strings; a room
  qualifies when the GameMode agrees and every non-empty attribute string
  equals the room's attribute at the same index (or falls in its min,max
  range). Attribute index 1 is reserved for the selection method and never
  filters. No criteria (the pre-param path) matches everything, and an
  unreadable criteria block fails open to the same.
- **This is the real Speed/Standard split.** The criteria carry the mode and
  settings as strings (console-measured: `["3","","259",""…""]` vs
  `["1","","320",""…""]`, GameMode `"2"`); the source sessions alone do not
  tell the queues apart, which is what sank v0.2.29.

## 2026-09-21 — v0.2.31: log raw auto-match searches

- **Diagnostic:** confirmed on console that Golf's Speed and Standard ranked
  searches are identical in every parsed field (GameMode 2, same attributes,
  same params, empty appBuf/description) — the v0.2.29 attribute theory is
  dead. Each auto-match search now also logs its raw bytes (capped) so the
  marker can be found by offline diff, if the server ever sees it.

## 2026-09-21 — v0.2.30: revert mode-split matching, log session templates

- **Reverted:** v0.2.29 matched auto-match rooms on GameMode plus the full
  attribute list. The attributes vary per search even inside one mode, so
  same-mode lobbies stopped meeting entirely — both Golf ranked queues sat
  alone. Matching is back to v0.2.28 behaviour (password + NAT) while the
  real mode marker is found.
- **Added:** every auto-match search now logs its full session template
  (`matchmake: template …` — gameMode, attributes, application buffer,
  matchmake-param map, description). That line is what will name the field
  that distinguishes Speed from Standard.

## 2026-09-21 — v0.2.29: Golf's match points upload is acknowledged

- **Ranking `UploadScore` (0x70.0x01) answers bool true.** Mario Golf posts its
  match points the moment a ranked match ends (console-measured 2026-09-21:
  `EndParticipation`, then 0x70/0x01, category 26, score 1037). It got
  `NotImplemented`, hung up with 2306-0103, and the game reported the points
  as lost. Scores are acknowledged, not stored — same stance as DataStore.

## 2026-09-21 — v0.2.28: ACNH gets its secure station scheme

- **Animal Crossing: New Horizons serves `prudps`, not `prudp`.** Its entry
  carried no scheme override, so the default went out in every login station
  URL. The title builds its secure station with the secure scheme, and on
  `prudp` its CONNECT arrives empty and dies when Pia needs the session
  (servers/nex provenance). Untested live — no ACNH
  client has completed a login yet — but the wrong scheme fails closed on
  first contact, so it ships before the title's first session, not after.

## 2026-09-20 — v0.2.27: Golf gets the Pia shape the measured server gives it

- **Mario Golf: Super Rush moves to the Pia 5.19+ station shape.** The
  measured server's Golf profile defaults to the MODERN shape — its own
  `GOLF_LEGACY_PIA` is `0`, and the legacy type=0x03 is the opt-in. We had the
  two the wrong way round: Golf's public station went back `type=3` with no
  `Pa`, and Pa is what a Pia 5.19+ title punches toward. Its era-guess is now
  measured — Ryujinx reports Golf linking **Pia 5.33.0**, well past the 5.19
  cutover, and a working title on this server (Kirby) already gets
  `type=11;Pa=`.

## 2026-09-20 — v0.2.26: Golf's lobby is normalised like every other

- **`FinalizeCreateSession` on for Mario Golf: Super Rush.** The measured server
  normalises every session it creates, for every title; we had it behind a
  per-title flag set only for Clubhouse Games and MK8D. Golf's lobby went back
  keyless, unstamped, and counting one participant while two stood in it. Both
  sides hole-punched through to each other and neither ever started; each gave
  up after ~60 s and re-matchmade.
- **`@SR` and `@GIR` are stamped on a created session**, as the measured server does.
  Without them the param map goes back exactly as the client sent it.

## 2026-09-20 — v0.2.25: Mario Golf gets past its first call

- **Ranking `GetRanking` (0x70.0x09) is answered.** Golf's first call after
  CONNECT is a board read (mode 1, category 1280, a window of one around
  itself). It got `NotImplemented`, took it as fatal and hung up one second
  in — on the console, "could not connect to the server". The answer is a
  RankingResult structure: empty list, zero total, a sinceTime. The two
  by-list reads (0x0C, 0x0D) share the shape and are answered the same way.

## 2026-09-19 — v0.2.21: Kirby's host opens its own room again

- **`ForceOpenParticipation` off for Kirby** (added in v0.2.17). Kirby creates
  its room closed and opens it only after its NAT check and station update.
  Forced open, the joiner arrived first, got the host's pre-NAT station
  (`port=1;CID=0`), probed nothing reachable until the host's 10 s lobby
  closed, and failed with 2618-0502. Searches ~2 s apart now meet; searches
  in the same ~2 s still each host.

## 2026-09-19 — v0.2.20: auto-matchmake tells the joiner who it joined

- **Auto-join notifications follow the measured server.** The joiner is announced
  (Participate 3001, count in Param3) to every member, itself included, then
  gets one Participate per member already in the room. Before, only the
  existing members heard anything: Kirby's joiner sat at Pia's notification
  wait for 10 s and ended its participation — "a network error has occurred".

## 2026-09-19 — v0.2.19: Kirby sends its ticket

- **Kirby's secure station is `prudps`.** Decompiled: the client picks its
  transport from the scheme, and only the `prudps` one (SecureStream) puts
  `[ticket][check-data]` in CONNECT; on `prudp` it connects with an empty
  payload and the PID had to be guessed from the latest login — a console
  and Ryujinx logging in the same second were both handed <user-b>'s account.

## 2026-09-19 — v0.2.18: Kirby rooms carry a session key

- **Kirby's Dream Buffet takes the create-time normalisation**
  (`FinalizeCreateSession`): a 32-byte session key, the host counted as the
  first participant, a start time. With v0.2.17 Ryujinx auto-joined the
  console's room, then sat 10 s without asking for any station and left
  (EndParticipation) — the room it joined had nothing for Pia to join with.

## 2026-09-19 — v0.2.17: Kirby's random match, password rooms kept private

- **Kirby's Dream Buffet opens its sessions at create** (`ForceOpenParticipation`).
  Its sessions arrive closed and open ~2 s later, so a console and Ryujinx
  searching a second apart each hosted a room (gid 2 and 3) instead of
  sharing one — the Mario Tennis Aces deadlock.
- **Auto-matchmake keeps password rooms private.** A searcher only lands in a
  room whose password equals its own: a random search never joins a password
  room, and a password search only finds its room. Before, any open room took
  any searcher.

## 2026-09-19 — v0.2.16: per-title /healthz

- **`GET /healthz` on every title's base+2**, the port ports.md always gave
  it and `-dump` already printed, but nothing listened on. Stdlib `net/http`,
  bound on all interfaces like the game ports. The body is the servers/nex
  shape: `ok <title> auth=<base>/tls secure=<base+1>/tls`, 200 only once both
  of the title's listeners are serving. A title whose auth or secure port
  failed to bind still answers healthz — 503, `down <title> auth=… secure=…
  not listening: <which>` — rather than refusing the connection, so a probe
  tells "title broken" from "process gone".
- A title whose secure port fails to bind now releases its auth port too
  (it used to stay bound with nothing accepting on it).
- **Two data races fixed** (`go test -race` was failing on main): a push
  from another connection bumped a connection's send sequence outside its
  write lock, and a disconnect removed a player from gatherings while other
  handlers read them. Each title's secure-stage dispatch and disconnect
  handling now share one mutex.

## 2026-09-18 — v0.2.15: Super Mario 3D World + Bowser's Fury and Kirby's Dream Buffet

- **Two titles registered from our own dumps.** Game server IDs and access
  keys were read out of the games' code, confirmed by the key and ID being
  loaded side by side (the shape MK8D shows): 3D World `2B306A00` /
  `67f366f2` (NEX 4.6.4, matching the Measured server), Kirby
  `25374500` / `6wVacwwe` (NEX 4.6.6). Ports 22370 and 22380.
- **`ParticipateDelay`**: 3D World's Participate pushes are held back 100 ms,
  the measured server's measured policy, alongside full-session join answers.
- **`switch-nexd -dump`** prints every title's resolved configuration.
- Neither title is console-verified; their first boots' NotImplemented lines
  are the backlog.

## 2026-09-18 — Minecraft online: the hostname, the route, and the first login

- **The last unrouted title found its name by booting.** Minecraft's NEX
  game-server hostname was recorded nowhere — not our repos or captures, not Kinnay's Game
  Server List (Switch table ends at ACNH). OpenPak's earlier Ryujinx port resolves
  the whole `.nintendo.net` suffix to the box, so the moment the game booted,
  its own DNS line named it: `g28c77600-lp1.s.n.srv.nintendo.net`. A temporary
  `g2` regexp route was live for the first attempt; the specific
  `nex-minecraft` router replaced it the same hour.
- **First real Minecraft login against switch-nexd** (2026-09-18 04:45 UTC):
  auth over the websocket carriage, method 0x2, `login pid=1900000001
  nickname="<user-a>"` — the account name resolved through nx-baas — and the
  station URL handed out for 22311. The secure-stage close line carries
  `37.228.205.45`, a real client address: the host-networking cutover doing
  exactly what it was for.
- **Open:** the client spent ~29 s and 17 frames on the secure stage without
  ever sending CONNECT, then quit with 2306-0502 — the ticket was never
  spent. Next: the secure websocket handshake on 22311.

## 2026-09-17 — Host networking: the server sees real client addresses

- **The punch gate was deployment, not matchmaking.** Behind rootless podman's
  published-port forwarder every client arrived from the gateway
  (`10.89.2.253`), so every public station named the server's own bridge and
  no mesh could form. The cutover: the quadlet now runs `Network=host` with
  no `PublishPort=` at all — switch-nexd binds its fifteen secure ports
  directly in the host netns, and inbound traffic reaches INPUT with the
  console's real source address (no DNAT in the path).
- **Consequences handled.** Traefik's fourteen nex routers retargeted from
  `switch-nexd:<base>` to `host.containers.internal:<base>` (Traefik stays a
  rootless container; `127.0.0.1` inside it is itself). nx-baas's internal
  identity API — port 20070, baked into the image and never published — now
  rides `PublishPort=127.0.0.1:20070:20070`, and `NX_INTERNAL_URL` became
  `http://127.0.0.1:20070`. The INPUT multiport ACCEPT was missing **22151**
  (MK8D secure) all along; published-port DNAT had been masking it, and the
  cutover would have blackholed exactly that port. Fixed and persisted.
- **Verified without a console:** nexprobe login through the whole chain
  (`login UNPROVEN (id token not proven)` — nx-baas really consulted, over the
  new loopback path), all fifteen SNIs serving the OpenPak wildcard leaf,
  direct secure dial on 22151 answering TLS, `/state` still writable, fifteen
  serving lines, no errors. What only a console proves: a secure-stage log
  line carrying a real client IP, and two clients exchanging probes. Backups:
  `dynamic.before-hostnet-20260917.yml`, both quadlets `.before-hostnet-20260917`.
- The image is unchanged — still `v0.2.12`, and that is the point: the freeze
  was never in the binary.

## 2026-09-17 — The joiner gets the session and the host's stations

- **A room join told the joiner nothing.** The join family (0x6D.0x07/0x1E/0x27)
  answered a bare `bool` where the client parses a full MatchmakeSession: no
  session key, no pre-join count, nothing to build a P2P mesh from. The
  emulator hung up two seconds after joining a room — matchmaking answered
  success on every call and the join still failed. MK8D now joins Clubhouse on
  the echo rule: the join answers the full session.
- **GetSessionURLs (0x15.0x29) was a stub.** The joiner's next question after
  joining is "where is the host", and the stub answered an empty list —
  nowhere to probe. It now serves the host's registered stations, lan then
  public, to participants only (PermissionDenied otherwise, SessionVoid for a
  gathering that is gone).
- Found by comparing the failing join in the production log against the AGPL
  `nex-protocols-common-go` measured shapes (the harness agreed with the
  server, which is exactly the failure mode the harness cannot catch). A new
  harness test walks create → room code → resolve → join → station handover
  with two clients and insists the joiner can parse every answer.
- First client pass against the fix (2026-09-17 20:03 UTC): the console's
  flows are clean; the Ryujinx fork created a room, drew code `BHFBB`, then
  froze waiting for a mesh it cannot form — with v0.2.12 the matchmaking
  answers are right and the UDP punch is what fails. The stations the server
  hands out are the addresses it sees: behind rootless podman's forwarder
  every public station names the gateway (10.89.2.253), not the client, and
  the client-reported LANs were container-range addresses. The cutover that
  gets real client addresses to the server (host networking, Traefik retarget
  and all) is written up as the gate in `next-session.md`.

## 2026-09-17 — Websocket carriage

- MK8D on the Ryujinx fork opens its TLS stream with a websocket upgrade; the
  server read the `GET` as a malformed frame and hung up, which the game shows
  as **2306-0502**. `prudplite` now sniffs the first bytes of a connection:
  an upgrade is answered and that connection is served one frame per binary
  message, anything else stays bare. Found with a guest-side SSL plaintext
  trace; the server log had been silent because nothing reached CONNECT.
- The server now logs a connection dropped before its first frame, and each
  upgrade — the silence was the expensive part of this one.
- `nexprobe -ws` opens the way the game does.
- Secure-stage recall works behind the production NAT. Rootless podman's port
  forwarder and Traefik each rewrite the source, so the login arrived from one
  internal address and the ticketless secure CONNECT from another, and the
  by-IP recall refused it every 2 s until the game gave up. When the source is
  a private address the title's latest login (30 s window) pairs them instead.
  Known ceiling, marked in the code: simultaneous logins to one title can
  cross. The real fix is getting client addresses to the server.
- **MatchmakeSession was parsed and written with the wrong nesting.** An
  inherited structure travels as siblings — Gathering under its own header,
  then MatchmakeSession's header over only its own fields. The server nested
  Gathering inside MatchmakeSession, so every session a console sent misparsed
  and `AutoMatchmakeWithParam_Postpone` answered `Core::InvalidArgument`; MK8D
  showed "a communication error has occurred" on create and join. Reader and
  writer were wrong symmetrically, which is why the harness never noticed. The
  parser is now bounded by each header's length (fields a newer client appends
  are skipped) and structure versions are echoed as the client sent them. Found
  by the diagnostics below on their first run.
- **MatchmakeParam is a structure, not a bare map.** It carries its own 5-byte
  header; read without it the header became a map count of 1024 and
  `CreateMatchmakeSessionWithParam` (0x6D/0x26) answered InvalidArgument. This
  is the "5 unexplained bytes" of the previous entry, explained.
- A call that ends in an error or no answer now logs its **full** request, not
  just the head: that is what the fix gets written from.
- **The room-code answer is a structure.** 0x6D/0x44 (and Smash's 0x37)
  answered the bare pair `(String, DateTime)`; the wire wants it under a
  structure header. MK8D left its freshly created room 0.2 s after reading the
  bare answer. Codes are now 5 characters, the length the measured server
  issues (ours was an invented 6), and resolve accepts the code bare or inside
  a header. Shape and length are measured facts; the code here is our own. A numeric-code guess was tried first and
  reverted once the measured server was read — read the measured server first.
- ZNC (`api-lp1.znc.srv.nintendo.net/v1/NX/getToken`, the voice-chat path) is
  called by MK8D after create and gets 404 from nx-baas. The measured server
  does not serve it either, so it is presumed non-fatal; recorded, not fixed.
- **The creator hears its own join.** After a successful create MK8D sat for
  exactly 10 s — no call, no P2P socket — then unregistered the gathering and
  showed a communication error. A create now pushes the creator its own
  Participate (3001) event before the answer, as Pretendo's common matchmaking
  does, and MK8D's created session goes back finalised (occupied by its host,
  keyed, timestamped). Two changes in one step, both from the same measured server
  behaviour; which one MK8D was waiting for is not separated.
- **Connections were tabled by client address alone.** Two players behind one
  NAT — in production, *every* player, since the forwarder rewrites the source
  — shared one slot: the newer connection evicted the older, pushes to the
  evicted player went nowhere, and the first to close deleted the other's
  entry. Keyed by address and port now. Found by the new test for the push
  above, whose host and joiner both dial from 127.0.0.1.
- **Catch-all diagnostics, for every title.** Each connection now ends with one
  line saying how: who hung up (client closed / idle timeout / DISCONNECT /
  malformed frame / handler panic, with the stack), at which stage, on which
  carriage, after how many frames and calls, and what the last call was. Each
  RMC call on both stages logs its outcome — `ok`, `ERROR 0x…` or `NO ANSWER
  SENT` — with the heads of request and answer, and an answer that could not be
  delivered is logged instead of dropped. A handler panic no longer takes the
  process down. Read the `closed` line first, then the `rmc` lines above it.
- The leaf certificate carries `IP:145.241.199.19`: the game dials the secure
  stage by raw IP and verifies the name.

## 2026-09-16 — Clubhouse Games: 51 Worldwide Classics

- New title `clubhouse-games-51` (010047700D540000, game server 2035BB00) on
  port block 22870 (allocated from the ports.md pool), access key `8c3661ff`.
  First title whose facts came from earlier research notes —
  provenance in `docs/parity.md`.
- `protocols/`: new `DSClubhouse` DataStore mode (GetPersistenceInfos answers
  empty + NotFound per slot — the "nothing saved yet" signal the game's
  account setup needs; GetMeta NotFound; everything else a bare empty list),
  plus two Env knobs: `FinalizeCreateSession` (create-time normalisation:
  bound, host-counted, 32-byte key, start time, active state, padded
  attributes, `@SR`/`@GIR` params) and `JoinEchoSession` (joins answer the
  full session with the pre-join participation count). The title also rides
  the existing forced-open-participation fix.
- Tests: create normalisation + browsability, join echo (new joiner hears the
  pre-join count, store counts the join, rejoin does not double-count), and
  the three DataStore answers.
- Deployed to quilted-waffle 2026-09-16: image rebuilt from the shipped
  arm64 binary, quadlet publishes 22871, Traefik SNI route
  `g2035bb00-lp1.s.n.srv.nintendo.net` → `switch-nexd:22870` (delta backup
  `dynamic.before-clubhouse-20260916.yml`), nftables ACCEPT + rules.v4
  persisted for 22871. Verified: 15/15 listeners, edge SNI handshake serves
  the `*.s.n.srv.nintendo.net` leaf, secure port 22871 reachable externally.
- Firewall closure: the cutover had left every switch-nexd secure port
  except MK8D's blocked at the host REJECT (auth via 443/SNI worked, secure
  direct-dial did not). One multiport ACCEPT for
  22111,22131,22141,22161,22171,22181,22191,22301,22311,22321,22331,22341,22351
  now sits before the REJECT (rules.v4 persisted); all 15 secure ports
  verified externally serving the OpenPak leaf.

## 2026-09-16 — deployed to quilted-waffle (all titles)

- `openpak-switch-nexd.service` quadlet + `localhost/switch-nexd:latest`
  image; Traefik SNI re-pointed for 13 titles (MHGU restored to its nex-go
  container); nex-go Switch-title services stopped and disabled.
- Verified: 14/14 listeners, 13/13 SNI handshakes through the edge with the
  OpenPak-issued leaf cert.
- Known gap carried over: minecraft has no SNI router in dynamic.yml (its
  game-server hostname was never recorded) — auth via the edge is unreachable
  for it until the hostname is identified and a router added.

## 2026-09-15 — Server parity for every Switch-era NEX title

- `protocols/` new package: SecureConnection, Utility (+AcquireTagId /
  UpdateCurrentUser), NAT traversal, Notification, Messaging/MessageDelivery,
  the matchmaking trio (0x15/0x32/0x6D) over a real in-memory gathering store
  with a full NEX4 MatchmakeSession codec, Ranking (scores + common data),
  SMB35's Ranking2 + MatchmakeReferee (round store, raw param echo), and LM3's
  0x79 presence stub. Response behaviour matched to the measured
  answers per title — `docs/parity.md` is the comparison record.
- `cmd/switch-nexd` serves 14 titles (all Switch-era NEX except MHGU, whose
  UDP transport stays on nex-go), with per-title protocol sets, MPS detail-
  find sanitisation, the MTA open-participation fix, ACNH 0x2C codeword
  updates, SMB35's 17-entry settings table / 16-byte source key / retail
  secure PID, and auth-stage TicketGranting methods 0x02 and 0x06.
- `rmc` gained the Switch byte-stream types (structure headers, strings,
  buffers, variants, result codes).
- Tests: session byte round-trip + header-framing fidelity, create→find
  flows, sanitisation and session-key preservation, SessionVoid/RoundNotFound
  errors, referee lifecycle, RegisterEx shape, 0x79 stub, SMM2 GetUsers shape,
  SMB35 settings table.

## 2026-09-15 — initial import

## 2026-09-16 — server parity pass

Closed all 43 findings from `docs/gap-report.md`.

Login and connection setup:
- `ValidateAndRequestTicketWithParam` (0x0A/0x06) now answers its own structure
  instead of the LoginEx body with a header on it — no retval, and the secure
  endpoint as a plain URL string plus the server time. The old shape was
  rejected as an invalid argument by every NEX 4.4+ title.
- `Login` (0x0A/0x01) shares the LoginEx answer.
- The login special-station field renders as `prudp:/`, not the bare word.
- `SecureConnection::Register` (0x0B/0x01) answers the same three fields as
  RegisterEx, and both hand back the CLIENT's public station — derived from the
  stations it reported, stamped with its PID and connection id — rather than our
  own secure address, which Pia cannot recognise as itself.
- Connection ids are per connection; they were all 1.
- `ReplaceURL` stores the re-reported station instead of discarding it.
- Public-station `type` and `Pa` follow each title's Pia version, and the
  station scheme is per title (`prudps` for MPS, Golf, ARMS, SMO).
- The IP-to-PID recall table survives a restart (`-recall-file`).

Wire fixes:
- `DataStore::NotFound` went out as 0x80069004; the measured value is
  0x80690004. The constant had two digits transposed.
- Ranking2 common data uses QBuffers for the Mii and binary blobs, and a
  zero-length string for an unnamed player.
- Ranking2 category settings write the reset month as a u16.
- `GetEstimateMyRank` is method 11, not 10.
- `GetCommonData` for an unknown player answers an empty structure, not an error.
- Referee `EndRoundWithoutReport` is method 5; method 4 is the partial report.
- Utility unique ids derive from the PID and answer a framed structure on
  methods 2 and 5.
- Integer settings and matchmake parameters are written in key order; Go's map
  iteration made both non-deterministic between calls.
- RMC protocol ids from 0x80 up escape to a u16.

New behaviour:
- Server-initiated pushes: participation, disconnect, ownership, host change,
  gathering teardown, referee round start, and the NAT `InitiateProbe` relay.
- `GetRelaySignatureKey` answers a well-formed "no relay offered".
- NAT properties are classified and incompatible peers are not seated together.
- Host migration on 0x15 (0x24/0x28/0x2A/0x2B/0x2C) and when an owner leaves.
- Room and arena codes (0x37/0x38/0x44/0x45) and tournaments (0x39/0x3F/0x42/0x43).
- `CustomGetSimplePlayingSession` (0x6D/0x3C) resolves a friend's gathering; it
  answered an empty list, which is why friend-joins failed.
- `FindMatchmakeSessionByParticipant` parses its structure header, and serves
  real results only for the title that uses it.
- Ranking 0x70 serves the measured server's method set, including common-data-by-PIDs.
- Mario Strikers' bring-up shape table on 0x6E/0x73/0x78/0x79/0x7A.
- DataStore stub shapes are per title.

Corrected `docs/parity.md`: the per-title table wrongly recorded eight
titles as having no Ranking, and SMB35's secure PID as a retail constant rather
than 2.
