# Migrating Switch NEX titles from `servers/nex` to `switch-nex`

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

The switch-nex **runtime core is not implemented yet** — no title can migrate until
it is. Critical path, in order:

1. PRUDPLite frame parser/serializer over TLS-TCP (self-delimiting frames, connect,
   split/ack, ping, data; fragmentation)
2. RMC dispatch (packed method IDs) + TicketGranting method 0x02 handler producing the
   verified `ValidateAndRequestTicketResult` body (`result.go`, byte-verified 2026-09-14)
3. Ticketless secure CONNECT with IP→PID recall + SecureConnection protocol
4. Per-title protocol sets (see table)

Until 1–3 land, every title below stays served by its nex-go container.

## Titles

| Port base | Title | nex-go state | switch-nex state |
|---|---|---|---|
| 22100 | shared reference host | running (test) | n/a — stays nex-go for cross-checks |
| 22110 | Mario Party Superstars | deployed | not started |
| 22120 | Monster Hunter Generations Ultimate | deployed | not started |
| 22130 | Mario Golf: Super Rush | deployed (wss) | not started |
| 22140 | ARMS | deployed | not started |
| 22150 | **Mario Kart 8 Deluxe** | deployed (debugged 2026-09-14) | **pilot** — migration target |
| 22160 | Super Mario Bros. 35 | deployed | not started |
| 22170 | Mario Tennis Aces | deployed (unverified) | not started |
| 22180 | Animal Crossing: New Horizons | porting | not started |
| 22190 | Luigi's Mansion 3 | porting | not started |
| 22300 | Mario Strikers: Battle League | porting | not started |
| 22310 | Minecraft | porting | not started |
| 22320 | Splatoon 2 | porting | not started |
| 22330 | Super Mario Maker 2 | porting (DataStore-heavy) | not started |
| 22340 | Super Mario Odyssey | porting | not started |
| 22350 | Super Smash Bros. Ultimate | porting | not started |

## Per-title cutover checklist

1. Per-title config under `titles/<name>/` (ports, station URL address, protocol set)
2. Runtime milestones 1–3 green against the title
3. Parallel run: nex-go container + switch-nex binary side by side on different ports;
   console/emulator traffic compared (SPEC.md capture method)
4. Traefik SNI route re-pointed (`g<id>-lp1.s.n.srv.nintendo.net` → the switch-nex
   deployment); nex-go container stopped
5. 2306-xxxx watch: any new error code is a regression until triaged
