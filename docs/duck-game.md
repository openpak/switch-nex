# Duck Game — 2026-10-12 (scaffold, trace pending)

Duck Game (`0100750001C70000`, update 1.0.4 / content 262144) is registered
in `cmd/switch-nexd` as `duck-game`. It shares the existing transport,
account resolution, matchmaking and NAT/notification handlers. No new
proprietary backend is introduced. This is a local-only scaffold for client
testing, not gameplay-verified support: the game server ID and access key
are still unknown, so no route, quadlet, certificate name or catalogue
entry exists yet.

## Client facts and provenance

The source is the owner's updated client executable extracted during the
88-game sweep. Raw inputs and extraction tools remain outside the repository
in `~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/0100750001C70000/`
(`main.bin`, 117958696 bytes, NSO) with earlier analysis attempts in
`~/REPOS/Openpak/scratch/nex-second-batch-20261012/`. Only sanitized
constants and instruction observations are recorded here.

| Title | Application ID | Update content version | Game server ID | Public PRUDP access key | SDK |
| --- | --- | ---: | --- | --- | --- |
| Duck Game | `0100750001C70000` | 262144 | `25E42700` | unknown | NEX 4.6.2, Pia 5.12.1, MM-only |

- The game server ID is console-measured, not guessed: one second after
  Duck Game's aauth (application `0100750001c70000`, version `00040000`)
  at 2026-10-12 21:12:42 UTC, the console dialled
  `g25e42700-lp1.s.n.srv.nintendo.net`, logged by the nx-baas catch-all at
  21:12:43 and answered 404. The dial was followed by a WebSocket GET `/`
  with an empty user agent, also 404; whether the game tunnels its NEX
  transport over WebSocket is the first retest's observation.
- The access key is still unknown (see the exhausted recovery attempts
  below), so the title runs with unverified CONNECT signatures until a
  logged handshake verifies it.

- Markers: `SDK MW+Nintendo+NEX-4_6_2-`, `SDK MW+Nintendo+Pia-5_12_1`,
  `MatchmakeExtension`, `NexNetwork`, `NgsFacade` RTTI, LDN. Host strings
  are the generic `n/p/s.n.srv.nintendo.net` only; no baked
  `g<id>-lp1` hostname.
- The auth hostname is formatted at runtime: `g%08x-%%.s.n.srv.nintendo.net`
  (file `0x52a29dd`, vaddr `0x52a28dd`) and the Pia discovery hostname
  `g%08x.%%.p.srv.nintendo.net` (file `0x52a5040`). The server ID is an
  integer argument, not a string literal.
- Key recovery was attempted and failed: the main carries no 8-char ASCII
  key literal (the only lowercase-hex runs are the OnlineCore build-path
  hash `7923402a` and a libstdc++ constant), no `adrp x3` / `movz w2`
  same-call login pair, and the prior UTF-16 sweep found nothing key-like
  (dominated by repeated crypto constants). A linear `movz`/`movk` scan
  yields ~2500 constants in the server-ID range, all false positives
  without a call-site pairing.
- The binary links MatchmakeExtension, NATTraversal and SecureConnection
  only. The single `Ranking` hit is `LanMakeHostCandidateRanking`, a
  LAN/mesh job name, not NEX Ranking. No Ranking2, Referee or DataStore.

## Initial profile

- `prudps` secure station URLs, so players present their own tickets even
  when they share an address. The endpoint also supports ticketless
  clients; the first game run must establish which secure path is used.
- Legacy public station shape (`type=3`, no `Pa`): Pia 5.12.1 predates the
  5.19 cutover, so the pre-5.19 shape is the measured-era choice.
- A separate matchmaking store, normalized creation (creator counted,
  32-byte session key), full-session join replies and participant recaps.
  The echo shape is provisional: a strict reader rejects the bare boolean
  with 2306-0116, as risk-of-rain-returns showed. These reuse the fixes
  measured on other OpenPak NEX titles; their exact applicability to Duck
  Game remains to be validated.
- No Ranking, Ranking2, referee relay, DataStore service, forced-open
  sessions, parameter stamping (`@SR`/`@GIR`) or title-specific settings
  without client evidence. FinalizeParams stays off: nothing measured says
  this title wants them, and ACNH breaks with them.
- Empty access key is deliberate, as on risk-of-rain-returns: CONNECT
  signatures go unverified until a logged handshake verifies the real key.
  The scaffold test pins the key empty so filling it in later is a
  visible, reviewed change.

## Deployment bundle

| Title selector | Auth | Secure | Health | Auth SNI |
| --- | ---: | ---: | ---: | --- |
| `duck-game` | 23100 | 23101 | 23102 | `g25e42700-lp1.s.n.srv.nintendo.net` |

**Deployed 2026-10-12 ~21:20 UTC** (local binary mount, not a published
release). The block sits inside the planned 23000–23499 Switch-NEX range.

- `deploy/nex-duck-game.yml` merged into the Traefik TCP maps (backup
  `dynamic.before-duck-game-20261012T212500Z.yml` on the host); exactly one
  router plus one service added, unrelated maps preserved. Existing
  console entrypoint, passthrough TLS and PROXY v2.
- `openpak-switch-duck-game` quadlet: pinned-base image with the local
  arm64 binary mounted over `/app/switch-nexd`
  (`~/openpak/experiments/duck-game-20261012/switch-nexd-arm64`, SHA-256
  `df8e915510384be78b70035f26c94523bffada6a955a03915ee49e3b80dedecd`),
  host networking, own recall/state directory, shared identity
  environment. It joins the existing idle-aware release updater via its
  `openpak-ports=` description.
- Host INPUT accepts secure TCP **23101** (own multiport rule, before the
  REJECT, persisted to `/etc/iptables/rules.v4`) and the internal-net
  range extended to 22000:23102 so health **23102** stays reachable
  internally. Health is not exposed publicly.
- Public verification 2026-10-12: SNI route completes TLS against the
  OpenPak CA on 443; direct secure 23101 negotiates TLS externally; a
  synthetic external client exercises both login methods, ticketed secure
  connects for distinct players on one address and legacy `type=3`
  registration. Rollback: remove the router/service, stop/remove the
  quadlet, drop the 23101 rule. Routing/environment backups stay on the
  host; raw probe artifacts remain in `/tmp/duck-probe` locally.

Still pending: voice-gathering URL list, catalogue/server-health
configuration (catalogue stays out until game-verified), and the release
cut that replaces the binary mount. A local registration plus a healthy
listener does not establish playability.

## Validation and next observations

Local validation 2026-10-12:

- Both login methods mint tickets two players spend from one address;
  legacy station registration returns `type=3` with no `Pa`.
- A four-slot arena is created, browsed, joined (full-session echo with
  32-byte key) and its host stations resolve; the explicit-join recap
  regression covers this profile.
- Validation: the full `GOWORK=off go test -race ./...` suite and
  `GOWORK=off go vet ./...` pass.

Production validation 2026-10-12 (~21:25 UTC): public SNI route, direct
secure TLS, both login methods, distinct ticketed players behind one
address and legacy registration all pass through the deployed container.

Next: re-enter the online menu and record the login method, station URL
and first RPCs. That trace yields the CONNECT signature to verify the
key against and shows whether the WebSocket GET `/` was the game's
transport or a side request. Then finish with host/join in both
directions on separate machines and a full gameplay session before any
playable claim.
