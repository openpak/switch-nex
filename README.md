# Switch NEX

OpenPak's Switch NEX Server.

| | |
| --- | --- |
| Network | Nintendo Switch |
| Protocol | NEX 4.x over PRUDPLite/TLS |
| Image | `ghcr.io/openpak/switch-nex` |

## Titles

| Game | Title ID | Module |
| --- | --- | --- |
| [Animal Crossing: New Horizons](https://openpak.org/titles#t-6) | — | `animal-crossing-new-horizons` |
| [ARMS](https://openpak.org/titles#t-11) | — | `arms` |
| [Castle Crashers Remastered](https://openpak.org/titles) | `010001300D14A000` | `castle-crashers-remastered` |
| [Clubhouse Games: 51 Worldwide Classics](https://openpak.org/titles#t-31) | `010047700D540000` | `clubhouse-games-51` |
| [Don't Starve Together](https://openpak.org/titles#t-143) | `010090100E334000` | `dont-starve-together` |
| [Duck Game](https://openpak.org/titles#t-135) | `0100750001C70000` | `duck-game` |
| [Full Metal Furies](https://openpak.org/titles#t-72357) | `010079300AD54000` | `full-metal-furies` |
| [Heroes of Hammerwatch](https://openpak.org/titles#t-72361) | `0100D2B00BC54000` | `heroes-of-hammerwatch` |
| [Kirby's Dream Buffet](https://openpak.org/titles#t-8708) | `0100A8E016236000` | `kirbys-dream-buffet` |
| [Luigi's Mansion 3](https://openpak.org/titles#t-16) | — | `luigis-mansion-3` |
| [Mario Golf: Super Rush](https://openpak.org/titles#t-13) | `0100726014352000` | `mario-golf-super-rush` |
| [Mario Kart 8 Deluxe](https://openpak.org/titles#t-4) | `0100152000022000` | `mario-kart-8-deluxe` |
| [Mario Party Superstars](https://openpak.org/titles#t-14) | — | `mario-party-superstars` |
| [Mario Strikers: Battle League](https://openpak.org/titles#t-15) | — | `mario-strikers-battle-league` |
| [Mario Tennis Aces](https://openpak.org/titles#t-12) | `0100BDE00862A000` | `mario-tennis-aces` |
| [Minecraft: Nintendo Switch Edition](https://openpak.org/titles#t-72363) | — | `minecraft` |
| [Monopoly for Nintendo Switch](https://openpak.org/titles#t-72366) | `01007430037F6000` | `monopoly` |
| [Nobody Saves the World](https://openpak.org/titles#t-72370) | `0100787014460000` | `nobody-saves-the-world` |
| [Pico Park](https://openpak.org/titles#t-137) | `01004EA00DF70000` | `pico-park` |
| [Risk of Rain](https://openpak.org/titles#t-72352) | `0100AAA00ACBE800` | `risk-of-rain` |
| [Risk of Rain 2](https://openpak.org/titles#t-130) | `010076D00E4BA000` | `risk-of-rain-2` |
| [Risk of Rain Returns](https://openpak.org/titles#t-131) | `0100D1801A0F4000` | `risk-of-rain-returns` |
| [Splatoon 2](https://openpak.org/titles#t-9) | — | `splatoon-2` |
| [Streets of Rage 4](https://openpak.org/titles#t-126) | `0100EC9010258000` | `streets-of-rage-4` |
| [Super Mario 3D World + Bowser's Fury](https://openpak.org/titles#t-8707) | — | `super-mario-3d-world-bowsers-fury` |
| [Super Mario Bros. 35](https://openpak.org/titles#t-18) | — | `super-mario-bros-35` |
| [Super Mario Maker 2](https://openpak.org/titles#t-10) | — | `super-mario-maker-2` |
| [Super Mario Odyssey](https://openpak.org/titles#t-7) | — | `super-mario-odyssey` |
| [Super Smash Bros. Ultimate](https://openpak.org/titles#t-5) | — | `super-smash-bros-ultimate` |

One container serves every title. Each Module is a registry entry in
`cmd/switch-nexd/main.go`. `titles/mario-kart-8-deluxe/` holds that title's notes, and
`cmd/mk8d` is its standalone server.

## What it serves

| Host | What | Served by |
| --- | --- | --- |
| `g<8 hex>-lp1.s.n.srv.nintendo.net` | NEX auth and secure stages (the hex is the game server ID) | `switch-nexd` |

## Run

```sh
GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o switch-nexd-arm64 ./cmd/switch-nexd
```

Ports, environment and the production runbook: [deploy.md](docs/deploy.md).

## Docs

- [Protocol](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy](docs/deploy.md)
- [Notes](docs/notes.md)
- [Parity record](docs/parity.md): per-title comparison with the measured server implementations
- [Duck Game](docs/duck-game.md): relay investigation
- [Full Metal Furies](docs/full-metal-furies.md)
- [Gap report](docs/gap-report.md): server parity gaps
- [Heroes of Hammerwatch](docs/heroes-of-hammerwatch.md)
- [Household networking](docs/household-networking.md)
- [Identity gap](docs/identity-gap.md)
- [Login shape conflict](docs/login-shape-conflict.md)
- [Monopoly](docs/monopoly.md)
- [First new NEX titles](docs/nex-first-batch.md)
- [Unexplained push](docs/unexplained-push.md)
- [Protocol facts](SPEC.md)
- [Migration record](MIGRATION.md)
