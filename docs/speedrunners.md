# SpeedRunners — 2026-10-27

SpeedRunners (`0100C8B00C4AE000`, base v0) is registered in
`cmd/switch-nexd` as `speedrunners` (port base 22360). It shares the
existing transport, account resolution, matchmaking and
NAT/notification handlers. No new proprietary backend is introduced.
This is an initial profile for client testing, not gameplay-verified
support.

## Client facts and provenance

The source is the owner's base dump
(`/mnt/media/Emulation/roms/switch/Games/SpeedRunners[0100C8B00C4AE000][USA][v0].nsp`),
extracted during the 88-game sweep. Raw inputs and extraction tools
remain outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/0100C8B00C4AE000/`
(`main.bin` decompressed NSO, `main.nso`, `global-metadata.dat`).
Only sanitized constants and instruction observations are recorded
here. Recon: `scratch/decompile-speedrunners/RECON.md`.

| Title | Application ID | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- |
| SpeedRunners | `0100C8B00C4AE000` | `2C186B00` | `ae8528ab` | NEX 4.6.3, PiaNex 5.18.0, MM |

- Unity 2018.3.10f1 IL2CPP (metadata v24.1). The pair is named
  managed constants in `Assets.Switch.SwitchOnline` (Il2CppDumper
  `dump.cs`): `GAME_SERVER_ID = 739797760` (`0x2C186B00`) and
  `GAME_SERVER_KEY = "ae8528ab"`.
- Native consumption is same-call construction in
  `Assets.Switch.InitFrameworkTask::Execute` (native `0x34d8a0`):
  the ID is built into `w23` at `0x34db7c` (`mov w23, #0x6b00`) /
  `0x34db80` (`movk w23, #0x2c18, lsl #16`) and stored at
  `[x21+0x1c]` (`0x34dbac`), while the key string object
  (`0x3081E38`, the single `ae8528ab` entry in `stringliteral.json`)
  is stored at `[x21+0x20]` (`0x34dbb4`) — the ID and key land in
  one framework config struct. Stronger than adjacent-config
  (heroes-of-hammerwatch class); runtime/DNS confirmation still
  open.
- The auth hostname formats `g%08x` at runtime
  (`g%08x-%%.s.n.srv.nintendo.net` at rodata `0x27842a7`, loaded at
  `0x1effa70`; the ID is read from the config at `[x19+0x164]`),
  so the derived SNI is `g2c186b00-lp1.s.n.srv.nintendo.net` —
  not yet observed on the wire.
- Harbor (installed binary, direct ROM boot with
  `OPENPAK_REAL_PROGRAMS=none`) reaches main menu → Versus →
  ONLINE → the "link your Nintendo Account" gate; no NEX dials
  are emitted before an account is linked, so the first routed
  boot needs a linked test account. Screenshots and logs:
  `scratch/harbor-eden/run/srv-speedrunners2/`.
- Addresses are virtual addresses in the decompressed base `main`
  (text base 0).
- Markers: `SDK MW+Nintendo+NEX-4_6_3-`,
  `SDK MW+Nintendo+Pia-5_18_0-`, `MatchmakeExtension`,
  `NexNetwork`, `NgsFacade`, `AcquireNexUniqueId*`,
  `AssociateNexUniqueIdWithMyPrincipalId*`, lobby API
  (`CreateAndJoinLobby`, `LobbyEntered/Refused`),
  `RankingClient::*`, `CSpeedRunner.PlatformSwitch`, LDN. Host
  strings are the generic `n/p/s.n.srv.nintendo.net` only; no
  baked `g<id>-lp1` hostname. `Lidgren` is an assembly-name-only
  leftover (no DLL shipped).
