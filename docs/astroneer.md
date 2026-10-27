# Astroneer — 2026-10-27

Astroneer (`0100E63013E60000`, UE4 project `Astro`) is registered in
`cmd/switch-nexd` as `astroneer` (port base 23180). It shares the
existing transport, account resolution, matchmaking and
NAT/notification handlers. No new proprietary backend is introduced.
This is a patched-client profile for client testing, not
gameplay-verified support: consoles run the `openpak_astro_key` patch
and the title serves them with an empty access key.

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
| Astroneer | `0100E63013E60000` | `A57` (patched routing ID; real ID unknown) | `""` (empty; patched clients only) | NEX ~4.6.5 UNCONFIRMED, no Pia banner |

- UE4 4.26/4.27, NEX client statically linked. The production pair is
  unrecoverable statically: both values are `FConfig::GetString`
  reads (callee `0x1a98c7c`) from
  `[/Script/SwitchRuntimeSettings.SwitchRuntimeSettings]` —
  `GameServerId` (single code ref `0xfe3d20`) and
  `GameServerAccessKey` (single code ref `0x10292f8`) — with no
  default literals in code. The values live only in the
  AES-encrypted `Astro-Switch.pak` (`DefaultEngine.ini`), and no
  UE4 pak/AES key strings exist in the binary.
- The patch (`astroneer` repo `tools/astro-key/`, owner-approved
  2026-10-27, shipped to consoles as
  `exefs_patches/openpak_astro_key`) is two read-only data edits —
  no code cave exists (max `.text` zero run is 15 bytes):
  - Wide `GameServerAccessKey` → `XameServerAccessKey` (first
    character): the read misses and the client takes its guarded
    empty path (zero length selects the NUL default, the narrow
    conversion yields `""`, frees are null-checked). The empty key
    is the documented `risk-of-rain-returns` server shape.
  - SNI format `g%08x-%%.s.n.srv.nintendo.net` →
    `ga57-lp1.s.n.srv.nintendo.net` (exact-fit 30 bytes). The game
    server ID is never validated — it only selects the server via
    DNS — so the real ini value flows through untouched and only
    the routing name is pinned. No format verbs remain, so both
    format stages pass the fixed string through.
- Covered builds: update v2293760 (`35DD08…`: key name fileoff
  `0x769b10a`, SNI `0x74b600e`) and base v0 (`B70714…`: key name
  `0x55815c0`, SNI `0x5736593`); each string occurs exactly once per
  build with the same code-ref shape.
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
