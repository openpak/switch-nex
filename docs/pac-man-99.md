# PAC-MAN 99 — 2026-10-24

PAC-MAN 99 (`0100AD9012510000`, update v131072) is registered in `cmd/switch-nexd` as
`pac-man-99` on port block 23140 (auth 23140, secure 23141, healthz 23142) with its Eagle
gameplay relay on 23143. Nintendo closed the official service on 2023-10-08, so there is no
retail traffic to compare with: this is an initial profile derived from the client and from
Super Mario Bros. 35, not yet verified in a game.

## Client facts and provenance

The source is the owner's updated client executable. Only sanitized constants are recorded.

| Title | Application ID | Update | Game server ID | Public PRUDP access key | SDK |
| --- | --- | ---: | --- | --- | --- |
| PAC-MAN 99 | `0100AD9012510000` | 131072 | `2E471600` | `1e6b25ec` | NEX 4.6.5 (MM, R2, UT), libeagle 2.0.5, no Pia |

- The game server ID occurs once as a little-endian literal and the access key once as a
  string in the decompressed `main`; both match Kinnay's public Game Server List.
- The libeagle version string `2.0.5` sits where SMB35's `2.0.4` does (next to the URL
  parser's messages), and the libeagle source tree is the same layout. SMB35 is the same
  NEX generation with the same components.
- Kinnay's public *Eagle Protocol* page lists PAC-MAN 99 as 11-bit nodes, 1024-node bitmap,
  protocol 3; *Eagle RPC (PAC-MAN 99)* names game RPCs 31–36 (ChangeTarget, SendAttack,
  Dead, CharUpdate, Refresh, PowEat), which the relay forwards opaquely like SMB35's.

## Profile

The client's RTTI names its NEX protocol clients: TicketGranting, SecureConnection,
MatchMaking, MatchmakeExtension, Messaging, MessageDelivery, Ranking2 and Utility. SMB35
links the same set plus MatchmakeReferee, which PAC-MAN 99 does not have.

Starts from SMB35's: matchmaking store, Ranking2, MessageDelivery (no referee) and the
Eagle handoff (notification 200000 with `url` + `token`), 16-byte source key. The relay
uses the `PacMan99` profile: 99 players per room, and its login fields (protocol, app
protocol version, DDL hash, version string) are logged instead of enforced until a boot has
measured them. Box-side overrides while unmeasured: `PACMAN99_SOURCE_KEY_BYTES` (16 or 32)
and `PACMAN99_INTEGER_SETTINGS` (the `index:key=value;…` shape).

## Deployment

`deploy/make-quadlets.sh` generates `PACMAN99_EAGLE_LISTEN=:23143` and
`PACMAN99_EAGLE_URL=wss://g2e471600-lp1.s.n.srv.nintendo.net:23143`. The existing NEX leaf
(`*.s.n.srv.nintendo.net`) covers the name. Needs a Traefik SNI route for
`g2e471600-lp1.s.n.srv.nintendo.net` to 23140 and host ACCEPTs for 23141 and 23143.
