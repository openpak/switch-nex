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
- Re-extract the current update (content 327680) before relying on
  this pair long-term: the analysed dump is outdated. A local
  registration alone does not install a route or start a production
  container.

**Deployed 2026-10-12 ~22:15 UTC** (local arm64 binary mount, not a
published release). The block sits inside the planned 23000–23499
Switch-NEX range.

- `deploy/nex-full-metal-furies.yml` merged into the Traefik TCP maps
  (backup `dynamic.before-fmf-20261012.yml` on the host); exactly one
  router plus one service added, unrelated maps preserved. Existing
  console entrypoint, passthrough TLS and PROXY v2. The SNI route
  serves the wildcard `*.s.n.srv.nintendo.net` leaf against the
  OpenPak CA.
- `openpak-switch-full-metal-furies` quadlet: pinned-base image with
  the local arm64 binary mounted over `/app/switch-nexd`
  (`~/openpak/experiments/full-metal-furies-20261012/switch-nexd-arm64`,
  SHA-256
  `9423c8966855c27ec4c5383d89caad6a4ea5bd28fa2d18f968dafaa6cd640cd5`),
  host networking, own recall/state directory, shared identity
  environment.
- Host INPUT accepts secure TCP **23121** (own rule with comment,
  before the REJECT, persisted to `/etc/iptables/rules.v4`). Health
  **23122** answers `ok full-metal-furies` internally; not exposed
  publicly.
- Rollback: remove the router/service, stop the quadlet, drop the
  23121 rule. Routing/environment backups stay on the host.

Still pending: voice-gathering URL list, catalogue/server-health
configuration (catalogue stays out until game-verified), and the release
cut that replaces the binary mount. A local registration plus a healthy
listener does not establish playability.

## Console verification — 2026-10-13 (Harbor ↔ Switch, owner-played)

- **Owner confirmation:** "game was played between harbor and switch."
- **Host evidence:** two proved consoles logged in (title
  `010079300AD54000`, version `00050000` — the current update, content
  327680 — no `UNPROVEN` fallback) and held ticketed secure sessions
  at 10:00:37/10:00:46 UTC over the websocket carriage, method 0x6.
  The SNI (`g2c82ee00-lp1`) and CONNECT key (`c093ff57`) are now
  console-measured: both logins passed signature verification with no
  `bad signature` line. Host created gid `914033` (`max=4 open=true`)
  at 10:00:51; an earlier browse found 0 sessions and the post-create
  browse found 1.
- **Join evidence:** joiner browsed, joined gid `914033` at 10:01:02
  (`count=1`, full-session echo with key), fetched host stations
  (`0x15/0x29`), both sides ran `replaceURL`, and NAT probe requests
  crossed in both directions with `InitiateProbe` pushes each way;
  the punch succeeded (`rtt=102ms`). Session-part updates
  (`0x6D/0xB`) followed through 10:01:33. No RPC error or
  unimplemented call appeared in the inspected host/join/NAT window.
- **Teardown:** host closed its connection at 10:02:33; the joiner
  received the leave notifications (3007/4000) and idled out at
  10:04:33. Clean lifecycle, no ghost session.
- **Still unverified:** a sustained full co-op run to results and
  leave/rejoin. The pair's static provenance (adjacent-config,
  outdated dump) is now superseded by this trace.

## Validation and next observations

Local validation 2026-10-12: the full `GOWORK=off go test -race ./...`
suite and `GOWORK=off go vet ./...` pass.

Production validation 2026-10-12 (~22:15 UTC): public SNI route, both
login methods, ticketed logins and legacy `type=3` registration all
pass through the deployed container. Both methods mint tickets
(`UNPROVEN (no token)`, the synthetic-probe path); no CONNECT
signature failure against `c093ff57` — external confirmation that the
static key candidate signs correctly.

Next: boot the game, enter the online menu and record the actual SNI,
login method, station URL and first RPCs — in particular whether the
dialled SNI matches `g2c82ee00-lp1` and whether the CONNECT signature
verifies against `c093ff57`. For the first failures distinguish
routing/signatures, reply parsing, unimplemented calls, session discovery
and P2P. Finish with host/join in both directions on separate machines
and a full four-player run before marking the title playable.
