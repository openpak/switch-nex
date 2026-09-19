# Changelog — switch-nex

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
  `67f366f2` (NEX 4.6.4, matching the Reference reference), Kirby
  `25374500` / `6wVacwwe` (NEX 4.6.6). Ports 22370 and 22380.
- **`ParticipateDelay`**: 3D World's Participate pushes are held back 100 ms,
  the reference's measured policy, alongside full-session join answers.
- **`switch-nexd -dump`** prints every title's resolved configuration.
- Neither title is console-verified; their first boots' NotImplemented lines
  are the backlog.

## 2026-09-18 — Minecraft online: the hostname, the route, and the first login

- **The last unrouted title found its name by booting.** Minecraft's NEX
  game-server hostname was recorded nowhere — not our repos or captures, not
  the earlier server's (their console agent covers the case with a `g2*` hosts
  wildcard; their public repos carry no per-title table), not Kinnay's Game
  Server List (Switch table ends at ACNH). The an earlier Ryujinx fork fork resolves
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
  `nex-protocols-common-go` reference shapes (the harness agreed with the
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
  bare answer. Codes are now 5 characters, the length the working reference
  issues (ours was an invented 6), and resolve accepts the code bare or inside
  a header. Shape and length are facts read from earlier-nex's matchmaking
  handlers; the code here is our own. A numeric-code guess was tried first and
  reverted once the reference was read — read the reference first.
- ZNC (`api-lp1.znc.srv.nintendo.net/v1/NX/getToken`, the voice-chat path) is
  called by MK8D after create and gets 404 from nx-baas. The reference stack
  does not serve it either, so it is presumed non-fatal; recorded, not fixed.
- **The creator hears its own join.** After a successful create MK8D sat for
  exactly 10 s — no call, no P2P socket — then unregistered the gathering and
  showed a communication error. A create now pushes the creator its own
  Participate (3001) event before the answer, as Pretendo's common matchmaking
  does, and MK8D's created session goes back finalised (occupied by its host,
  keyed, timestamped). Two changes in one step, both from the same reference
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
  First title whose reference facts were taken straight from a public
  earlier network repo — provenance in `docs/legacy-parity.md`.
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

## 2026-09-15 — earlier parity for every Switch-era NEX title

- `protocols/` new package: SecureConnection, Utility (+AcquireTagId /
  UpdateCurrentUser), NAT traversal, Notification, Messaging/MessageDelivery,
  the matchmaking trio (0x15/0x32/0x6D) over a real in-memory gathering store
  with a full NEX4 MatchmakeSession codec, Ranking (scores + common data),
  SMB35's Ranking2 + MatchmakeReferee (round store, raw param echo), and LM3's
  0x79 presence stub. Response behaviour matched to the measured reference
  answers per title — `docs/legacy-parity.md` is the comparison record.
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

## 2026-09-16 — reference parity pass

Closed all 43 findings from `docs/legacy-gap-report.md`.

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
- Ranking 0x70 serves the reference's method set, including common-data-by-PIDs.
- Mario Strikers' bring-up shape table on 0x6E/0x73/0x78/0x79/0x7A.
- DataStore stub shapes are per title.

Corrected `docs/legacy-parity.md`: the per-title table wrongly recorded eight
titles as having no Ranking, and SMB35's secure PID as a retail constant rather
than 2.
