# Monopoly for Nintendo Switch — 2026-10-12

Monopoly for Nintendo Switch (`01007430037F6000`, update 1.0.5 / content
327680) is registered in `cmd/switch-nexd` as `monopoly`. It shares the
existing transport, account resolution, matchmaking and NAT/notification
handlers. No new proprietary backend is introduced. This is an initial
profile for client testing, not gameplay-verified support.

## Client facts and provenance

The source is the owner's updated client executable extracted during the
88-game sweep. Raw inputs and extraction tools remain outside the repository
in `~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/01007430037F6000/`.
Only sanitized constants and instruction observations are recorded here.

| Title | Application ID | Update content version | Game server ID | Public PRUDP access key | SDK |
| --- | --- | ---: | --- | --- | --- |
| Monopoly for Nintendo Switch | `01007430037F6000` | 327680 | `2E041D00` | `65c92799` | NEX 4.3.1, Pia 5.9.0, MM/R2 |

- Key loaded into `x3` at `0x560370`, paired ID in `w2`, call at `0x560394`
  (same-call pair, the MK8D/3D World/Kirby pattern).
- Addresses are virtual addresses in the decompressed updated `main`. The
  pairing is high-confidence static evidence; the actual DNS name
  (`g2e041d00-lp1.s.n.srv.nintendo.net`) and CONNECT signatures still need a
  game run. Neither a hex-shaped literal nor a linked SDK alone established
  the pair.
- The binary links MatchmakeExtension, Ranking, Ranking2, MatchmakeReferee,
  DataStore, NATTraversal and SecureConnection, plus Ubisoft's own HTTPS
  (`public-ubiservices.ubi.com`: profiles, challenges, leaderboards,
  events). The Ubisoft service is a separate backend and stays out of scope
  for this NEX profile; if the game gates online entry on it, that is the
  next trace to record.

## Initial profile

- `prudps` secure station URLs, so players present their own tickets even
  when they share an address. The endpoint also supports ticketless
  clients; the first game run must establish which secure path is used.
- Legacy public station shape (`type=3`, no `Pa`): Pia 5.9.0 predates the
  5.19 cutover, so the pre-5.19 shape is the measured-era choice.
- A separate matchmaking store, normalized creation (creator counted,
  32-byte session key, start time), full-session join replies and
  participant recaps. These reuse the fixes measured on other OpenPak NEX
  titles; their exact applicability to Monopoly remains to be validated.
- Ranking, Ranking2, MatchmakeReferee and an empty-list DataStore are
  registered because the binary links those modules. Boards, referee
  rounds and datastore records use the existing in-memory stores and do
  not survive a restart. Gameplay, leaderboard and Ubisoft-service
  verification are separate acceptance checks.
- No forced-open sessions, parameter stamping (`@SR`/`@GIR`), hold-for-open
  or title-specific settings without client evidence. FinalizeParams stays
  off: nothing measured says this title wants them, and ACNH breaks with
  them.

## Deployment bundle

| Title selector | Auth | Secure | Health | Auth SNI |
| --- | ---: | ---: | ---: | --- |
| `monopoly` | 23090 | 23091 | 23092 | `g2e041d00-lp1.s.n.srv.nintendo.net` |

The block follows the three first-batch blocks (23060/23070/23080) and was
unused in the workspace port plan. Routing follows the existing `console`
entrypoint, passthrough TLS and PROXY v2. The current wildcard NEX
certificate covers the SNI name.

- `deploy/make-quadlets.sh <image-tag> <output-dir>` generates the title's
  unit with its own recall/state directory, host networking and shared
  identity environment. Install only the new unit for this title.
- Merge `deploy/nex-monopoly.yml` into the existing Traefik TCP maps;
  preserve unrelated routers/services and back up the old configuration.
- Admit secure TCP port **23091** at the provider and before the host's
  INPUT rejection. Health port **23092** is for the internal
  status/voice network; do not expose it publicly.
- Add the health endpoint to the existing voice-gathering URL list and
  catalogue/server-health configuration. Catalogue state stays alpha until
  the relevant flow is game-verified.
- Use the existing release workflow for the image and follow
  `docs/deploy.md`. A local registration alone does not install a route or
  start a production container.

## Validation and next observations

The TLS test harness covers the independently supplied client access key,
both login methods, ticketed secure connects for distinct players on one
address, Register's legacy public-station shape, create/browse/join and
host stations, the explicit-join participant recap, and Ranking2
common-data through TLS dispatch.

After deployment, enter the game's online menu and record the actual SNI,
login method, station URL and first RPCs. Note in particular whether it
calls the Ubisoft hosts before or after NEX login, and whether any
Ubisoft call gates entry. For the first failures distinguish
routing/signatures, reply parsing, unimplemented calls, session discovery
and P2P. Finish with host/join in both directions on separate machines
and a full game before marking the title playable.
