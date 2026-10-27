# Astroneer — 2026-10-27

Astroneer (`0100E63013E60000`, UE4 project `Astro`) is registered in
`cmd/switch-nexd` as `astroneer` (port base 23180). It shares the
existing transport, account resolution, matchmaking and
NAT/notification handlers. No new proprietary backend is introduced.
This is an initial profile for client testing, not gameplay-verified
support. Consoles run unpatched: the production pair below replaced
the earlier `openpak_astro_key` patched-client profile (empty key,
fixed SNI `ga57-lp1`), whose patch set is superseded.

## Client facts and provenance

The source is the owner's dumps
(`/mnt/media/Emulation/roms/switch/Games/ASTRONEER
[0100E63013E60000][US][v0][Base].nsp` and the v2293760 update). Raw
inputs and extraction tools remain outside the repository in
`~/REPOS/Openpak/scratch/switch-multiplayer-sweep-20261012/0100E63013E60000/`
(update `main.bin`, build `35DD08251AEDF2D208E594A0390157E9`) and
`~/REPOS/Openpak/scratch/decompile-astroneer/` (base `main.dec`, build
`B707145B9C35A12A1D493F479B7F8538`). Only sanitized constants and
instruction observations are recorded here. Recon:
`scratch/decompile-astroneer/RECON.md`.

| Title | Application ID | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- |
| Astroneer | `0100E63013E60000` | `2EE4D000` | `18dbde6c` | NEX ~4.6.5 UNCONFIRMED, no Pia banner |

- UE4 4.26/4.27, NEX client statically linked. The pair is cooked
  config: both values are `FConfig::GetString` reads (callee
  `0x1a98c7c`) from
  `[/Script/SwitchRuntimeSettings.SwitchRuntimeSettings]` —
  `GameServerId` (single code ref `0xfe3d20`) and
  `GameServerAccessKey` (single code ref `0x10292f8`) — with no
  default literals in code. The values read
  `GameServerId=2EE4D000` and `GameServerAccessKey="18dbde6c"`
  from `BaseSwitchEngine.ini`, identical in the base v0 and
  update v2293760 paks.
- The paks were first thought AES-encrypted; they are fully
  plaintext (0 encrypted entries across 33963 base + 46196 update
  entries, footer index-SHA1 verified) — zlib had been mistaken
  for ciphertext. No AES key was ever needed.
- History: before the plaintext finding, consoles ran the
  `openpak_astro_key` patch (empty key, fixed SNI `ga57-lp1`;
  `astroneer` repo `tools/astro-key/`). The patch set is now
  superseded and the `ga57-lp1` router retired for the derived
  `g2ee4d000-lp1.s.n.srv.nintendo.net` — not yet observed on the
  wire.
- Markers: `nn::nex::*` (~6800 mangled names in an unreferenced
  blob), StationURL/PRUDP/NAT-traversal clients, protocols
  Authentication, SecureConnection, MatchMaking (+Ext),
  MatchmakeExtension, NATTraversal (+Relay), Ranking2,
  TicketGranting, Notification, Dns, Http, NATCheck, Health,
  Monitoring, Kerberos, NgsBridge. No DataStore/Utility/Messaging/
  Subscriber clients. No `NEXConfig` symbol in this NEX build.
- Runtime confirmation needs the first patched-client online
  attempt: DNS for `ga57-lp1.s.n.srv.nintendo.net` (already routed
  by the `*.s.n.srv` wildcard) and an empty-key CONNECT.
  Unpatched clients keep failing against OpenPak until the pak
  decrypts or a handshake is captured for brute force.
