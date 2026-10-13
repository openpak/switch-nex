# Heroes of Hammerwatch — Ultimate Edition — 2026-10-12

Heroes of Hammerwatch — Ultimate Edition (`0100D2B00BC54000`, content
524288 current) is registered in `cmd/switch-nexd` as
`heroes-of-hammerwatch`. It shares the existing transport, account
resolution, matchmaking and NAT/notification handlers. No new proprietary
backend is introduced. This is an initial profile for client testing, not
gameplay-verified support.

## Client facts and provenance

The source is the owner's updated client executable extracted during the
88-game sweep. Raw inputs and extraction tools remain outside the repository
in `~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/0100D2B00BC54000/`
(`main.bin`, NSO). Only sanitized constants and instruction observations
are recorded here.

| Title | Application ID | Update content version | Game server ID | Public PRUDP access key | SDK |
| --- | --- | ---: | --- | --- | --- |
| Heroes of Hammerwatch — Ultimate Edition | `0100D2B00BC54000` | 524288 | `2075E100` | `1203b5eb` | NEX 4.6.3, Pia 5.24.0, MM |

- ID built into `w8` at `0x7c7d44` (`mov w8, #0xe100`) / `0x7c7d48`
  (`movk w8, #0x2075, lsl #16`) and stored at `[sp+0x60]`; the key
  `1203b5eb` sits at `0xa615b3`, loaded by `adrp x1` at `0x7c7d5c`
  (page `0xa61000`) plus `add x1, x1, #0x5b3`. The two are built
  adjacently into the same client config, a weaker claim than the
  MK8D/3D World/Kirby same-call login pairs: the CONNECT signatures
  still need a game run. Neither a hex-shaped literal nor a linked SDK
  alone established the pair.
- **DNS-confirmed 2026-10-12 21:58:02 UTC:** the retail Switch dialled
  `g2075e100-lp1.s.n.srv.nintendo.net`, so the derived SNI matches the
  real one. The game opens with a websocket upgrade (`GET /` with
  `Upgrade`/`Sec-Websocket-*`, empty UA) — the carriage switch-nexd
  already speaks per connection (`prudplite/websocket.go`, as on MK8D).
  The name is currently unrouted, so the attempt ended at the nx-baas
  catch-all 404. No route, quadlet or catalogue entry exists yet.
- The same run's aauth for `0100D2B00BC54000` at `00080000` returned
  200: the installed v524288 equals the gated latest, so no version
  exception is needed for the Switch. A Harbor install at `00070000`
  queried `update required` in the same window; it needs
  `NX_VERSION_GATE_OVERRIDES=0100D2B00BC54000=00070000` if it joins
  the test.
- Addresses are virtual addresses in the decompressed updated `main`.
- Markers: `SDK MW+Nintendo+NEX-4_6_3-`, `SDK MW+Nintendo+Pia-5_24_0`,
  `MatchmakeExtension`, `NexNetwork`, `NgsFacade` RTTI, LDN. Host
  strings are the generic `n/p/s.n.srv.nintendo.net` only; no baked
  `g<id>-lp1` hostname.
- The auth hostname is formatted at runtime:
  `g%08x-%%.s.n.srv.nintendo.net`, so the server ID travels as an
  integer constant. Online co-op is 1–4 players (Nintendo storefront).
- The binary links MatchmakeExtension with NATTraversal and
  SecureConnection protocol strings. The `Ranking` hits are a
  LAN/mesh job name (`LanMakeHostCandidateRanking`) and NEX DDL
  boilerplate, not a linked Ranking service. No Ranking2, Referee or
  DataStore.

## Initial profile

- `prudps` secure station URLs, so players present their own tickets even
  when they share an address. The endpoint also supports ticketless
  clients; the first game run must establish which secure path is used.
- Modern public station shape (`type=0x0B`, `Pa` present): Pia 5.24.0 is
  past the 5.19 cutover, so the post-5.19 shape is the measured-era
  choice (Golf measured this the same way on Pia 5.33.0).
