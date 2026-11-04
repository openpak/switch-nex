# Switch NEX

OpenPak's Switch NEX Server.

| | |
| --- | --- |
| Network | Nintendo Switch |
| Protocol | NEX 4.x over PRUDPLite/TLS |
| Image | `ghcr.io/openpak/switch-nex` |
| Licence | AGPL-3.0-only |

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

Rankings: with `MK8D_DATABASE_URL` set, `mario-kart-8-deluxe` also serves the OpenPak rankings contract (read-only, container network only) on `RANKINGS_ADDR` (for example `:9090`); empty leaves it off.

Scores of the other titles: every title with Ranking (0x70) or Ranking2 (0x7A) keeps what the code parses (category and score; best per board, honouring the order flag and update mode) in `internal/scorestore`: Postgres schema `scores` when `SWITCH_NEX_DATABASE_URL` is set (created on start), memory otherwise. Splatoon 2 also keeps each player's summed Splatfest contribution (board `fest<id>`). `RANKINGS_ADDR` serves them through the rankings contract (network `switch`, pid = unsigned decimal NEX pid); the units share the host network, so set it per unit, or `RANKINGS_ADDR=auto` for `:<title port base + 5>` (Mario Golf :22135, Mario Tennis Aces :22175, SMB35 :22165, Kirby's Dream Buffet :22385, Splatoon 2 :22325, Clubhouse Games 51 :22875, Risk of Rain :22895 / Returns :23005 / 2 :23045, Pac-Man 99 :23145, Smash Ultimate :22355). Keep these ports off the public side like the MK8D one. Rewards events: every title with a matchmaking store reports `game_sessions.<title>` (plus its family metric); Splatoon 2 reports `splatfests_joined`, Mario Kart 8 Deluxe `kart_tournaments_entered`.

## Docs

- [Protocol](docs/protocol.md)
- [Deploy](docs/deploy.md)
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
- [Protocol facts](SPEC.md)

PRDs: openpak/prds (private)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).

## Not supported

- Super Mario Odyssey balloon DataStore methods 0x2F-0x35 (openpak/prds#115).
- Splatoon 2 Splatfest GetEventMatchResult and methods 0x18/0x1A (openpak/prds#116).
- Mario Kart 8 Deluxe object bodies need the S3 store configured; without it the body endpoint answers 501.
