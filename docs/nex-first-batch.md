# First new NEX titles — 2026-10-12

Castle Crashers Remastered, Nobody Saves the World and PICO PARK are registered
in `cmd/switch-nexd`. They share the existing transport, account resolution,
matchmaking and NAT/notification handlers. No new proprietary backend is introduced.
These are initial profiles for client testing, not gameplay-verified support.

## Client facts and provenance

The source is the owner's updated client executable extracted during the
88-game sweep. Raw inputs and extraction tools remain outside the repository
in `~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/<title-id>/`.
Only sanitized constants and instruction observations are recorded here.

| Title | Application ID | Update content version | Game server ID | Public PRUDP access key | SDK modules |
| --- | --- | ---: | --- | --- | --- |
| Castle Crashers Remastered | `010001300D14A000` | 458752 | `2458D100` | `effc17b3` | NEX 4.6.3 MM/R2/VS |
| Nobody Saves the World | `0100787014460000` | 196608 | `2CA9E100` | `5a8be9a0` | NEX 4.6.6 MM/VS |
| PICO PARK | `01004EA00DF70000` | 524288 | `2C3C9A00` | `c02db590` | NEX 4.6.2 MM/VS |

- Castle Crashers: key loaded into `x3` at `0x193384`, ID built in `w2` at
  `0x19338c` / `0x193390`, both arguments passed at `0x1933a8`.
- Nobody Saves the World: key loaded into `x3` at `0x126a78`, paired ID in
  `w2`, call at `0x126a98`.
- PICO PARK: ID built at `0x2162c` / `0x21630`, stored at `[sp]`; key loaded
  at `0x21638`, stored at `[sp+8]`; the shared config is passed in `x1`
  at `0x21648`.

Addresses are virtual addresses in the decompressed updated `main`. These
pairings are high-confidence static evidence; the actual DNS names and
CONNECT signatures still need a game run. Neither a hex-shaped literal nor
a linked SDK alone established the pairs.

## Initial profiles

All three start with:

- `prudps` secure station URLs, so players present their own tickets even
  when they share an address. The existing endpoint also supports ticketless
  clients; the first game run must establish which secure path is used.
- Legacy public station shape (`type=3`, no `Pa`): all three link the NEX
  virtual-socket module without a Pia SDK banner. This is an initial profile
  choice based on the existing bare-NEX path, not a measured requirement.
- A separate matchmaking store, normalized creation (creator counted,
  32-byte session key), full-session join replies and participant recaps.
  These reuse the fixes measured on other OpenPak NEX titles; their exact
  applicability to each new game remains to be validated.
- No forced-open sessions, parameter stamping, extra DataStore service,
  referee relay or title-specific settings without client evidence.

Castle Crashers additionally enables the shared Ranking2 service because
its executable links NEX_R2. Boards/common data use the existing in-memory
store and do not survive a restart. Gameplay and leaderboard verification
are separate acceptance checks.

## Deployment bundle

| Title selector | Auth | Secure | Health | Auth SNI |
| --- | ---: | ---: | ---: | --- |
| `castle-crashers-remastered` | 23060 | 23061 | 23062 | `g2458d100-lp1.s.n.srv.nintendo.net` |
| `nobody-saves-the-world` | 23070 | 23071 | 23072 | `g2ca9e100-lp1.s.n.srv.nintendo.net` |
| `pico-park` | 23080 | 23081 | 23082 | `g2c3c9a00-lp1.s.n.srv.nintendo.net` |

The three blocks were unused in the workspace's port plan and had no listening
TCP sockets on the production host when checked on 2026-10-12. Routing follows
the existing `console` entrypoint, passthrough TLS and PROXY v2. The current
wildcard NEX certificate covers all three SNI names.

- `deploy/make-quadlets.sh <image-tag> <output-dir>` generates each title's
  unit with its own recall/state directory, host networking and shared identity
  environment. Install only these three new units for this batch.
- Merge `deploy/nex-first-batch.yml` into the existing Traefik TCP maps;
  preserve unrelated routers/services and back up the old configuration.
- Admit secure TCP ports **23061, 23071, 23081** at the provider and before
  the host's INPUT rejection. Health ports **23062, 23072, 23082** are for
  the internal status/voice network; do not expose them publicly.
- Add the health endpoints to the existing voice-gathering URL list and
  catalogue/server-health configuration. Catalogue state stays alpha until
  the relevant flow is game-verified.
- Use the existing release workflow for the image and follow
  `docs/deploy.md`. A local registration alone does not install a route or
  start a production container.

## Validation and next observations

The TLS test harness covers independently supplied client access keys, both
login methods, ticketed secure connects for distinct players on one address,
Register's public-station shape, create/browse/join and host stations. The
existing explicit-join regression now also covers all three new profiles and
requires notifications about both the host and the joiner. Castle Crashers'
Ranking2 endpoint is exercised through TLS dispatch.

Validation on 2026-10-12: the full `GOWORK=off go test -race ./...` suite
and `GOWORK=off go vet ./...` pass. A further focused race run includes
the new browse/host-station checks. The generated quadlets, routing fragment
and all 25 title port blocks were checked for consistency and collisions.

After deployment, enter each game's online menu and record the actual SNI,
login method, station URL and first RPCs. For the first failures distinguish
routing/signatures, reply parsing, unimplemented calls, session discovery and
P2P. Finish with host/join in both directions on separate machines and a full
gameplay session before marking any title playable.
