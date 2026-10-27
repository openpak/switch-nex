# Nobody Saves the World — 2026-10-27

Nobody Saves the World (`0100787014460000`) is registered in
`cmd/switch-nexd` as `nobody-saves-the-world` (port base 23210). It
shares the existing transport, account resolution, matchmaking and
NAT/notification handlers. No new proprietary backend is introduced.
This is an initial profile for client testing, not gameplay-verified
support.

## Client facts and provenance

The source is the owner's dump
(`/mnt/media/Emulation/roms/switch/Games/Nobody Saves the World
[0100787014460000][USA][v0].nsp`). Raw inputs and extraction tools
remain outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/0100787014460000/`
(`main.bin` decompressed NSO). Only sanitized constants and
instruction observations are recorded here. Recon:
`scratch/decompile-nobody-saves-the-world/RECON.md`.

| Title | Application ID | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- |
| Nobody Saves the World | `0100787014460000` | `2CA9E100` | `5a8be9a0` | NEX 4.6.6, MM/VS |

- DrinkBox custom native engine. The pair is same-call
  construction: the ID is built into `w2` at `0x126a80` (`mov
  w2, #0xe100`) / `0x126a84` (`movk w2, #0x2ca9, lsl #16`)
  while `x3` carries the key string (fileoff `0xbfba6d`, the
  single `5a8be9a0` occurrence), consumed by the call at
  `0x126a98`.
- The auth hostname formats `g%08x` at runtime, so the derived SNI
  is `g2ca9e100-lp1.s.n.srv.nintendo.net` — not yet observed on the
  wire.
- Markers: `NEX-4_6_6`, `NEX_MM-4_6_6`, `NEX_VS-4_6_6` (no R2
  banner), `NgsFacade` auth client, `MatchmakeReferee` DDL, secure
  PRUDP. No Pia banner measured.
