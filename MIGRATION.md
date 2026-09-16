# Migrating Switch NEX titles from `servers/nex` to `switch-nex`

**Deployed 2026-09-16** on quilted-waffle: `openpak-switch-nexd.service`
(container `switch-nexd`, image `localhost/switch-nexd:latest`, built from
`deploy/switch-nex/` on the host) serves all 14 titles below; auth via
Traefik SNI (`switch-nexd:<base>` on openpak.network), secure ports published
directly. The nex-go title services are stopped and disabled; MHGU and the
Wii U / 3DS fleet stay on nex-go. Traefik delta:
`dynamic.before-switch-nex-cutover-v001.yml` holds the previous state.

Decision 2026-09-14: the **nex-go lineage keeps Wii U / 3DS titles** (UDP PRUDP —
Mario Kart 7, Mario Kart 8 Wii U, MH4/MH4U/MHGen, Pikmin 3, Tetris Axis, Team Kirby
Clash Deluxe, Yo-kai Watch Blasters, Super Street Fighter IV, Ice Station Z, Metroid
Prime Federation Force, Pokémon Rumble World, Minecraft Wii U, Tri Force Heroes,
Resident Evil: Mercenaries 3D, Wii U Chat, Splatoon (Wii U), Mario vs Donkey Kong).
**Switch-era titles move to `switch-nex`** — they need raw PRUDPLite-over-TLS transport,
kerberos v1 tickets, 8-byte PIDs, structure headers, and ticketless secure CONNECT,
none of which the nex-go websocket path provides correctly (write framing bug,
2026-09-14).

## Status

The switch-nex runtime core is implemented (PRUDPLite over TLS, RMC dispatch,
ticket minting, ticketless secure CONNECT with IP→PID recall) and the
per-title protocol sets are implemented at the working-reference behaviour
level for every Switch-era NEX title except MHGU (UDP transport — stays on
nex-go). The per-title comparison, behaviour notes and remaining deviations
are in `docs/legacy-parity.md`.

## Titles

| Port base | Title | nex-go state | switch-nex state |
|---|---|---|---|
| 22100 | shared reference host | running (test) | n/a — stays nex-go for cross-checks |
| 22110 | Mario Party Superstars | deployed | **parity implemented** (matchmaking, settings tables) |
| 22120 | Monster Hunter Generations Ultimate | deployed (udp) | **out of scope** — UDP transport, stays nex-go |
| 22130 | Mario Golf: Super Rush | deployed (wss) | **parity implemented** |
| 22140 | ARMS | deployed | **parity implemented** (DataStore stub) |
| 22150 | **Mario Kart 8 Deluxe** | deployed (debugged 2026-09-14) | **pilot** — parity implemented + wire-verified login |
| 22160 | Super Mario Bros. 35 | deployed | **parity implemented** (Ranking2, Referee, settings) |
| 22170 | Mario Tennis Aces | deployed (unverified) | **parity implemented** (open-participation fix) |
| 22180 | Animal Crossing: New Horizons | porting | **parity implemented** (0x33 full-session, 0x2C codeword) |
| 22190 | Luigi's Mansion 3 | porting | **parity implemented** (0x79 presence stub) |
| 22300 | Mario Strikers: Battle League | porting | **parity implemented** |
| 22310 | Minecraft | porting | **parity implemented** |
| 22320 | Splatoon 2 | porting | **parity implemented** (splatfest empties) |
| 22330 | Super Mario Maker 2 | porting (DataStore-heavy) | **parity implemented** (GetUsers shape; Course World generic) |
| 22340 | Super Mario Odyssey | porting | **parity implemented** (Utility + DataStore only) |
| 22350 | Super Smash Bros. Ultimate | porting | **parity implemented** |
| 22870 | Clubhouse Games: 51 Worldwide Classics | — (new title, no nex-go predecessor) | **parity implemented** (account-setup DataStore, forced-open + normalised sessions, join session echo) |

## Per-title cutover checklist

1. Per-title config under `titles/<name>/` (ports, station URL address, protocol set)
2. Runtime milestones 1–3 green against the title
3. Parallel run: nex-go container + switch-nex binary side by side on different ports;
   console/emulator traffic compared (SPEC.md capture method)
4. Traefik SNI route re-pointed (`g<id>-lp1.s.n.srv.nintendo.net` → the switch-nex
   deployment); nex-go container stopped
5. 2306-xxxx watch: any new error code is a regression until triaged
