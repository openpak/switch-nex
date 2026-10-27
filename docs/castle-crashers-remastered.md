# Castle Crashers Remastered — 2026-10-27

Castle Crashers Remastered (`010001300D14A000`) is registered in
`cmd/switch-nexd` as `castle-crashers-remastered` (port base 23200). It
shares the existing transport, account resolution, matchmaking and
NAT/notification handlers. No new proprietary backend is introduced.
This is an initial profile for client testing, not gameplay-verified
support.

## Client facts and provenance

The source is the owner's dump
(`/mnt/media/Emulation/roms/switch/Games/Castle Crashers Remastered
[010001300D14A000][v0][Base].nsp`). Raw inputs and extraction tools
remain outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/010001300D14A000/`
(`main.bin` decompressed NSO). Only sanitized constants and
instruction observations are recorded here. Recon:
`scratch/decompile-castle-crashers-remastered/RECON.md`.

| Title | Application ID | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- |
| Castle Crashers Remastered | `010001300D14A000` | `2458D100` | `effc17b3` | NEX 4.6.3, MM/R2/VS |

- The Behemoth custom native engine. The pair is same-call
  construction: the ID is built into `w2` at `0x19338c` (`mov
  w2, #0xd100`) / `0x193390` (`movk w2, #0x2458, lsl #16`)
  while `x3` carries the key string (fileoff `0x30307b`, the
  single `effc17b3` occurrence), consumed by the call at
  `0x1933a8`.
- The base-v0 dump decrypts with `title.keys` and carries the same
  key at fileoff `0x307ec3` — the pair is confirmed across two
  independent mains.
- The auth hostname formats `g%08x` at runtime, so the derived SNI
  is `g2458d100-lp1.s.n.srv.nintendo.net` — not yet observed on the
  wire.
- Markers: `NEX-4_6_3`, `NEX_MM-4_6_3`, `NEX_R2-4_6_3`,
  `NEX_VS-4_6_3`, `MatchmakeExtension`/`Session`, Ranking2. No Pia
  banner measured.
