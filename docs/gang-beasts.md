# Gang Beasts — 2026-10-27

Gang Beasts (`0100D58012E12000`, base v0) is registered in
`cmd/switch-nexd` as `gang-beasts` (port base 22390; 22210 was
taken on the production host by the Splatoon 3 service, found at
deploy time 2026-10-27). It shares the
existing transport, account resolution, matchmaking and
NAT/notification handlers. No new proprietary backend is introduced.
This is an initial profile for client testing, not gameplay-verified
support. The Coatsink matchmaker/multiplay leg and UNET lobby stay
out of scope for the NEX registry; see the game repo's
`docs/protocol.md`.

## Client facts and provenance

The source is the owner's base dump
(`/mnt/media/Emulation/roms/switch/Games/Gang Beasts
[0100D58012E12000][v0].nsp`), extracted during the 88-game sweep. Raw
inputs and extraction tools remain outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/0100D58012E12000/`
(`main.bin` decompressed NSO, `main.nso`,
`global-metadata.dat`). Only sanitized constants and instruction
observations are recorded here. Recon:
`scratch/decompile-gang-beasts/RECON.md`.

| Title | Application ID | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- |
| Gang Beasts | `0100D58012E12000` | `2F56A000` | `b9106845` | NEX 4.6.5, PiaNex 5.36.0 |

- Unity 2020.3.5f1 IL2CPP (metadata v29). The pair is named managed
  constants in `CS.CorePlatform.Nintendo.Client.Part.NintendoLobby`
  (Il2CppDumper `dump.cs`): `NEX_SERVER_ID = 794206208`
  (`0x2F56A000`), `NEX_ACCESS_KEY = "b9106845"`. The same class
  carries `LOCAL_COMMUNICATION_ID = 72292340087201792` and
  `WIRELESS_CRYPTOKEY = "Boneloaf_GangBeasts_WirelessCryptoKey"`
  for the LDN path.
- Native consumption is same-struct construction in
  `NintendoLobby::InitialisePiaSettings` (native `0x2c4f9c0`): the
  ID is built as one 64-bit value at `0x2c4fc60` (`mov x8,
  #0xc800`) / `0x2c4fc64` (`movk x8, #0xa000, lsl #32`) /
  `0x2c4fc68` (`movk x8, #0x2f56, lsl #48`) and stored at
  `[x26+0x14]` (`0x2c4fc6c`), so the ID lands at `+0x18` next to
  the `0xC800` tag; the key string object goes to the same config
  at `+0x20` (`0x2c4fb8c`). The key's literal-table slot is filled
  at runtime, so the key-to-login link is provisional until a
  CONNECT verifies it — same honesty class as
  heroes-of-hammerwatch.
- The auth hostname formats `g%08x` at runtime
  (`g%08x-%%.s.n.srv.nintendo.net` at rodata `0x55e3123`, loaded at
  `0x59f424`; the ID is read from the NEX config at `[x19+0x164]`),
  so the derived SNI is `g2f56a00-lp1.s.n.srv.nintendo.net` —
  not yet observed on the wire.
- Addresses are virtual addresses in the decompressed base `main`
  (text base 0).
- Markers: `SDK MW+Nintendo+NEX-4_6_5-`,
  `SDK MW+Nintendo+PiaNex-5_36_0-forNEX-4_6_5`,
  `MatchmakeExtension`, UNET HLAPI (`com.unity.multiplayer-hlapi`),
  `Coatsink.Matchmaker*` / `Coatsink.Multiplay*` namespaces,
  `websocket-sharp`. Host strings are the generic
  `n/p/s.n.srv.nintendo.net` only; no baked `g<id>-lp1` hostname.
