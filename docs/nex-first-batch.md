# First new NEX titles — 2026-10-12

Castle Crashers Remastered, Nobody Saves the World and PICO PARK are registered
in `cmd/switch-nexd`. They share the existing transport, account resolution,
matchmaking and NAT/notification handlers. No new proprietary backend is introduced.
These are initial profiles for client testing, not gameplay-verified support.

**Deployed 2026-10-12, v0.3.32.** All three generated per-title quadlets are
installed and active. The running image matches the published release digest
`sha256:aea0fcfc35565b18d9bb9115aa04adca7752d89cd0edb0df8c8653739655937d`.
The [release workflow](https://github.com/openpak/switch-nex/actions/runs/38256450958)
passed tests/vet and published both architectures.

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
  32-byte session key), full-session join replies and participant recaps. PICO
  PARK briefly used a boolean join reply after its first console trace retried
  JoinMatchmakeSessionEx three times without requesting participant stations,
  but four console runs then showed the game accepting the join at the RPC
  layer and never following up — no station fetch, no P2P — and a bare bool
  carries no session key or stations for it to start P2P with. It is back on
  the session echo while a successful gameplay run is still awaited.
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

Production verification on 2026-10-12:

- Auth routes for all three SNI names accept the title's signed PRUDPLite
  handshake on public port 443.
- Public secure ports 23061/23071/23081 are reachable and pass TLS certificate
  verification against the OpenPak CA. Existing provider ingress already admits
  them; only the host's secure-port ACCEPT rule needed adding.
- A synthetic external client exercises both login methods, decrypts the
  returned ticket, presents it to each public secure listener and successfully
  registers distinct players behind one address. These probes use unproven
  synthetic login names, not real console BAAS tokens. Castle Crashers'
  Ranking2 common-data call also passes externally.
- Each health endpoint returns 200 locally and from the website container's
  network. The website has loaded the three appended voice-gathering URLs.
- Catalogue entries are `alpha` / `NEX`, with gameplay verification pending.
  The website's existing hard-coded server-health display map is unchanged;
  the new health endpoints were checked directly.
- PICO PARK's first two-player trace (14:30–14:32 UTC) reached create, browse,
  and join successfully, but the joiner retried JoinMatchmakeSessionEx three
  times and no GetParticipantsURLs call followed. Four evening runs then showed
  joins succeeding at the RPC layer with either reply shape while the game
  never fetched stations or started P2P ("failed to connect to the lobby"),
  and abandoned rooms lingering under ghost owners. Leave teardown, one-lobby
  seating and the session-echo join reply are in; this needs a fresh
  two-console gameplay run.

Installed quadlets are `openpak-switch-castle-crashers-remastered.container`,
`openpak-switch-nobody-saves-the-world.container` and
`openpak-switch-pico-park.container` under the host's user unit directory.
Each has a separate state/recall directory and participates in the existing
idle-aware release updater. Routing/environment backups stay on the host;
raw probe artifacts remain in `scratch/nex-first-batch-20261012/`.

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
