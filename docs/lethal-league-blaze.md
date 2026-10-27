# Lethal League Blaze — 2026-10-27

Lethal League Blaze (`01003AB00983C000`, base v0) is registered in
`cmd/switch-nexd` as `lethal-league-blaze` (port base 23170). It
shares the existing transport, account resolution, matchmaking and
NAT/notification handlers. No new proprietary backend is introduced.
This is an initial profile for client testing, not gameplay-verified
support.

## Client facts and provenance

The source is the owner's base dump
(`/mnt/media/Emulation/roms/switch/Games/Lethal League Blaze
[01003AB00983C000][v0].nsp`), extracted during the 88-game sweep. Raw
inputs and extraction tools remain outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/01003AB00983C000/`
(`main.bin` decompressed NSO, `main.nso`,
`global-metadata.dat`). Only sanitized constants and instruction
observations are recorded here. Recon:
`scratch/decompile-lethal-league-blaze/RECON.md`.

| Title | Application ID | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- |
| Lethal League Blaze | `01003AB00983C000` | `21117E00` | `a3267987` | NEX 4.6.2, Pia 5.14.0, PIA_UNITY 0.13.0 |

- Unity 2017.4.17f1 IL2CPP (metadata v24.1). No managed
  `GAME_SERVER_*` constants exist (unlike SpeedRunners/Gang
  Beasts); the pair is same-struct native construction in
  `PiaSettings::GetInternetInitSettings` (native `0x10cfb80`):
  the ID is built into `w21` at `0x10cfc28` (`mov w21, #0x7e00`)
  / `0x10cfc2c` (`movk w21, #0x2111, lsl #16`) and stored at
  `[x19+0x1c]` (`0x10cfc50`), while the key string object from
  literal slot `0x33461F0` (pointer `0x35AFB70`, the managed
  literal `"a3267987"`) lands at `[x19+0x20]`. The
  `0xC800`-tag/ID/key layout matches the SpeedRunners and Gang
  Beasts config structs exactly.
- The auth hostname formats `g%08x` at runtime
  (`g%08x-%%.s.n.srv.nintendo.net` at rodata `0x299efe1`, loaded at
  `0x217f2b0`; the ID is read from the NEX config at `[x19+0x164]`),
  so the derived SNI is `g21117e00-lp1.s.n.srv.nintendo.net` —
  not yet observed on the wire.
- Negative search: no 8-hex NUL-delimited literal in native
  `.rodata`, no 8-char managed literal besides the key, no RomFS
  config copy — the slot-pointer→literal match is the key's only
  static witness besides its shape and struct slot.
- Addresses are virtual addresses in the decompressed base `main`
  (text base 0).
- Markers: `SDK MW+Nintendo+NEX-4_6_2-`,
  `SDK MW+Nintendo+Pia-5_14_0`, `MatchmakeExtension`,
  `NexNetwork`, `NgsFacade`, `nn::pia::nex::NexMatch*Job` RTTI
  (Create/Destroy/Browse/Random/Join/Leave), `FixMath.NET`
  rollback netcode, LDN. Host strings are the generic
  `n/p/s.n.srv.nintendo.net` (plus `nncs1/2`) only; no baked
  `g<id>-lp1` hostname.
