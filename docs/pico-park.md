# Pico Park — 2026-10-27

Pico Park (`01004EA00DF70000`) is registered in `cmd/switch-nexd` as
`pico-park` (port base 23220). It shares the existing transport,
account resolution, matchmaking and NAT/notification handlers. No new
proprietary backend is introduced. This is an initial profile for
client testing, not gameplay-verified support.

## Client facts and provenance

The source is the owner's dump
(`/mnt/media/Emulation/roms/switch/Games/Pico Park
[01004EA00DF70000][v0].nsp`). Raw inputs and extraction tools remain
outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/01004EA00DF70000/`
(`main.bin` decompressed NSO). Only sanitized constants and
instruction observations are recorded here. Recon:
`scratch/decompile-pico-park/RECON.md`.

| Title | Application ID | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- |
| Pico Park | `01004EA00DF70000` | `2C3C9A00` | `c02db590` | NEX 4.6.2, MM/VS |

- TECOPARK custom native engine. The pair is same-call
  construction into a stack config struct: the ID (`movk
  #0x2c3c, lsl #16` over `#0x9a00`) is stored at `[sp]` while
  `x8` carries the key string (fileoff `0x296319`, the single
  `c02db590` occurrence) at `[sp+8]`, consumed by the call at
  `0x21648`.
- The auth hostname formats `g%08x` at runtime, so the derived SNI
  is `g2c3c9a00-lp1.s.n.srv.nintendo.net` — not yet observed on the
  wire.
- Markers: `NEX-4_6_2`, `NEX_MM-4_6_2`, `NEX_VS-4_6_2` (no R2
  banner), `NgsFacade` auth client, `MatchmakeReferee` DDL, secure
  PRUDP. No Pia banner measured.