- A separate matchmaking store, normalized creation (creator counted,
  32-byte session key, start time), full-session join replies and
  participant recaps. These reuse the fixes measured on other OpenPak NEX
  titles; their exact applicability to Hammerwatch remains to be
  validated.
- No Ranking, Ranking2, referee relay, DataStore service, forced-open
  sessions, parameter stamping (`@SR`/`@GIR`) or title-specific settings
  without client evidence. FinalizeParams stays off: nothing measured
  says this title wants them, and ACNH breaks with them.

## Deployment bundle

| Title selector | Auth | Secure | Health | Auth SNI |
| --- | ---: | ---: | --- | --- |
| `heroes-of-hammerwatch` | 23110 | 23111 | 23112 | `g2075e100-lp1.s.n.srv.nintendo.net` |

**Deployed 2026-10-12 in v0.3.42** (`openpak-switch-heroes-of-hammerwatch`
quadlet, image `ghcr.io/openpak/switch-nex:latest` = 0.3.42 digest
`sha256:0545683850846cfefbc8ed70a0042977b7ccaee0f9f0d351416c37efb2614e4e`,
own recall/state directory, host networking, shared identity
environment, drain-update managed). The block follows the Duck Game
block (23100). Routing follows the existing `console` entrypoint,
passthrough TLS and PROXY v2.

- `deploy/nex-heroes-of-hammerwatch.yml` merged into the Traefik TCP maps
  (backup `dynamic.before-hoh-20261012.yml` on the host); exactly one
  router plus one service added, unrelated maps preserved.
- Host INPUT accepts secure TCP **23111** (own rule with comment, before
  the REJECT, persisted to `/etc/iptables/rules.v4`). Health **23112**
  stays internal-only. Provider ingress already admitted the port:
  the external probes below reached it.
- Health `23112` appended to the host `VOICE_GATHERING_URLS`
  (backup `.env.before-hoh-20261012`); the unit was restarted with it.

Production verification 2026-10-12 (~22:10 UTC):

- SNI route completes TLS against the OpenPak wildcard
  (`*.s.n.srv.nintendo.net`), not a catch-all per-hostname cert.
- A synthetic external client over the game's own websocket carriage
  passes both login methods (2 and 6), ticketed secure connects and
  registration; the server log shows `login UNPROVEN` for the unsigned
  probe token, proving the nx-baas identity path is live.
- The CONNECT handshake accepts the `1203b5eb` access key: the key is
  now cryptographically confirmed, not just a rodata candidate.
- Direct secure-port TLS negotiates externally; `/healthz` answers
  `ok heroes-of-hammerwatch` on host loopback.

Still pending: website voice-gathering URL list and
catalogue/server-health configuration (catalogue stays out until
game-verified). Rollback: remove the router/service, stop/remove the
quadlet, drop the 23111 rule. Routing/environment backups stay on the
host.

## First retail trace — 2026-10-13 ~07:16 UTC

The owner hosted from the retail Switch (v524288):

- Proved login on method 0x6 (no `UNPROVEN`), version `00080000`;
  websocket carriage end to end.
- Register ok, NAT moderate, `replaceURL` stored the host's post-NAT
  stations (2 station(s)).
- `matchmake: created gid=876463 max=4 open=false` via
  MatchmakeExtension 0x6D/0x26, followed by steady 0x6D traffic
  (methods 0x1/0x2/0xB, all ok) — session attribute updates, no
  `NotImplemented`, no errors, session still live at the last look.
- The session was created closed (`open=false`); if a second console
  cannot browse to it, that flag is the first suspect. Joining from a
  second machine is the next run.

## Validation and next observations

The TLS test harness covers both login methods, ticketed secure connects
for distinct players on one address, Register's modern public-station
shape, create/browse/join and host stations, the explicit-join
participant recap, and dispatch through the title's own TLS route.

Now re-enter the game's online menu: the dialled SNI and carriage are
already known, so watch for the login method the game uses, its station
URL shape and first RPCs. For the first failures distinguish
reply parsing, unimplemented calls, session discovery and P2P. Finish
with host/join in both directions on separate machines and a full
four-hero run before marking the title playable.
