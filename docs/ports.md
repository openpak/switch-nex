# Ports — switch-nex (trimmed)

> Trimmed copy for this repository; the canonical document lives at
> `Openpak/ports.md` and governs. Last synchronised 2026-09-16.

One block per concern, nothing below 20000, nothing at or above 27000 (Photon-Reference and
Steam live there). Every service reads its listeners from `<SVC>_HTTP_ADDR`, `<SVC>_GRPC_ADDR`,
`<SVC>_METRICS_ADDR`; the values below are the defaults and the local-run convention. Each
service owns a block of ten: +0 HTTP, +1 gRPC, +2 metrics/pprof, +3..+9 spare.

The Switch-era NEX blocks are served by `cmd/switch-nexd` (one container, deployed
2026-09-16) except 22100/22120: the shared reference host and MHGU stay on nex-go.
Host pattern: +0 authentication (via Traefik SNI on 443), +1 secure (published
directly — the console dials it straight from the login station URL, so each +1
port needs a firewall ACCEPT before the host's final REJECT), +2 /healthz.

| Block | Title | auth | secure | healthz |
| --- | --- | --- | --- | --- |
| 22100 | `servers/nex` — shared host reference instance | 22100 | 22101 | 22102 |
| 22110 | `servers/nex/titles/mario-party-superstars` — Mario Party Superstars (NEX 4.6.5) | 22110 | 22111 | 22112 |
| 22120 | `servers/nex/titles/monster-hunter-generations-ultimate` — Monster Hunter Generations Ultimate (NEX 4.4.0); auth tcp/wss, secure udp | 22120 | 22121 | 22122 |
| 22130 | `servers/nex/titles/mario-golf-super-rush` — Mario Golf: Super Rush (NEX 4.6.5); wss, one listener carrying both streams | 22130 | — (stream 2 on 22130) | 22132 |
| 22140 | `servers/nex/titles/arms` — ARMS (NEX 4.3.5) | 22140 | 22141 | 22142 |
| 22150 | `servers/nex/titles/mario-kart-8-deluxe` — Mario Kart 8 Deluxe (NEX 4.0.0, ws) | 22150 | 22151 | 22152 |
| 22160 | `servers/nex/titles/super-mario-bros-35` — Super Mario Bros. 35 (NEX 4.6.0); ws, one listener carrying both streams | 22160 | — (stream 2 on 22160) | 22162 |
| 22170 | `servers/nex/titles/mario-tennis-aces` — Mario Tennis Aces (NEX 4.3.5, unverified); wss, two listeners | 22170 | 22171 | 22172 |
| 22180 | `servers/nex/titles/animal-crossing-new-horizons` — Animal Crossing: New Horizons (porting) | 22180 | 22181 | 22182 |
| 22190 | `servers/nex/titles/luigis-mansion-3` — Luigi's Mansion 3 (porting) | 22190 | 22191 | 22192 |
| 22300 | `servers/nex/titles/mario-strikers` — Mario Strikers: Battle League (porting) | 22300 | 22301 | 22302 |
| 22310 | `servers/nex/titles/minecraft` — Minecraft (porting) | 22310 | 22311 | 22312 |
| 22320 | `servers/nex/titles/splatoon-2` — Splatoon 2 (porting) | 22320 | 22321 | 22322 |
| 22330 | `servers/nex/titles/super-mario-maker-2` — Super Mario Maker 2 (porting; DataStore-heavy) | 22330 | 22331 | 22332 |
| 22340 | `servers/nex/titles/super-mario-odyssey` — Super Mario Odyssey (porting) | 22340 | 22341 | 22342 |
| 22350 | `servers/nex/titles/super-smash-bros-ultimate` — Super Smash Bros. Ultimate (porting) | 22350 | 22351 | 22352 |
| 22370 | `servers/switch-nex` — Super Mario 3D World + Bowser's Fury (game server 2B306A00; served by `cmd/switch-nexd`, not console-verified) | 22370 | 22371 | 22372 |
| 22380 | `servers/switch-nex` — Kirby's Dream Buffet (game server 25374500; served by `cmd/switch-nexd`, not console-verified) | 22380 | 22381 | 22382 |
| 22870 | `servers/switch-nex` — Clubhouse Games: 51 Worldwide Classics (010047700D540000, game server 2035BB00; served by `cmd/switch-nexd`, new title — no nex-go predecessor) | 22870 | 22871 | 22872 |

The canonical allocation table lives in Openpak/ports.md.
