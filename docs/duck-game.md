# Duck Game — relay investigation, gameplay unverified

Duck Game (`0100750001C70000`, update 1.0.4 / content 262144) is registered
in `cmd/switch-nexd` as `duck-game`. It shares the existing transport,
account resolution, matchmaking and NAT/notification handlers. It has a
production route and per-title quadlet, but gameplay remains unverified.
The access key is unknown; the current profile deliberately uses an empty key.

## 2026-10-13: independently verified relay infrastructure

- Checked workspace/subrepo status and recent history before edits. The workspace
  root is not a Git repository; switch-nex contains concurrent uncommitted
  household/connectivity work. That work was retained.
- Production connectivity actually used rootless bridge networking and published
  UDP ports 22900–22979. Migrated it to `Network=host`, removed port publishing,
  and moved all 18 NPLN API URLs to `http://host.containers.internal:20150`.
  NPLN remains on `openpak.network`; coturn and title binaries were unchanged.
- The host alias resolves to `10.0.0.249`. The API now listens on IPv4
  `0.0.0.0:20150`; existing host INPUT rules admit loopback/local host routes
  and reject other traffic to this port. An external HTTP probe timed out.
  Do not add a public API firewall permit.
- Added and persisted an explicit non-loopback INPUT rejection for TCP 20150,
  ahead of other firewall rules. All 18 actual-container authenticated ICE
  probes passed again afterward. Live service-issued Pia-shaped TURN allocation,
  channel binding and bidirectional relay also passed after the migration.
- All 18 titles were TCP-idle before migration. Each passed health, exact binary
  hash, and authenticated participant registration / ICE allocation / release
  using its actual mounted token from its actual container network namespace.
  This checks infrastructure, not 18 retail game sessions.
- Exact backups and journal:
  `~/openpak/deploy/connectivity/releases/20261013-npln/host-network-20261013T155605Z/`.
  The original NPLN rollout manifest's candidate hashes were updated so its
  existing guarded per-title rollback still works. An initial attempt rolled
  back before any NPLN restart because `:U` token ownership prevented host-side
  reads; the successful attempt reads tokens inside their owning containers.
- External two-socket proof passed: STUN observations from coturn matched
  independent nonce-tagged packet captures **at the allocated relay destination**.
  After those exact observations were registered and bound, both directions
  delivered the correct payload with the relay as source. A third unbound socket
  was dropped. Namespace counters: 48 forwarded bytes, 3 drops (two inactive
  observation packets plus the third-source packet). Probe leases/socket released.
  Reproducible operator tool: `openpak/connectivity/tools/probe_host_relay.py`.
- Full `GOWORK=off go test -race ./...` and `go vet ./...` passed in connectivity,
  Switch NEX and NPLN before the retail probe candidate was built.

### Remaining native-client blocker

The current Switch NEX relay adapter selects LAN/reported-public candidates for
BindRelay. The real shared service correctly rejects these: only an `observed`
candidate with `destination` equal to that exact relay qualifies. The Switch NEX
candidate type does not yet carry `destination`, and no native Pia verifier
exists. The previous TLS relay test uses a permissive fake service; it proves
station-list plumbing, **not a valid real-service bind or gameplay**.

For a bounded Duck-only observation, `SWITCH_NEX_RELAY_PROBE_ONLY=1` advertises an
allocated **inactive** relay station without calling BindRelay. It cannot forward
traffic. Direct stations are retained and allocations release on leave/teardown.
This operator canary is not production relay support and must be removed after
the measurement. Other titles cannot enter this mode.

Probe-only retail candidate deployed 2026-10-13 16:04:29 UTC, SHA-256
`cb18ce9e5f929b563a4d2a420fc8fa6436873e2226bfba7cf2eedbb5a03bbbfe`.
The parallel connectivity agent had replaced the old experimental mount with
`releases/20261013-switch-duck-game/switch-nexd` (hash
`fc3099bfbd61ea2800f1558a475854e03c93f5ad8fe68dac639b32166988f5e4`).
The first canary deployment guard refused the obsolete mount; the next backed
up and preserved the newer deployment instead of restoring historical binaries.
Exact quadlet backup:
`~/.config/containers/systemd/openpak-switch-duck-game.container.before-probe-only-20261013T160429Z`.
Retail packet capture: `/tmp/duck-relay-retail-20261013.pcap` on production.
No TEMP-DIAG station-address logger was deployed by this session.
The first 180-second capture contained zero packets and no retail clients
connected during that window; this is **not** evidence that Pia ignored a station.
A bounded rotating capture (`/tmp/duck-relay-retail-live.pcap0`, up to two 2 MB
files, 15-minute timeout) was started at 16:07 UTC while awaiting the owner's
Switch-host / Harbor-join run. No retail result is recorded yet.

After the run, restore the exact newer quadlet backup above if the live quadlet
still mounts `switch-nexd-arm64-probe-only` and sets the probe environment flag,
then reload user systemd and restart `openpak-switch-duck-game.service` while
the title is idle. Preserve any subsequent operator changes. Do not restore the
historical `.before-diag` binary over the newer connectivity deployment.

Two local Atmosphere reports (`/tmp/duck-crash-1018.log` and
`/tmp/duck-crash-1131.log`) contain a User Break with `nn::util::CompressZlib` on
the main-thread stack. They establish the abort location, not that a failed mesh,
WiFi delay, CGNAT, or server response caused it. Claims in the previous handoff
about six reports, timing and causality have not been independently reproduced.

Protocol research leads, still requiring retail capture: Pia 5.11–5.17 uses
header version 3; the encrypted messages use a matchmaking session key. See
[Pia packet format](https://github.com/kinnay/NintendoClients/wiki/Pia-Protocol),
[station request](https://github.com/kinnay/NintendoClients/wiki/Station-Protocol)
and [native NAT probe format](https://github.com/kinnay/NintendoClients/wiki/NAT-Traversal-Protocol-%28Pia%29).
Do not promote an unencrypted claimed PID/RVCID, guessed LAN port, or observation
from another STUN destination into a verified relay mapping.

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
