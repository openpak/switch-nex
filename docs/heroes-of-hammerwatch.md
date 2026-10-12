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

The block follows the Duck Game block (23100) and was unused in the
workspace port plan. Routing follows the existing `console` entrypoint,
passthrough TLS and PROXY v2. The current wildcard NEX certificate covers
the SNI name.

- `deploy/make-quadlets.sh <image-tag> <output-dir>` generates the title's
  unit with its own recall/state directory, host networking and shared
  identity environment. Install only the new unit for this title.
- Merge `deploy/nex-heroes-of-hammerwatch.yml` into the existing Traefik
  TCP maps; preserve unrelated routers/services and back up the old
  configuration.
- Admit secure TCP port **23111** at the provider and before the host's
  INPUT rejection. Health port **23112** is for the internal
  status/voice network; do not expose it publicly.
- Add the health endpoint to the existing voice-gathering URL list and
  catalogue/server-health configuration. Catalogue state stays out until
  the relevant flow is game-verified.
- Use the existing release workflow for the image and follow
  `docs/deploy.md`. A local registration alone does not install a route or
  start a production container.

## Validation and next observations

The TLS test harness covers the independently supplied client access key,
both login methods, ticketed secure connects for distinct players on one
address, Register's modern public-station shape, create/browse/join and
host stations, the explicit-join participant recap, and dispatch through
the title's own TLS route.

After deployment, enter the game's online menu and record the actual SNI,
login method, station URL and first RPCs — in particular whether the
dialled SNI matches `g2075e100-lp1` and whether the CONNECT signature
verifies against `1203b5eb`. For the first failures distinguish
routing/signatures, reply parsing, unimplemented calls, session discovery
and P2P. Finish with host/join in both directions on separate machines
and a full four-hero run before marking the title playable.
