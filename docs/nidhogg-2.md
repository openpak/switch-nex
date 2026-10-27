# Nidhogg 2 — 2026-10-27

Nidhogg 2 (`0100C8700B0B6000`) is registered in `cmd/switch-nexd` as
`nidhogg-2` (port base 23230). It shares the existing transport,
account resolution, matchmaking and NAT/notification handlers. No new
proprietary backend is introduced. This is an initial profile for
client testing, not gameplay-verified support.

## Client facts and provenance

The source is the owner's dump
(`/mnt/media/Emulation/roms/switch/Games/Nidhogg 2
[0100C8700B0B6000][v0][US][Base].nsp`). Raw inputs and extraction
tools remain outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/0100C8700B0B6000/`
(`main.bin` decompressed NSO). Only sanitized constants and
instruction observations are recorded here. Recon:
`scratch/decompile-nidhogg-2/RECON.md`.

| Title | Application ID | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- |
| Nidhogg 2 | `0100C8700B0B6000` | `28DDB000` | `c95df951` | NEX 4.5.0, MM/VS, GameMaker |

- GameMaker Studio 2 runner. The pair shares the GameMaker login
  literal pool, duplicated across two data segments: `"c95df951"`
  at fileoff `0xebf093` and `0xecaa9d` beside the SWITCH NET login
  strings, with the double `685617152.0` (`0x28DDB000`) at
  `0xebf148` and `0xecab20` respectively — the only two
  occurrences of each, pairing the same values twice. Same-pool
  evidence, one step below same-call construction.
- The auth hostname formats `g%08x` at runtime, so the derived SNI
  is `g28ddb000-lp1.s.n.srv.nintendo.net` — not yet observed on the
  wire.
- Markers: `NEX-4_5_0`, `NEX_MM-4_5_0`, `NEX_VS-4_5_0` (no R2
  banner), `MatchmakeReferee` DDL, secure PRUDP (`prudp:/`,
  `prudps:/`). No Pia banner (NEX 4.5.0 predates it).
