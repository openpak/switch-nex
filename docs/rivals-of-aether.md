# Rivals of Aether — 2026-10-27

Rivals of Aether (`0100D2700DE7C000`, update v655360) is registered in
`cmd/switch-nexd` as `rivals-of-aether` (port base 23160). It shares the
existing transport, account resolution, matchmaking and
NAT/notification handlers. No new proprietary backend is introduced.
This is an initial profile for client testing, not gameplay-verified
support.

## Client facts and provenance

The source is the owner's dump
(`/mnt/media/Emulation/roms/switch/Games/Rivals of Aether
[0100D2700DE7C000] [v0].nsp`), whose base-v0 program NCA is titlekey
crypto the local keys cannot open; static analysis reused the sweep's
already-decrypted update main. Raw inputs and extraction tools remain
outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/0100D2700DE7C000/`
(`main.bin` decompressed NSO, 76.8 MB). Only sanitized constants and
instruction observations are recorded here. Recon:
`scratch/decompile-rivals-of-aether/RECON.md`.

| Title | Application ID | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- |
| Rivals of Aether | `0100D2700DE7C000` | `2A28FB00` | `8f0c5daa` | NEX 4.6.4, MM/R2/VS, no Pia banner |

- GameMaker Studio 2 runner
  (`GameMaker_Studio_2-7.7.1.438-Release`). The pair shares one
  GameMaker literal pool in the update main: the key `"8f0c5daa"`
  sits at file offset `0x417ce95` between the NEX login format
  strings (`"Error! NEX log in failed because the user is
  offline"`, `"Error during NEX login"`, `"NEX login successful"`,
  `"NEX online token is \""`), and the double `706199552.0`
  (`0x2A28FB00`, f64 bytes `000000807d14c541`) follows at
  `0x417cee8` — the complete literal set of the GML NEX-login
  function. Both literals occur exactly once in the main; the ID
  fits the third-party shape (low byte `0x00`, high byte in the
  measured `0x21`–`0x2F` range) and the key fits the 8-hex-char
  access-key convention. Same-pool evidence, one step below
  same-call construction; runtime/DNS confirmation still open.
- The auth hostname formats `g%08x` at runtime (`g%08x` at rodata
  `0x3cee6cc`, next to `srv.nintendo` at `0x3cee6d9`), so the
  derived SNI is `g2a28fb00-lp1.s.n.srv.nintendo.net` — not yet
  observed on the wire.
- Offsets are file offsets in the decompressed update `main`
  (text base 0).
- Markers: `NEX-4_6_4`, `NEX_MM-4_6_4`, `NEX_R2-4_6_4`,
  `NEX_VS-4_6_4`, `MatchmakeExtension` (+DDL), `MatchmakeReferee`,
  `MatchmakeSession`/RendezVous session passwords,
  `Ranking`/`Ranking2` clients, Secure PRUDP (`prudp:/`,
  `prudps:`, `PRUDPEndPoint.cpp`). No Pia banner (remaining
  "pia" hits are Olympia-fighter game data). Host strings are the
  generic `nncs1/n.n.srv.nintendo.net` and `s.n.srv.nintendo.net`
  only; no baked `g<id>-lp1` hostname.
