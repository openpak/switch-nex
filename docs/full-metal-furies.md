# Full Metal Furies — 2026-10-12

Full Metal Furies (`010079300AD54000`, available update content 131072,
latest 327680 — the analysed dump is outdated) is registered in
`cmd/switch-nexd` as `full-metal-furies`. It shares the existing
transport, account resolution, matchmaking and NAT/notification
handlers. No new proprietary backend is introduced. This is an initial
profile for client testing, not gameplay-verified support.

## Client facts and provenance

The source is the owner's client executable extracted during the 88-game
sweep. Raw inputs and extraction tools remain outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/010079300AD54000/`
(`main.bin`, NSO). Only sanitized constants and instruction observations
are recorded here.

| Title | Application ID | Update content version | Game server ID | Public PRUDP access key | SDK |
| --- | --- | ---: | --- | --- | --- |
| Full Metal Furies | `010079300AD54000` | 131072 (latest 327680) | `2C82EE00` | `c093ff57` | NEX 4.5.0, Pia 5.11.3, MM-only |

- ID built into `w8` at `0x2d95c` (`movz w8, #0xee00`) / `0x2d960`
  (`movk w8, #0x2c82, lsl #16`), stored to the stack config slot at
  `0x2d964`; the key `c093ff57` (null-terminated rodata at `0x40a9f47`)
  is loaded by `adrp x1` at `0x2d968` plus `add` at `0x2d96c`. The two
  are built adjacently into the same client config, a weaker claim than
  the MK8D/3D World/Kirby same-call login pairs (same class as
  heroes-of-hammerwatch): the actual DNS name
  (`g2c82ee00-lp1.s.n.srv.nintendo.net`) and CONNECT signatures still
  need a game run. Neither a hex-shaped literal nor a linked SDK alone
  established the pair; re-verified 2026-10-12 against the local
  `main.bin` (segments at NSO header `0x10`/`0x20`/`0x30`).
- Addresses are virtual addresses in the decompressed `main`.
- Markers: `SDK MW+Nintendo+NEX-4_5_0-`, `NEX_MM`/`NEX_R2`/`NEX_RK`
  banners, `SDK MW+Nintendo+Pia-5_11_3`, `MatchmakeExtension`,
  `NexNetwork`, `NgsFacade` RTTI, LDN, plus the runtime-formatted
  `g%08x-%%.s.n.srv.nintendo.net` auth hostname and
  `g%08x.%%.p.srv.nintendo.net` Pia discovery hostname. Host strings
  are the generic `n/p/s.n.srv.nintendo.net` only; no baked
  `g<id>-lp1` hostname.
- The `NEX_R2`/`NEX_RK` banners do not imply linked services: no
  `RankingProtocolClient`, `Ranking2ProtocolClient`,
  `MatchmakeReferee`, `DataStoreProtocolClient`, `UtilityProtocol` or
  `MessageDelivery` strings exist in the binary. The single `Ranking`
  hit is `LanMakeHostCandidateRanking`, a LAN/mesh job name — the same
  trap Duck Game's analysis avoided. Only MatchmakeExtension with
  NATTraversal and SecureConnection protocol strings is linked.
- Online co-op is 1–4 players (Nintendo storefront).

## Initial profile

- `prudps` secure station URLs, so players present their own tickets even
  when they share an address. The endpoint also supports ticketless
  clients; the first game run must establish which secure path is used.
- Legacy public station shape (`type=3`, no `Pa`): Pia 5.11.3 predates
  the 5.19 cutover, so the pre-5.19 shape is the measured-era choice.
- A separate matchmaking store, normalized creation (creator counted,
  32-byte session key, start time), full-session join replies and
  participant recaps. The echo shape is provisional: a strict reader
  rejects the bare boolean with 2306-0116, as risk-of-rain-returns
  showed. These reuse the fixes measured on other OpenPak NEX titles;
  their exact applicability to Full Metal Furies remains to be
  validated.
- No Ranking, Ranking2, referee relay, DataStore service, forced-open
  sessions, parameter stamping (`@SR`/`@GIR`) or title-specific settings
  without client evidence. FinalizeParams stays off: nothing measured
  says this title wants them, and ACNH breaks with them.

## Deployment bundle

| Title selector | Auth | Secure | Health | Auth SNI |
| --- | ---: | ---: | --- | --- |
| `full-metal-furies` | 23120 | 23121 | 23122 | `g2c82ee00-lp1.s.n.srv.nintendo.net` |

The block follows the Heroes of Hammerwatch block (23110) and was unused
in the workspace port plan. Routing follows the existing `console`
entrypoint, passthrough TLS and PROXY v2. The current wildcard NEX
certificate covers the SNI name.

- `deploy/make-quadlets.sh <image-tag> <output-dir>` generates the title's
  unit with its own recall/state directory, host networking and shared
  identity environment. Install only the new unit for this title.
- Merge `deploy/nex-full-metal-furies.yml` into the existing Traefik
  TCP maps; preserve unrelated routers/services and back up the old
  configuration.
- Admit secure TCP port **23121** at the provider and before the host's
  INPUT rejection. Health port **23122** is for the internal
  status/voice network; do not expose it publicly.
- Add the health endpoint to the existing voice-gathering URL list and
  catalogue/server-health configuration. Catalogue state stays out until
  the relevant flow is game-verified.
- Re-extract the current update (content 327680) before deploying: the
  analysed dump is outdated and the pair must be re-confirmed against
  it. A local registration alone does not install a route or start a
  production container.

## Validation and next observations

The TLS test harness covers the independently supplied client access key,
both login methods, ticketed secure connects for distinct players on one
address, Register's legacy public-station shape, create/browse/join and
host stations, the explicit-join participant recap, and dispatch through
the title's own TLS route.

After deployment, enter the game's online menu and record the actual SNI,
login method, station URL and first RPCs — in particular whether the
dialled SNI matches `g2c82ee00-lp1` and whether the CONNECT signature
verifies against `c093ff57`. For the first failures distinguish
routing/signatures, reply parsing, unimplemented calls, session discovery
and P2P. Finish with host/join in both directions on separate machines
and a full four-player run before marking the title playable.
